package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"

	httpbinclient "github.com/mccutchen/go-httpbin/v2/httpbin/client"
	"github.com/openziti/sdk-golang/ziti"
	"github.com/openziti/sdk-golang/ziti/config"
)

var (
	address string

	identityJson      string
	identityJsonFile  string
	identityJsonBytes []byte
	serviceName       string
	enableZiti        bool
	headers           map[string][]string
	queries           map[string][]string
)

func main() {
	flag.StringVar(&address, "address", "", "Address to query")

	headers = make(map[string][]string)
	queries = make(map[string][]string)

	flag.Func("header", "Add a header to the request (key=val)", func(f string) error {
		data := strings.Split(f, "=")
		if _, ok := headers[data[0]]; !ok {
			headers[data[0]] = make([]string, 0)
		}
		headers[data[0]] = append(headers[data[0]], data[1])
		return nil
	})

	flag.Func("query", "Add a query string to the request (key=val)", func(f string) error {
		data := strings.Split(f, "=")
		if _, ok := queries[data[0]]; !ok {
			queries[data[0]] = make([]string, 0)
		}
		queries[data[0]] = append(queries[data[0]], data[1])
		return nil
	})

	flag.BoolVar(&enableZiti, "ziti", false, "Enable the usage of a ziti network")
	flag.StringVar(&identityJsonFile, "ziti-identity", "", "Path to Ziti Identity json file")
	flag.StringVar(&serviceName, "ziti-name", "", "Name of the Ziti Service")
	flag.Parse()

	// Command line flags take precedence over environment vars, so we only
	// check for environment vars if we have default values for our command
	// line flags.
	var err error
	if address == "" && os.Getenv("ADDRESS") != "" {
		address = os.Getenv("ADDRESS")
	}

	if zitiEnv := os.Getenv("ENABLE_ZITI"); !enableZiti && (zitiEnv == "1" || zitiEnv == "true") {
		enableZiti = true
	}

	httpc := http.DefaultClient
	addr := address

	if enableZiti {
		if identityJsonFile == "" && os.Getenv("ZITI_IDENTITY") != "" {
			identityJsonFile = os.Getenv("ZITI_IDENTITY")
			identityJsonBytes, err = os.ReadFile(identityJsonFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to read identity config JSON from file %s: %s\n", identityJsonFile, err)
				os.Exit(1)
			}
		} else if os.Getenv("ZITI_IDENTITY_JSON") != "" {
			identityJson = os.Getenv("ZITI_IDENTITY_JSON")
			identityJsonBytes = []byte(identityJson)
		}
		if len(identityJsonBytes) == 0 {
			fmt.Fprintf(os.Stderr, "Error: When running a ziti enabled service must have ziti identity provided\n\n")
			flag.Usage()
			os.Exit(1)
		}

		if serviceName == "" && os.Getenv("ZITI_SERVICE_NAME") != "" {
			serviceName = os.Getenv("ZITI_SERVICE_NAME")
		}
		if serviceName == "" {
			fmt.Fprintf(os.Stderr, "Error: When running a ziti enabled service must have ziti service name provided\n\n")
			flag.Usage()
			os.Exit(1)
		}

		cfg := &config.Config{}
		err := json.Unmarshal(identityJsonBytes, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load ziti configuration JSON: %v", err)
			os.Exit(1)
		}
		zitiContext := ziti.NewContextWithConfig(cfg)

		dial := func(_ context.Context, _, addr string) (net.Conn, error) {
			service := strings.Split(addr, ":")[0]
			return zitiContext.Dial(service)
		}

		zitiTransport := http.DefaultTransport.(*http.Transport).Clone()
		zitiTransport.DialContext = dial
		httpc = &http.Client{Transport: zitiTransport}

		addr = serviceName
	}

	c := httpbinclient.NewClient(httpc, addr)

	var request *http.Request
	fmt.Println(flag.Args())
	switch strings.ToLower(flag.Args()[0]) {
	case "get":
		request = c.Get(headers, queries)
	case "post":
		body := io.NopCloser(strings.NewReader(strings.Join(flag.Args()[1:], " ")))
		request = c.Post(headers, queries, body)
	default:
		fmt.Fprint(os.Stderr, "Unknown client command")
		return
	}

	resp, err := c.Do(request)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error doing request: %v", err)
		os.Exit(1)
	}
	fmt.Println(resp)
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading body: %v", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}
