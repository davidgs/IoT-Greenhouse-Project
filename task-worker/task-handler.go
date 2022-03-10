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

package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	camundaclientgo "github.com/citilinkru/camunda-client-go/v2"
	"github.com/citilinkru/camunda-client-go/v2/processor"
	mqtt "github.com/eclipse/paho.mqtt.golang"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	log "github.com/sirupsen/logrus"
)

const (
	// MQTT server URL.
	MQTT_URL = "tcp://YOUR_MQTT_SERVER:1883"
	// INFLUXDB server URL.
	INFLUX_URL = "https://YOUR_INFLUX_SERVER:8086"
	// INFLUX_DB_TOKEN.
	INFLUX_DB_TOKEN = "YOUR_INFLUX_DB_TOKEN"
	// INFLUX_DB_NAME.
	INFLUX_DB_NAME = "YOUR_INFLUX_DB_NAME"
	/** !Important!
	 * Make sure to update the 'bucket' in each query!
	**/
	// Influx Temp Query
	INFLUX_TEMP_QUERY = `from(bucket: "telegraf")
  |> range(start: -1m)
  |> filter(fn: (r) => r["_measurement"] == "greenhouse")
  |> filter(fn: (r) => r["_field"] == "temp_c" )
  `
	// Influx CO2 Query
	INFLUX_CO2_QUERY = `from(bucket: "telegraf")
  |> range(start: -1m)
  |> filter(fn: (r) => r["_measurement"] == "greenhouse")
  |> filter(fn: (r) => r["_field"] == "co2" )`
	// Influx Humidity Query
	INFLUX_HUMIDITY_QUERY = `from(bucket: "telegraf")
  |> range(start: -1m)
  |> filter(fn: (r) => r["_measurement"] == "greenhouse")
  |> filter(fn: (r) => r["_field"] == "humidity" )`
	// Influx Soil Query
	INFLUX_SOIL_QUERY = `from(bucket: "telegraf")
  |> range(start: -1m)
  |> filter(fn: (r) => r["_measurement"] == "greenhouse")
  |> filter(fn: (r) => r["_field"] == "soil" )`
	// Camunda server URL.
	CAMUNDA_SERVER = "https://YOUR_CAMUNDA_SERVER:8080"
	// Camunda API User ID.
	CAMUNDA_API_USER = "demo"
	// Camunda API password.
	CAMUNDA_API_PASS = "demo"
	// MQTT password.
	// MQTT topic.
	MQTT_TOPIC = "davidgs/test"

)

type GrowHouse struct {
	Name string
	Humidity float64
	Soil float64
	Temp float64
	CO2 float64
	DoorOpen bool
	FanOn bool
	PumpOn bool
}

var Crop = GrowHouse{
	Name: "GrowHouse",
	Humidity: 0.00,
	Soil: 0.00,
	Temp: 0.00,
	CO2: 0.00,
	DoorOpen: false,
	FanOn: false,
	PumpOn: false,
}

type ControlMsg struct {
	Sensor   string `json:"sensor"`
	Commands struct {
		Fan  string `json:"fan"`
		Vent string `json:"vent"`
		Pump string `json:"pump"`
	} `json:"commands"`
}

func main() {
	logger := func(err error) {
		log.Error(err)
	}


	client := camundaclientgo.NewClient(camundaclientgo.ClientOptions{
		UserAgent:   "",
		EndpointUrl: CAMUNDA_SERVER + "/engine-rest",
		Timeout: time.Second * 10,
		ApiUser: CAMUNDA_API_USER,
		ApiPassword: CAMUNDA_API_PASS,
	},
	)
	asyncResponseTimeout := 5000
	// get a process instance to work with

	proc := processor.NewProcessor(client, &processor.ProcessorOptions{
		WorkerId:                  "GreenHouseHandler",
		LockDuration:              time.Second * 20,
		MaxTasks:                  10,
		MaxParallelTaskPerHandler: 100,
		LongPollingTimeout:        25 * time.Second,
		AsyncResponseTimeout:      &asyncResponseTimeout,
	}, logger)
	log.Debug("Processor started ... ")
	// add a handler for checking the existing Queue
	proc.AddHandler(
		&[]camundaclientgo.QueryFetchAndLockTopic{
			{TopicName: "checkCO2"},
		},
		func(ctx *processor.Context) error {
			return checkCO2(ctx.Task.Variables, ctx)
		},
	)
	proc.AddHandler(
		&[]camundaclientgo.QueryFetchAndLockTopic{
			{TopicName: "checkTemp"},
		},
		func(ctx *processor.Context) error {
			return checkTemp(ctx.Task.Variables, ctx)
		},
	)
	proc.AddHandler(
		&[]camundaclientgo.QueryFetchAndLockTopic{
			{TopicName: "checkSoil"},
		},
		func(ctx *processor.Context) error {
			return checkSoil(ctx.Task.Variables, ctx)
		},
	)
	proc.AddHandler(
		&[]camundaclientgo.QueryFetchAndLockTopic{
			{TopicName: "checkHumidity"},
		},
		func(ctx *processor.Context) error {
			return checkHumidity(ctx.Task.Variables, ctx)
		},
	)
	proc.AddHandler(
		&[]camundaclientgo.QueryFetchAndLockTopic{
			{TopicName: "control"},
		},
		func(ctx *processor.Context) error {
			return control(ctx.Task.Variables, ctx)
		},
	)
	log.Debug("checkQueue Handler started ... ")
	http.HandleFunc("/sentiment", webHandler)
	http.Handle("/", http.FileServer(http.Dir("./static")))
	err := http.ListenAndServe(":9999", nil)
	if err != nil {
		log.Fatal(err)
	}
}

func webHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello, %s!", r.URL.Path[1:])
}

func checkCO2(variables map[string]camundaclientgo.Variable, ctx *processor.Context) error {
	// Create a new client using an InfluxDB server base URL and an authentication token
	client := influxdb2.NewClient(INFLUX_URL, INFLUX_DB_TOKEN)
	// Get query client
	queryAPI := client.QueryAPI(INFLUX_DB_NAME)
	// get QueryTableResult
	result, err := queryAPI.Query(context.Background(), INFLUX_CO2_QUERY )
	var averageCO2 float64 = 0.00
	var numResults int = 0
	if err == nil {
		// Iterate over query response
		for result.Next() {
			// Access data
			foo := fmt.Sprintf("%v", result.Record().Value())
			foo64, err := strconv.ParseFloat(foo, 64)
			if err != nil {
				fmt.Println("Query failed to parse float64")
			}
			averageCO2 = averageCO2 + foo64
			numResults++
		}
		// check for an error
		if result.Err() != nil {
			fmt.Printf("query parsing error: %s\n", result.Err().Error())
		}
		averageCO2 = (averageCO2 / float64(numResults))
		if math.IsNaN(averageCO2) {
			fmt.Println("averageCO2 is NaN")
			averageCO2 = 0.00
		}
		fmt.Printf("averageCO2: %f\n", averageCO2)

		varb := ctx.Task.Variables
		varb["co2"] = camundaclientgo.Variable{Value: averageCO2, Type: "double"}
		err := ctx.Complete(processor.QueryComplete{Variables: &varb})
		if err != nil {
			log.Error("queuStatus: ", err)
			return err
		}
	} else {
		log.Error("queuStatus: ", err)
		return err
		//panic(err)
	}
	Crop.CO2 = averageCO2
	// Ensures background processes finishes
	client.Close()
	log.Println("co2Status")
	return nil
}

func checkTemp(variables map[string]camundaclientgo.Variable, ctx *processor.Context) error {
	// Create a new client using an InfluxDB server base URL and an authentication token
	client := influxdb2.NewClient(INFLUX_URL, INFLUX_DB_TOKEN)
	// Get query client
	queryAPI := client.QueryAPI(INFLUX_DB_NAME)
	// get QueryTableResult
	result, err := queryAPI.Query(context.Background(), INFLUX_TEMP_QUERY )
	var averageTemp float64 = 0.00
	var numResults int = 0
	if err == nil {
		// Iterate over query response
		for result.Next() {
			// Access data
			foo := fmt.Sprintf("%v", result.Record().Value())
			foo64, err := strconv.ParseFloat(foo, 64)
			if err != nil {
				fmt.Println("Query failed to parse float64")
			}
			averageTemp = averageTemp + foo64
			numResults++
		}
		// check for an error
		if result.Err() != nil {
			fmt.Printf("query parsing error: %s\n", result.Err().Error())

		}
		averageTemp = (averageTemp / float64(numResults))
		if math.IsNaN(averageTemp) {
			averageTemp = 0.00
		}
		fmt.Printf("averageTemp: %f\n", averageTemp)

		varb := ctx.Task.Variables
		varb["temp"] = camundaclientgo.Variable{Value: averageTemp, Type: "double"}
		err := ctx.Complete(processor.QueryComplete{Variables: &varb})
		if err != nil {
			log.Error("queuStatus: ", err)
			return err
		}
	} else {
		log.Error("checkTemp: ", err)
		return err
	}
	Crop.Temp = averageTemp
	// Ensures background processes finishes
	client.Close()
	log.Println("tempStatus")
	return nil
}

func checkSoil(variables map[string]camundaclientgo.Variable, ctx *processor.Context) error {
	// Create a new client using an InfluxDB server base URL and an authentication token
	client := influxdb2.NewClient(INFLUX_URL, INFLUX_DB_TOKEN)
	// Get query client
	queryAPI := client.QueryAPI(INFLUX_DB_NAME)
	// get QueryTableResult
	result, err := queryAPI.Query(context.Background(), INFLUX_SOIL_QUERY )
	// or r["_field"] == "humidity" or r["_field"] == "soil" or r["_field"] == "temp_c")`)
	var avSoil float64 = 0.00
	var numResults int = 0
	if err == nil {
		// Iterate over query response
		for result.Next() {
			// Access data
			foo := fmt.Sprintf("%v", result.Record().Value())
			foo64, err := strconv.ParseFloat(foo, 64)
			if err != nil {
				fmt.Println("Query failed to parse float64")
			}
			avSoil = avSoil + foo64
			numResults++
		}
		// check for an error
		if result.Err() != nil {
			fmt.Printf("query parsing error: %s\n", result.Err().Error())
		}
		avSoil = (avSoil / float64(numResults))
		if math.IsNaN(avSoil) {
			avSoil = 0.00
		}
		fmt.Printf("average Soil: %f\n", avSoil)

		varb := ctx.Task.Variables
		varb["soil"] = camundaclientgo.Variable{Value: avSoil, Type: "double"}
		err := ctx.Complete(processor.QueryComplete{Variables: &varb})
		if err != nil {
			log.Error("queuStatus: ", err)
			return err
		}
	} else {
		return err
		//panic(err)
	}
	Crop.Soil = avSoil
	// Ensures background processes finishes
	client.Close()
	log.Println("soil complete")
	return nil
}

func checkHumidity(variables map[string]camundaclientgo.Variable, ctx *processor.Context) error {
	// Create a new client using an InfluxDB server base URL and an authentication token
	client := influxdb2.NewClient(INFLUX_URL, INFLUX_DB_TOKEN)
	// Get query client
	queryAPI := client.QueryAPI(INFLUX_DB_NAME)
	// get QueryTableResult
	result, err := queryAPI.Query(context.Background(), INFLUX_HUMIDITY_QUERY )
	var avHum float64 = 0.00
	var numResults int = 0
	if err == nil {
		// Iterate over query response
		for result.Next() {
						// Access data
			foo := fmt.Sprintf("%v", result.Record().Value())
			foo64, err := strconv.ParseFloat(foo, 64)
			if err != nil {
				fmt.Println("Query failed to parse float64")
			}
			avHum = avHum + foo64
			numResults++
		}
		// check for an error
		if result.Err() != nil {
			fmt.Printf("query parsing error: %s\n", result.Err().Error())
		}
		avHum = (avHum / float64(numResults))
		if math.IsNaN(avHum) {
			avHum = 0.00
		}
		fmt.Printf("avHum: %f\n", avHum)

		varb := ctx.Task.Variables
		varb["humidity"] = camundaclientgo.Variable{Value: avHum, Type: "double"}
		err := ctx.Complete(processor.QueryComplete{Variables: &varb})
		if err != nil {
			log.Error("queuStatus: ", err)
			return err
		}
	} else {
		return err
		//panic(err)
	}
	Crop.Humidity = avHum
	// Ensures background processes finishes
	client.Close()
	log.Println("humidity check complete")
	return nil
}

func controlPump(variables map[string]camundaclientgo.Variable,ctx *processor.Context, ot chan error) {
	var tlsConf *tls.Config = nil
	tlsConf = &tls.Config{
		InsecureSkipVerify: true,
	}
	var opts = mqtt.ClientOptions{
		ClientID: "greenhouse",
		Username: "",
		Password: "",
		TLSConfig:            tlsConf,
		KeepAlive:            0,
		PingTimeout:          0,
		ConnectTimeout:       time.Second * 10,
		MaxReconnectInterval: 0,
		AutoReconnect:        false,
		ConnectRetryInterval: 0,
		ConnectRetry:         false,
		Store:                nil,

	}

	opts.AddBroker(MQTT_URL)
	opts.SetClientID("greenhouse")
	opts.SetMaxReconnectInterval(time.Second * 10)
	var client = mqtt.NewClient(&opts)
	token := client.Connect()
	for !token.WaitTimeout(3 * time.Second) {
	}
	if err := token.Error(); err != nil {
		log.Fatal(err)
	}
	varb := ctx.Task.Variables["soil"].Value
	s := fmt.Sprintf("%v", varb)
	foo64, err := strconv.ParseFloat(s, 64)
			if err != nil {
				fmt.Println("Query failed to parse float64")
			}
	fmt.Println(foo64)
	msg := ""
	if foo64 <= 1500 {
		log.Println("pump on")
		msg = "pump on complete"
		t := client.Publish("greenhouse", 0, false, "pump-on")
		go func() {
    	_ = t.Wait() // Can also use '<-t.Done()' in releases > 1.2.0
    	if t.Error() != nil {
        log.Error(t.Error()) // Use your preferred logging technique (or just fmt.Printf)
    	}
		}()
	} else {
		log.Println("pump off")
		msg = "pump off complete"
		if token := client.Publish("greenhouse", 0, false, "pump-off"); token.Wait() && token.Error() != nil {
			ot <- token.Error()
		}
	}
	varbs := ctx.Task.Variables
		varbs["pump"] = camundaclientgo.Variable{Value: msg, Type: "string"}
		err = ctx.Complete(processor.QueryComplete{Variables: &varbs})
		if err != nil {
			log.Error("queuStatus: ", err)
			ot <- err
		}
	client.Disconnect(250)
	ot <- nil
}

func control(variables map[string]camundaclientgo.Variable,ctx *processor.Context) error {
	var tlsConf *tls.Config = nil
	tlsConf = &tls.Config{
		InsecureSkipVerify: true,
	}
	var opts = mqtt.ClientOptions{
		ClientID: "greenhouse",
		Username: "",
		Password: "",
		TLSConfig:            tlsConf,
		KeepAlive:            0,
		PingTimeout:          0,
		ConnectTimeout:       time.Second * 10,
		MaxReconnectInterval: 0,
		AutoReconnect:        false,
		ConnectRetryInterval: 0,
		ConnectRetry:         false,
		Store:                nil,

	}
	incoming := ControlMsg{}
	opts.AddBroker(MQTT_URL)
	opts.SetClientID("greenhouse")
	opts.SetMaxReconnectInterval(time.Second * 10)
	var client = mqtt.NewClient(&opts)
	token := client.Connect()
	for !token.WaitTimeout(3 * time.Second) {
	}
	if err := token.Error(); err != nil {
		return err
	}
	varb := fmt.Sprintf("%v", ctx.Task.Variables["action"].Value)
	fmt.Printf("Raw: %v\n", varb)
	err := json.Unmarshal([]byte(varb), &incoming)
	if err != nil {
		return err
	}
	fmt.Println("Incoming Sensor: ", incoming.Sensor)
	fmt.Printf("Incoming Commands: %v\n", incoming.Commands)
		t := client.Publish("greenhouse", 0, false, varb)
		go func() {
    	_ = t.Wait() // Can also use '<-t.Done()' in releases > 1.2.0
    	if t.Error() != nil {
        log.Error(t.Error()) // Use your preferred logging technique (or just fmt.Printf)
    	}
		}()
	varbs := ctx.Task.Variables
		varbs[incoming.Sensor] = camundaclientgo.Variable{Value: incoming.Sensor + " completed", Type: "string"}
		err = ctx.Complete(processor.QueryComplete{Variables: &varbs})
		if err != nil {
			log.Error("queuStatus: ", err)
			return err
		}
	client.Disconnect(250)
	return nil
}