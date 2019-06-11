package main

import (
	"crypto/tls"
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

const defaultHost = "0.0.0.0"
const defaultPort = 8080

var (
	host          string
	port          int
	maxBodySize   int64
	maxDuration   time.Duration
	httpsCertFile string
	httpsKeyFile  string
)

func main() {
	flag.StringVar(&host, "host", defaultHost, "Host to listen on")
	flag.IntVar(&port, "port", defaultPort, "Port to listen on")
	flag.StringVar(&httpsCertFile, "https-cert-file", "", "HTTPS Server certificate file")
	flag.StringVar(&httpsKeyFile, "https-key-file", "", "HTTPS Server private key file")
	flag.Int64Var(&maxBodySize, "max-body-size", httpbin.DefaultMaxBodySize, "Maximum size of request or response, in bytes")
	flag.DurationVar(&maxDuration, "max-duration", httpbin.DefaultMaxDuration, "Maximum duration a response may take")
	flag.Parse()

	// Command line flags take precedence over environment vars, so we only
	// check for environment vars if we have default values for our command
	// line flags.
	var err error
	if maxBodySize == httpbin.DefaultMaxBodySize && os.Getenv("MAX_BODY_SIZE") != "" {
		maxBodySize, err = strconv.ParseInt(os.Getenv("MAX_BODY_SIZE"), 10, 64)
		if err != nil {
			fmt.Printf("invalid value %#v for env var MAX_BODY_SIZE: %s\n", os.Getenv("MAX_BODY_SIZE"), err)
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
	if host == defaultHost && os.Getenv("HOST") != "" {
		host = os.Getenv("HOST")
	}
	if port == defaultPort && os.Getenv("PORT") != "" {
		port, err = strconv.Atoi(os.Getenv("PORT"))
		if err != nil {
			fmt.Printf("invalid value %#v for env var PORT: %s\n", os.Getenv("PORT"), err)
			flag.Usage()
			os.Exit(1)
		}
	}

	if httpsCertFile == "" && os.Getenv("HTTPS_CERT_FILE") != "" {
		httpsCertFile = os.Getenv("HTTPS_CERT_FILE")
	}
	if httpsKeyFile == "" && os.Getenv("HTTPS_KEY_FILE") != "" {
		httpsKeyFile = os.Getenv("HTTPS_KEY_FILE")
	}

	logger := log.New(os.Stderr, "", 0)

	h := httpbin.New(
		httpbin.WithMaxBodySize(maxBodySize),
		httpbin.WithMaxDuration(maxDuration),
		httpbin.WithObserver(httpbin.StdLogObserver(logger)),
	)

	listenAddr := net.JoinHostPort(host, strconv.Itoa(port))

	server := &http.Server{
		Addr:    listenAddr,
		Handler: h.Handler(),
	}

	var listenErr error
	if httpsCertFile != "" && httpsKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(httpsCertFile, httpsKeyFile)
		if err != nil {
			logger.Fatal("Failed to generate https key pair: ", err)
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		logger.Printf("go-httpbin listening on https://%s", listenAddr)
		listenErr = server.ListenAndServeTLS("", "")
	} else {
		logger.Printf("go-httpbin listening on http://%s", listenAddr)
		listenErr = server.ListenAndServe()
	}
	if listenErr != nil {
		logger.Fatalf("Failed to listen: %s", listenErr)
	}
}
