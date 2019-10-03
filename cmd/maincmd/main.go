package maincmd

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
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

// Main implements the go-httpbin CLI's main() function in a reusable way
func Main() {
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
			fmt.Fprintf(os.Stderr, "Error: invalid value %#v for env var MAX_BODY_SIZE: %s\n\n", os.Getenv("MAX_BODY_SIZE"), err)
			flag.Usage()
			os.Exit(1)
		}
	}
	if maxDuration == httpbin.DefaultMaxDuration && os.Getenv("MAX_DURATION") != "" {
		maxDuration, err = time.ParseDuration(os.Getenv("MAX_DURATION"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid value %#v for env var MAX_DURATION: %s\n\n", os.Getenv("MAX_DURATION"), err)
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
			fmt.Fprintf(os.Stderr, "Error: invalid value %#v for env var PORT: %s\n\n", os.Getenv("PORT"), err)
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

	// A hacky log helper function to ensure that shutdown messages are
	// formatted the same as other messages.  See StdLogObserver in
	// httpbin/middleware.go for the format we're matching here.
	serverLog := func(msg string, args ...interface{}) {
		const (
			logFmt  = "time=%q msg=%q"
			dateFmt = "2006-01-02T15:04:05.9999"
		)
		logger.Printf(logFmt, time.Now().Format(dateFmt), fmt.Sprintf(msg, args...))
	}

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

	// shutdownCh triggers graceful shutdown on SIGINT or SIGTERM
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	// exitCh will be closed when it is safe to exit, after graceful shutdown
	exitCh := make(chan struct{})

	go func() {
		sig := <-shutdownCh
		serverLog("shutdown started by signal: %s", sig)

		shutdownTimeout := maxDuration + 1*time.Second
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		server.SetKeepAlivesEnabled(false)
		if err := server.Shutdown(ctx); err != nil {
			serverLog("shutdown error: %s", err)
		}

		close(exitCh)
	}()

	var listenErr error
	if httpsCertFile != "" && httpsKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(httpsCertFile, httpsKeyFile)
		if err != nil {
			logger.Fatalf("failed to generate https key pair: %s", err)
		}
		server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
		}
		serverLog("go-httpbin listening on https://%s", listenAddr)
		listenErr = server.ListenAndServeTLS("", "")
	} else {
		serverLog("go-httpbin listening on http://%s", listenAddr)
		listenErr = server.ListenAndServe()
	}
	if listenErr != nil && listenErr != http.ErrServerClosed {
		logger.Fatalf("failed to listen: %s", listenErr)
	}

	<-exitCh
	serverLog("shutdown finished")
}
