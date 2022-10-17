package main

import (
	"context"
	"encoding/json"
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

	"github.com/mccutchen/go-httpbin/v2/httpbin"
	"github.com/openziti/sdk-golang/ziti"
	"github.com/openziti/sdk-golang/ziti/config"
)

const (
	defaultHost = "0.0.0.0"
	defaultPort = 8080
)

var (
	host            string
	port            int
	maxBodySize     int64
	maxDuration     time.Duration
	httpsCertFile   string
	httpsKeyFile    string
	useRealHostname bool

	identityJson      string
	identityJsonFile  string
	identityJsonBytes []byte
	serviceName       string
	enableZiti        bool
)

func main() {
	flag.StringVar(&host, "host", defaultHost, "Host to listen on")
	flag.IntVar(&port, "port", defaultPort, "Port to listen on")
	flag.StringVar(&httpsCertFile, "https-cert-file", "", "HTTPS Server certificate file")
	flag.StringVar(&httpsKeyFile, "https-key-file", "", "HTTPS Server private key file")
	flag.Int64Var(&maxBodySize, "max-body-size", httpbin.DefaultMaxBodySize, "Maximum size of request or response, in bytes")
	flag.DurationVar(&maxDuration, "max-duration", httpbin.DefaultMaxDuration, "Maximum duration a response may take")
	flag.BoolVar(&useRealHostname, "use-real-hostname", false, "Expose value of os.Hostname() in the /hostname endpoint instead of dummy value")

	flag.BoolVar(&enableZiti, "ziti", false, "Enable the usage of a ziti network")
	flag.StringVar(&identityJsonFile, "ziti-identity", "", "Path to Ziti Identity json file")
	flag.StringVar(&serviceName, "ziti-name", "", "Name of the Ziti Service")
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

	var serveTLS bool
	if httpsCertFile != "" || httpsKeyFile != "" {
		serveTLS = true
		if httpsCertFile == "" || httpsKeyFile == "" {
			fmt.Fprintf(os.Stderr, "Error: https cert and key must both be provided\n\n")
			flag.Usage()
			os.Exit(1)
		}
	}

	// useRealHostname will be true if either the `-use-real-hostname`
	// arg is given on the command line or if the USE_REAL_HOSTNAME env var
	// is one of "1" or "true".
	if useRealHostnameEnv := os.Getenv("USE_REAL_HOSTNAME"); useRealHostnameEnv == "1" || useRealHostnameEnv == "true" {
		useRealHostname = true
	}

	if zitiEnv := os.Getenv("ENABLE_ZITI"); !enableZiti && (zitiEnv == "1" || zitiEnv == "true") {
		enableZiti = true
	}

	if enableZiti {
		if identityJsonFile == "" && os.Getenv("ZITI_IDENTITY") != "" {
			identityJsonFile = os.Getenv("ZITI_IDENTITY")
		}
		if os.Getenv("ZITI_IDENTITY_JSON") != "" {
			identityJson = os.Getenv("ZITI_IDENTITY_JSON")
			identityJsonBytes = []byte(identityJson)
		} else {
			identityJsonBytes, err = os.ReadFile(identityJsonFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to read identity config JSON from file %s: %s\n", identityJsonFile, err)
				os.Exit(1)
			}
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

	opts := []httpbin.OptionFunc{
		httpbin.WithMaxBodySize(maxBodySize),
		httpbin.WithMaxDuration(maxDuration),
		httpbin.WithObserver(httpbin.StdLogObserver(logger)),
	}
	if useRealHostname {
		hostname, err := os.Hostname()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: use-real-hostname=true but hostname lookup failed: %s\n", err)
			os.Exit(1)
		}
		opts = append(opts, httpbin.WithHostname(hostname))
	}
	h := httpbin.New(opts...)

	listenAddr := net.JoinHostPort(host, strconv.Itoa(port))

	var listener net.Listener

	if enableZiti {
		config := config.Config{}
		err = json.Unmarshal(identityJsonBytes, &config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load ziti configuration JSON: %v", err)
			os.Exit(1)
		}
		zitiContext := ziti.NewContextWithConfig(&config)
		if err := zitiContext.Authenticate(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: Unable to authenticate ziti: %v\n\n", err)
			os.Exit(1)
		}

		listener, err = zitiContext.Listen(serviceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Unable to listen on ziti network: %v\n\n", err)
			os.Exit(1)
		}
	} else {
		listener, err = net.Listen("tcp", listenAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: Unable to listen on %s: %v\n\n", listenAddr, err)
			os.Exit(1)
		}
	}
	server := &http.Server{
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
	getListening := func() string {
		if enableZiti {
			return fmt.Sprintf("ziti serviceName=%s", serviceName)
		}
		s := "http"
		if serveTLS {
			s += "s"
		}
		return fmt.Sprintf("%s://%s", s, listenAddr)
	}
	if serveTLS {
		serverLog("go-httpbin listening on %s", getListening())
		listenErr = server.ServeTLS(listener, httpsCertFile, httpsKeyFile)
	} else {
		serverLog("go-httpbin listening on %s", getListening())
		listenErr = server.Serve(listener)
	}
	if listenErr != nil && listenErr != http.ErrServerClosed {
		serverLog("%T", listenErr)
		logger.Fatalf("failed to listen: %s", listenErr)
	}

	<-exitCh
	serverLog("shutdown finished")
}
