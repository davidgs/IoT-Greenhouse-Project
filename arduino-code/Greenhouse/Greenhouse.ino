/**
 * MIT License
 *
 * Copyright (c) 2022 David G. Simmons
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

#if defined(ESP32)
#include <WiFiMulti.h>
WiFiMulti wifiMulti;
#define DEVICE "ESP32"
#elif defined(ESP8266)
#include <ESP8266WiFiMulti.h>
ESP8266WiFiMulti wifiMulti;
#define DEVICE "ESP8266"
#endif
// #include <WiFiSSLClient.h>
#include <WiFiClientSecure.h>
#include <PubSubClient.h>
#include <InfluxDbClient.h>
#include <Wire.h>
#include "ESP32Servo.h"
#include "SparkFun_SCD30_Arduino_Library.h"
#include <DNSServer.h>
#include <ESPmDNS.h>
#include <WiFiUdp.h>
#include <ArduinoJson.h>

#define SERVO_PIN 19
#define FAN_CTL 15
#define PUMP_CTL 18
#define SOIL_CTL 32
#define SOIL A2
#define INFLUXDB_URL "YOUR_INFLUX_URL"
// InfluxDB v2 server or cloud API authentication token (Use: InfluxDB UI -> Data -> Tokens -> <select token>)
#define INFLUXDB_TOKEN "YOUR_INFLUX_TOKEN"
// InfluxDB v2 organization id (Use: InfluxDB UI -> User -> About -> Common Ids )
#define INFLUXDB_ORG "YOUR_INFLUX_ORG"
// InfluxDB v2 bucket name (Use: InfluxDB UI ->  Data -> Buckets)
#define INFLUXDB_BUCKET "YOUR_INFLUX_BUCKET"
#define TZ_INFO "EST5EDT"
#define SENSOR_ID "GRNHSE"
#define SID "YOUR_SSID"
#define PASSWORD "YOUR_SSID_PASSWORD"
#define MQTT_PORT 8883
#define MQTT_TOPIC "greenhouse"
#define CLIENT_ID "GRNHSE"

const char* MQTT_SERVER = "YOUR_MQTT_SERVER";

SCD30 airSensor;
Servo door_ctl;
int pos = 0;
bool FAN_ON = false;
unsigned long lastCO2Millis = 0;
unsigned long lastServoMillis = 0;
unsigned long lastPumpMillis = 0;
const long ServoInterval = 5000;
const long pumpInterval = 10000;
const long readingInterval = 2000;

int failure = 0;
int status = WL_IDLE_STATUS; // the Wifi radio's status
const int loopTimeCtl = 0;
hw_timer_t *timer = NULL;
InfluxDBClient influx(INFLUXDB_URL, INFLUXDB_ORG, INFLUXDB_BUCKET, INFLUXDB_TOKEN);
Point myPoint("greenhouse");
WiFiClient esp32Client;
PubSubClient mqttClient(esp32Client);
byte mac[6];

RTC_DATA_ATTR int bootCount = 0;
void (*resetFunc)(void) = 0; //declare reset function @ address 0
void IRAM_ATTR resetModule()
{
  ets_printf("reboot\n");
  resetFunc();
}
void print_wakeup_reason()
{
  esp_sleep_wakeup_cause_t wakeup_reason;

  wakeup_reason = esp_sleep_get_wakeup_cause();

  switch (wakeup_reason)
  {
  case ESP_SLEEP_WAKEUP_EXT0:
    Serial.println("Wakeup caused by external signal using RTC_IO");
    break;
  case ESP_SLEEP_WAKEUP_EXT1:
    Serial.println("Wakeup caused by external signal using RTC_CNTL");
    break;
  case ESP_SLEEP_WAKEUP_TIMER:
    Serial.println("Wakeup caused by timer");
    break;
  case ESP_SLEEP_WAKEUP_TOUCHPAD:
    Serial.println("Wakeup caused by touchpad");
    break;
  case ESP_SLEEP_WAKEUP_ULP:
    Serial.println("Wakeup caused by ULP program");
    break;
  default:
    Serial.printf("Wakeup was not caused by deep sleep: %d\n", wakeup_reason);
    break;
  }
}

const char AlphaSSLCA[] PROGMEM = R"EOF(
-----BEGIN CERTIFICATE-----
Put your server's cert here
-----END CERTIFICATE-----
)EOF";

void setup() {
  Serial.begin(115200);
  ++bootCount;
  Serial.println("Boot number: " + String(bootCount));
  //Print the wakeup reason for ESP32
  print_wakeup_reason();
  pinMode(FAN_CTL, OUTPUT);
  digitalWrite(FAN_CTL, HIGH);
  Serial.println("Fan set up complete...");
  Serial.println("");
  pinMode(PUMP_CTL, OUTPUT);
  digitalWrite(PUMP_CTL, LOW);
  Serial.println("Pump set up complete...");
  Serial.println("");
  pinMode(SOIL, INPUT);
  pinMode(SOIL_CTL, OUTPUT);
  digitalWrite(SOIL_CTL, LOW);
  Serial.println("Soil sensor set up complete...");
  Serial.println("");
  Wire.begin();
  delay(1000);
  if (airSensor.begin() == false)
  {
    Serial.println("Air sensor not detected. Please check wiring. Freezing...");
    while (1)
      ;
  }
  Serial.println("Air sensor detected. ");
  Serial.println("");
  Serial.println("Initializing Servo...");
  ESP32PWM::allocateTimer(0);
  ESP32PWM::allocateTimer(1);
  ESP32PWM::allocateTimer(2);
  ESP32PWM::allocateTimer(3);
  door_ctl.setPeriodHertz(50); // standard 50 hz servo
  door_ctl.attach(SERVO_PIN, 1000, 2000);
  door_ctl.write(0);
  delay(1000);
  door_ctl.write(90);
  delay(1000);
  door_ctl.write(180);
  delay(1000);
  door_ctl.write(90);
  delay(1000);
  door_ctl.write(0);
  Serial.println("Servo set up complete...");
  Serial.println("");
  Serial.println("Initializing WiFi...");
  WiFi.mode(WIFI_STA);
  Serial.print("Connecting to wifi");
  setup_wifi();

  while (WiFi.begin(SID, PASSWORD) != WL_CONNECTED)
  {
    Serial.print(".");
    delay(100);
  }
  Serial.println("");
  Serial.println("WiFi connected");
  Serial.println("");
  Serial.println("Setting up MQTT ...");
  mqttClient.setServer(MQTT_SERVER, MQTT_PORT);
  mqttClient.setCallback(incoming_MQTT);
  Serial.println("MQTT set up complete...");
  Serial.println("");
  timeSync(TZ_INFO, "pool.ntp.org", "time.nis.gov");
  influx.setWriteOptions(WriteOptions().writePrecision(WritePrecision::MS));
  influx.setWriteOptions(WriteOptions().batchSize(10).bufferSize(50));
 WiFiClientSecure *client = new WiFiClientSecure;
  if (client) {
    client->setCACert(AlphaSSLCA);
    // Check server connection
    if (influx.validateConnection()) {
      Serial.print("Connected to InfluxDB: ");
      Serial.println(influx.getServerUrl());
    } else {
      Serial.print("InfluxDB connection failed: ");
      Serial.println(influx.getLastErrorMessage());
      //  waitForInflux();
    }
  }
  myPoint.addTag("sensor", "GRN_CO2");
  myPoint.addTag("location", "YOUR_LOCATION");
  myPoint.addTag("Sensor_id", SENSOR_ID);
  Serial.println("Ready");
}

int co2 = 0;
void loop() {

  if (!mqttClient.connected()) {
    reconnect();
  }
  mqttClient.loop();
  unsigned long currentMillis = millis();
  if (currentMillis - lastCO2Millis >= readingInterval) {
    lastCO2Millis = currentMillis;
    myPoint.clearFields();
    if (influx.isBufferFull()) {
      influx.flushBuffer();
    } if (airSensor.dataAvailable()) {
      co2 = airSensor.getCO2();
      float temp_c = airSensor.getTemperature();
      float hum = airSensor.getHumidity();
      int rssi = WiFi.RSSI();
      float temp_f = temp_c * 9.0 / 5.0 + 32.0;
      myPoint.addField("co2", co2);
      myPoint.addField("RSSI", rssi);
      // myPoint.addField("battery", power);
      myPoint.addField("temp_c", temp_c);
      myPoint.addField("humidity", hum);
      myPoint.addField("temp_f", temp_f);
    }
    digitalWrite(SOIL_CTL, HIGH);
    delay(10);
    int soil = analogRead(SOIL);
    digitalWrite(SOIL_CTL, LOW);
    myPoint.addField("soil", soil);
    influx.writePoint(myPoint);
  }
}

void incoming_MQTT(char *topic, byte *payload, unsigned int length) {
  Serial.print("Message arrived [");
  Serial.print(topic);
  Serial.print("] ");
  for (int i = 0; i < length; i++) {
    Serial.print((char)payload[i]);
  }
  Serial.println();
  StaticJsonDocument<200> doc;
  DeserializationError error = deserializeJson(doc, payload);
  if (error) {
    Serial.print(F("deserializeJson() failed: "));
    Serial.println(error.f_str());
    return;
  }
  const char *fan = doc["commands"]["fan"];
  const char *vent = doc["commands"]["vent"];
  const char *pump = doc["commands"]["pump"];
  Serial.printf("Fan: %s Vent: %s Pump: %s\n", fan, vent, pump);
  if (fan) {
    if (strcmp(fan, "on") == 0) {
      Serial.println("Fan on");
      digitalWrite(FAN_CTL, LOW);
    } else if (strcmp(fan, "off") == 0) {
      Serial.println("Fan off");
      digitalWrite(FAN_CTL, HIGH);
    }
  }
  if(pump) {
    if (strcmp(pump, "on") == 0) {
      Serial.println("Pump on");
      digitalWrite(PUMP_CTL, HIGH);
    } else if (strcmp(pump, "off") == 0) {
      Serial.println("Pump off");
      digitalWrite(PUMP_CTL, LOW);
    }
  }
  if(vent) {
    if (strcmp(vent, "open") == 0){
      Serial.println("Vent open");
      door_ctl.write(180);
      pos = 180;
    } else if (strcmp(vent, "close") == 0) {
      Serial.println("Vent close");
      door_ctl.write(0);
      pos = 0;
    } else if (strcmp(vent, "half") == 0) {
      Serial.println("Vent Half");
      if(pos == 0 || pos == 180) {
        door_ctl.write(90);
        pos = 90;
      } else if (pos == 90) {
        // already half
      }
    }
  }
}

void setup_wifi() {
  char mySSID[14];
  findSSID();
  // Start by connecting to the WiFi network
  Serial.println();
  Serial.print("Connecting to ");
  Serial.println(mySSID);
  WiFi.macAddress(mac);
  Serial.print("MAC: ");
  Serial.print(mac[5], HEX);
  Serial.print(":");
  Serial.print(mac[4], HEX);
  Serial.print(":");
  Serial.print(mac[3], HEX);
  Serial.print(":");
  Serial.print(mac[2], HEX);
  Serial.print(":");
  Serial.print(mac[1], HEX);
  Serial.print(":");
  Serial.println(mac[0], HEX);
  while (int v = wait_for_wifi() > 0)
  {
    findSSID();
  }

  Serial.print("IP address: ");
  Serial.println(WiFi.localIP());
}

void findSSID()
{
  boolean foundSSID = false;

  while (!foundSSID)
  {
    Serial.print("Searching for WiFi Network: ");
    Serial.println(SID);
    int numberOfNetworks = WiFi.scanNetworks();
    for (int i = 0; i < numberOfNetworks; i++)
    {
      Serial.print("Network name: ");
      Serial.println(WiFi.SSID(i));
      Serial.print("Signal strength: ");
      Serial.println(WiFi.RSSI(i));
      if (WiFi.SSID(i) == SID)
      {
        foundSSID = true;
        Serial.print("Found ");
        Serial.println(WiFi.SSID(i));
        WiFi.begin(SID, PASSWORD);
        return;
      }
      Serial.println("-----------------------");
    }
  }
}

int wait_for_wifi()
{
  int tries = 30;
  int thisTry = 0;
  Serial.println("waiting for Wifi");

  while (WiFi.status() != WL_CONNECTED && thisTry < tries)
  {
    delay(1000);
    thisTry += 1;
  }
  if (WiFi.status() != WL_CONNECTED)
  {
    return 1;
  }
  Serial.println("");
  Serial.println("WiFi connected");
  return 0;
}

void reconnect()
{
  // Loop until we're reconnected
  while (!mqttClient.connected())
  {
    Serial.print("Attempting MQTT connection...");
    // Create a random client ID
    String clientId = "GRNHSE";
    clientId += String(random(0xffff), HEX);
    // Attempt to connect
    if (mqttClient.connect(clientId.c_str()))
    {
      Serial.println("connected");
      // ... and resubscribe
      mqttClient.subscribe("greenhouse");
    }
    else
    {
      Serial.print("failed, rc=");
      Serial.print(mqttClient.state());
      Serial.println(" try again in 5 seconds");
      // Wait 5 seconds before retrying
      delay(5000);
    }
  }
}
