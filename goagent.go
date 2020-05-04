package main

import (
	"flag"
	"fmt"
	"sync"

	"./awsiotjobs"
	"./awsiotjobs/mender"
)

func main() {
	c := awsiotjobs.NewConfig()
	configFile := ""
	flag.IntVar(&c.Port, "port", 8883, "the port to use to connect")
	flag.StringVar(&c.CaCertPath, "cacert", "rootCA.pem", "the CA cert path")
	flag.StringVar(&c.CertificatePath, "cert", "cert.pem", "the device certificate path")
	flag.StringVar(&c.PrivateKeyPath, "key", "private.key", "the private key path")
	flag.StringVar(&c.Endpoint, "endpoint", "", "the endpoint path")
	flag.StringVar(&c.ThingName, "thingName", "", "the thing name")
	flag.StringVar(&c.ClientID, "clientId", "", "the client Id for the MQTT connection")
	flag.StringVar(&configFile, "config", "/etc/goagent/goagent.conf", "the configuration file. Inline properties will override config file settings")
	flag.Parse()

	if len(configFile) > 0 {
		c.FromFile(configFile)
		flag.Parse() // We execute this to override the settings read from the config file
	}
	c.Handler = mender.Process
	awsJobsClient := awsiotjobs.NewClient(c)
	fmt.Println("MenderAgent started")
	awsJobsClient.ConnectAndSubscribe()

	var wg sync.WaitGroup
	wg.Add(1)
	wg.Wait()

}
