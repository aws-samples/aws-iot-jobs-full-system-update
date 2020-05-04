package awsiotjobs

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const jobBaseTopic = "$aws/things/%s/jobs/%s"
const publishTimeout = 2 * time.Second

// Config is the configuration to connect to AWS IoT
type Config struct {
	Port            int
	CaCertPath      string
	CertificatePath string
	PrivateKeyPath  string
	Endpoint        string
	ThingName       string
	ClientID        string
	Handler         func(je JobExecutioner)
}

// FromFile reads the configuration from a JSON file
// {
// 	"Port":           88,
// 	"CaCertPath":     "ca",
// 	"CertificatePath":"cert",
// 	"PrivateKeyPath": "key",
// 	"Endpoint":       "ep",
// 	"ThingName":      "tn",
// 	"ClientID":       "cid"
// }
func (c *Config) FromFile(file string) error {
	s, err := ioutil.ReadFile(file)
	if err != nil {
		fmt.Printf("Invalid config file - ignoring\n")
		return err
	}
	json.Unmarshal(s, &c)
	return nil
}

type nextJobPayload struct {
	clientToken string
}

// NewTLSConfig creates a new TLS config
func NewTLSConfig(caCertPath, certPath, privKeyPath string) *tls.Config {
	certpool := x509.NewCertPool()
	caCert, err := ioutil.ReadFile(caCertPath)
	if err == nil {
		certpool.AppendCertsFromPEM(caCert)
	} else {
		panic(err)
	}

	cert, err := tls.LoadX509KeyPair(certPath, privKeyPath)
	if err != nil {
		panic(err)
	}

	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])

	if err != nil {
		panic(err)
	}

	return &tls.Config{
		RootCAs:            certpool,
		ClientAuth:         tls.NoClientCert,
		ClientCAs:          nil,
		InsecureSkipVerify: false,
		Certificates:       []tls.Certificate{cert},
	}
}

// JobError contains the error code and message for a Job error
type JobError struct {
	ErrCode    string
	ErrMessage string
}

func (err JobError) Error() string {
	return fmt.Sprintf("code %s, msg: %s", err.ErrCode, err.ErrMessage)
}

// JobDocument represent the Job Document passed by AWS IoT Jobs.
// We use a generic map[string]interface{} since the documents are JSON based
type JobDocument map[string]interface{}

// StatusDetails represents the Status Details passed by AWS IoT Jobs
// We use a generic map[string]interface{} since the documents are JSON based
type StatusDetails map[string]interface{}

// JobExecutioner is an interface allowig the concrete Job handler to interact with the
// JobExecution logic
type JobExecutioner interface {
	GetJobDocument() JobDocument
	GetStatusDetails() StatusDetails
	Publish(string, byte, interface{})
	Success(StatusDetails) error
	Fail(JobError) error
	Reject(JobError) error
	InProgress(StatusDetails) error
	Terminate()
	GetThingName() string
	GetJobID() string
}

// JobExecution represents the AWS IoT job execution document
// JOB MESSAGE SAMPLE
// {
// 	"timestamp":1573561673,
// 	"execution":{
// 		"jobId":"mender_install-7cf96d",
// 		"status":"IN_PROGRESS",
// 		"queuedAt":1573560519,
// 		"startedAt":1573560656,
// 		"lastUpdatedAt":1573560656,
// 		"versionNumber":2,
// 		"executionNumber":1,
// 		"jobDocument": {
// 			"operation":"mender_install",
// 			"url":"https://fwupdate-demo"
// 		}
// 	}
// }
type JobExecution struct {
	JobID           string        `json:"jobId"`
	ThingName       string        `json:"thingName"`
	JobDocument     JobDocument   `json:"jobDocument"`
	Status          string        `json:"status"`
	StatusDetails   StatusDetails `json:"statusDetails"`
	QueuedAt        int64         `json:"queuedAt"`
	StartedAt       int64         `json:"startedAt"`
	LastUpdatedAt   int64         `json:"lastUpdatedAt"`
	VersionNumber   int64         `json:"versionNumber"`
	ExecutionNumber int64         `json:"executionNumber"`
	client          *Client
	mux             sync.Mutex
}

// GetJobDocument is the accessor to the JobDocument
func (je *JobExecution) GetJobDocument() JobDocument {
	return je.JobDocument
}

// GetStatusDetails is the accessor to the StatusDetails
func (je *JobExecution) GetStatusDetails() StatusDetails {
	return je.StatusDetails
}

// GetThingName is the accessor to the ThingName
func (je *JobExecution) GetThingName() string {
	return je.client.config.ThingName
}

// GetJobID is the accessor to JobID
func (je *JobExecution) GetJobID() string {
	return je.JobID
}

func (je *JobExecution) getUpdatePayload() interface{} {
	payload := make(map[string]interface{})
	payload["status"] = je.Status
	payload["statusDetails"] = je.StatusDetails
	payload["expectedVersion"] = je.VersionNumber
	payload["executionNumber"] = je.ExecutionNumber
	payload["includeJobExecutionState"] = true
	payload["clientToken"] = "client-token"
	jsonPayload, _ := json.Marshal(payload)
	return jsonPayload
}

func (je *JobExecution) sendUpdate() error {
	if je.client.Iot == nil {
		log.Panic("Iot client not set")
	}
	payload := je.getUpdatePayload()
	topic := fmt.Sprintf("%s/update", fmt.Sprintf(jobBaseTopic, je.client.config.ThingName, je.JobID))
	log.Printf("Updating status with %s\non topic %s\n", string(payload.([]byte)), topic)
	token := je.client.Iot.Publish(topic, 1, false, payload) // Send syncronously
	if token.WaitTimeout(publishTimeout) && token.Error() != nil {
		return token.Error()
	}
	return nil
}

/*
InProgress reports the execution in progress to AWS IoT Device Management
The argument which is passed to the function is reported in the StatusDetails field of the job.
You can use InProgress in case the execution of your job will take some time or needs multiple steps and
you need to be able to recover from an interruption.
The next time you access the Jobs API, you'll get the pending job execution and the correspondin state.
*/
func (je *JobExecution) InProgress(statusDetails StatusDetails) error {
	log.Printf("JOB IN_PROGRESS: %v\n", statusDetails)
	je.mux.Lock()
	je.StatusDetails = statusDetails
	je.Status = "IN_PROGRESS"
	je.mux.Unlock()
	return je.sendUpdate()
}

/*
Success reports a successfull job execution to AWS IoT Device Management
By passing a StatusDetails structure to the function you can store some additional information regarding
the execution.
This function should be called to notify Device Management that the job was successfully performed.
If there are other jobs pending, they will be immediately notified to the client.
*/
func (je *JobExecution) Success(statusDetails StatusDetails) error {
	log.Printf("JOB SUCCEEDED: %v\n", statusDetails)
	je.mux.Lock()
	je.StatusDetails = statusDetails
	je.Status = "SUCCEEDED"
	je.mux.Unlock()
	err := je.sendUpdate()
	if err != nil {
		return err
	}
	je.unsubscribeFromUpdates()
	return nil
}

/*
Success reports a fialed job execution to AWS IoT Device Management
By passing a StatusDetails structure to the function you can store some additional information regarding
the reason of the failure.
This function should be called to notify Device Management that the job failed.
If there are other jobs pending, they will be immediately notified to the client.
*/
func (je *JobExecution) Fail(err JobError) error {
	log.Printf("JOB FAIL: %v\n", err)
	je.mux.Lock()
	je.StatusDetails = map[string]interface{}{
		"error": err.Error(),
	}
	je.Status = "FAILED"
	je.mux.Unlock()
	e := je.sendUpdate()
	if e != nil {
		return err
	}
	je.unsubscribeFromUpdates()
	return nil
}

/*
Reject reports that the client could not handle the job execution, for example because the document was not understood or missing
compulsory information.
By passing a StatusDetails structure to the function you can store some additional information regarding
the reason of the rejection.
If there are other jobs pending, they will be immediately notified to the client.
*/
func (je *JobExecution) Reject(err JobError) error {
	log.Printf("JOB REJECTED: %v\n", err)
	je.mux.Lock()
	je.StatusDetails = map[string]interface{}{
		"error": err.Error(),
	}
	je.Status = "REJECTED"
	je.mux.Unlock()
	e := je.sendUpdate()
	if e != nil {
		return err
	}
	je.unsubscribeFromUpdates()
	return nil
}

// Terminate the job execution if the process has to stop
func (je *JobExecution) Terminate() {
	je.client.unsubscribe()
}

// Publish is a wrapper on the mqtt Publish
func (je *JobExecution) Publish(topic string, qos byte, payload interface{}) {
	je.client.Iot.Publish(topic, qos, false, payload)
}

// Internal types used to decode the updates
type executionStateType struct {
	Status        string        `json:"status"`
	StatusDetails StatusDetails `json:"statusDetails"`
	VersionNumber int64         `json:"versionNumber"`
}

type updatePayload struct {
	ExecutionState executionStateType `json:"executionState"`
}

func (je *JobExecution) updateHandler(client mqtt.Client, msg mqtt.Message) {
	payload := updatePayload{}
	json.Unmarshal(msg.Payload(), &payload)
	log.Printf("%v\n", payload)
	je.mux.Lock()
	je.VersionNumber = payload.ExecutionState.VersionNumber
	je.StatusDetails = payload.ExecutionState.StatusDetails
	je.mux.Unlock()
}

func (je *JobExecution) subscribeToUpdates() {
	updateTopic := fmt.Sprintf(jobBaseTopic, je.client.config.ThingName, "+/update/accepted")
	je.client.Iot.Subscribe(updateTopic, 0, je.updateHandler)
}

func (je *JobExecution) unsubscribeFromUpdates() {
	updateTopic := fmt.Sprintf(jobBaseTopic, je.client.config.ThingName, "+/update/accepted")
	je.client.Iot.Unsubscribe(updateTopic)
}

var defaultHandler mqtt.MessageHandler = func(client mqtt.Client, msg mqtt.Message) {
	log.Printf("Topic: %s\n", msg.Topic())
	log.Printf("Msg: %s\n", msg.Payload())
}

func parseJobMessage(msg []byte) (*JobExecution, error) {
	var jobExecution JobExecution
	var doc map[string]interface{}
	json.Unmarshal(msg, &doc)
	execution, ok := doc["execution"]
	if !ok {
		return &jobExecution, JobError{"ERR_INVALID_JOB", fmt.Sprintf("missing \"execution\" from payload: %s", msg)}
	}
	executionJSON, _ := json.Marshal(execution)
	json.Unmarshal(executionJSON, &jobExecution)
	return &jobExecution, nil
}

func (client *Client) jobHandler(mqttClient mqtt.Client, msg mqtt.Message) {
	job, err := parseJobMessage(msg.Payload())
	if err != nil {
		fmt.Printf("Not a job - Ignoring, %s\n", err.Error())
		return
	}
	job.client = client
	job.ThingName = client.config.ThingName // This is so the specialized jobs can access the property
	job.subscribeToUpdates()
	go job.client.config.Handler(job)
}

func (client *Client) subscribe() {
	thingName := client.config.ThingName
	client.Iot.Subscribe(fmt.Sprintf(jobBaseTopic, thingName, "notify-next"), 0, client.jobHandler)
	client.Iot.Subscribe(fmt.Sprintf(jobBaseTopic, thingName, "+/get/accepted"), 0, client.jobHandler)
	client.Iot.Subscribe(fmt.Sprintf(jobBaseTopic, thingName, "+/get/rejected"), 0, defaultHandler)
	client.Iot.Subscribe(fmt.Sprintf(jobBaseTopic, thingName, "start-next/accepted"), 0, client.jobHandler)
	client.Iot.Subscribe(fmt.Sprintf(jobBaseTopic, thingName, "start-next/rejected"), 0, defaultHandler)
}

func (client *Client) unsubscribe() {
	thingName := client.config.ThingName
	client.Iot.Unsubscribe(fmt.Sprintf(jobBaseTopic, thingName, "notify-next"))
	client.Iot.Unsubscribe(fmt.Sprintf(jobBaseTopic, thingName, "+/get/accepted"))
	client.Iot.Unsubscribe(fmt.Sprintf(jobBaseTopic, thingName, "+/get/rejected"))
	client.Iot.Unsubscribe(fmt.Sprintf(jobBaseTopic, thingName, "start-next/accepted"))
	client.Iot.Unsubscribe(fmt.Sprintf(jobBaseTopic, thingName, "start-next/rejected"))
}

// NewConfig return a new config object with the default paramters
func NewConfig() Config {
	return Config{}
}

// IMqttClient represents the Mqtt client interface used by this library, allows also for better testability
type IMqttClient interface {
	Publish(string, byte, bool, interface{}) mqtt.Token
	Subscribe(string, byte, mqtt.MessageHandler) mqtt.Token
	Unsubscribe(...string) mqtt.Token
	Connect() mqtt.Token
}

// Client defines the client for connecting to AWSIoTJobs.
type Client struct {
	Iot    IMqttClient //mqtt.Client
	config Config
}

func (client *Client) init(c Config) {
	client.config = c
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("ssl://%s:%d", c.Endpoint, c.Port))
	opts.SetClientID(c.ClientID).SetTLSConfig(NewTLSConfig(c.CaCertPath, c.CertificatePath, c.PrivateKeyPath))
	opts.SetDefaultPublishHandler(defaultHandler)
	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Minute)
	client.Iot = mqtt.NewClient(opts)
}

// NewClient returns a new AWSIoTJobsClient using the configuration
func NewClient(config Config) Client {
	client := Client{}
	client.init(config)
	return client
}

// ConnectAndSubscribe connects to AWS IoT Core and subscribed to the job topics
func (client *Client) ConnectAndSubscribe() {
	fmt.Println("ConnectAndSubscribe - Connecting")
	if token := client.Iot.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}
	client.subscribe()
	fmt.Println("ConnectAndSubscribe - Checking for jobs")
	client.Iot.Publish(fmt.Sprintf(jobBaseTopic, client.config.ThingName, "start-next"), 1, false, "")
	log.Println("ConnectAndSubscribe - Done")
}
