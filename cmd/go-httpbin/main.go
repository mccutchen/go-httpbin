package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/mccutchen/go-httpbin/httpbin"
)

const defaultPort = 8080

var (
	port        int
	maxMemory   int64
	maxDuration time.Duration
)

func main() {
	flag.IntVar(&port, "port", defaultPort, "Port to listen on")
	flag.Int64Var(&maxMemory, "max-memory", httpbin.DefaultMaxMemory, "Maximum size of request or response, in bytes")
	flag.DurationVar(&maxDuration, "max-duration", httpbin.DefaultMaxDuration, "Maximum duration a response may take")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Lmicroseconds | log.LUTC)

	// Command line flags take precedence over environment vars, so we only
	// check for environment vars if we have default values for our command
	// line flags.
	var err error
	if maxMemory == httpbin.DefaultMaxMemory && os.Getenv("MAX_MEMORY") != "" {
		maxMemory, err = strconv.ParseInt(os.Getenv("MAX_MEMORY"), 10, 64)
		if err != nil {
			fmt.Printf("invalid value %#v for env var MAX_MEMORY: %s\n", os.Getenv("MAX_MEMORY"), err)
			flag.Usage()
			os.Exit(1)
		}
	}
	if maxDuration == httpbin.DefaultMaxDuration && os.Getenv("MAX_DURATION") != "" {
		maxDuration, err = time.ParseDuration(os.Getenv("MAX_DURATION"))
		if err != nil {
			fmt.Printf("invalid value %#v for env var MAX_DURATION: %s\n", os.Getenv("MAX_DURATION"), err)
			flag.Usage()
			os.Exit(1)
		}
	}
	if port == defaultPort && os.Getenv("PORT") != "" {
		port, err = strconv.Atoi(os.Getenv("PORT"))
		if err != nil {
			fmt.Printf("invalid value %#v for env var PORT: %s\n", os.Getenv("PORT"), err)
			flag.Usage()
			os.Exit(1)
		}
	}

	h := httpbin.NewHTTPBinWithOptions(&httpbin.Options{
		MaxMemory:   maxMemory,
		MaxDuration: maxDuration,
	})

	listenAddr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	log.Printf("addr=%s", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, h.Handler()))
}
