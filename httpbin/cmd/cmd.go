// Package cmd implements the go-httpbin command line interface as a testable
// package.
package cmd

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
)

const (
	defaultListenHost = "0.0.0.0"
	defaultListenPort = 8080

	// Reasonable defaults for our http server
	srvReadTimeout       = 5 * time.Second
	srvReadHeaderTimeout = 1 * time.Second
	srvMaxHeaderBytes    = 16 * 1024 // 16kb
)

// Main is the main entrypoint for the go-httpbin binary. See loadConfig() for
// command line argument parsing.
func Main() int {
	return mainImpl(os.Args[1:], os.Getenv, os.Hostname, os.Stderr)
}

// mainImpl is the real implementation of Main(), extracted for better
// testability.
func mainImpl(args []string, getEnv func(string) string, getHostname func() (string, error), out io.Writer) int {
	logger := log.New(out, "", 0)

	cfg, err := loadConfig(args, getEnv, getHostname)
	if err != nil {
		if cfgErr, ok := err.(ConfigError); ok {
			// for -h/-help, just print usage and exit without error
			if cfgErr.Err == flag.ErrHelp {
				fmt.Fprint(out, cfgErr.Usage)
				return 0
			}

			// anything else indicates a problem with CLI arguments and/or
			// environment vars, so print error and usage and exit with an
			// error status.
			//
			// note: seems like there's consensus that an exit code of 2 is
			// often used to indicate a problem with the way a command was
			// called, e.g.:
			// https://stackoverflow.com/a/40484670/151221
			// https://linuxconfig.org/list-of-exit-codes-on-linux
			fmt.Fprintf(out, "error: %s\n\n%s", cfgErr.Err, cfgErr.Usage)
			return 2
		}
		fmt.Fprintf(out, "error: %s", err)
		return 1
	}

	opts := []httpbin.OptionFunc{
		httpbin.WithMaxBodySize(cfg.MaxBodySize),
		httpbin.WithMaxDuration(cfg.MaxDuration),
		httpbin.WithObserver(httpbin.StdLogObserver(logger)),
		httpbin.WithExcludeHeaders(cfg.ExcludeHeaders),
	}
	if cfg.Prefix != "" {
		opts = append(opts, httpbin.WithPrefix(cfg.Prefix))
	}
	if cfg.RealHostname != "" {
		opts = append(opts, httpbin.WithHostname(cfg.RealHostname))
	}
	if len(cfg.AllowedRedirectDomains) > 0 {
		opts = append(opts, httpbin.WithAllowedRedirectDomains(cfg.AllowedRedirectDomains))
	}
	app := httpbin.New(opts...)

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.ListenHost, strconv.Itoa(cfg.ListenPort)),
		Handler:           app.Handler(),
		MaxHeaderBytes:    srvMaxHeaderBytes,
		ReadHeaderTimeout: srvReadHeaderTimeout,
		ReadTimeout:       srvReadTimeout,
	}

	if err := listenAndServeGracefully(srv, cfg, logger); err != nil {
		logger.Printf("error: %s", err)
		return 1
	}

	return 0
}

// config holds the configuration needed to initialize and run go-httpbin as a
// standalone server.
type config struct {
	AllowedRedirectDomains []string
	ListenHost             string
	ExcludeHeaders         string
	ListenPort             int
	MaxBodySize            int64
	MaxDuration            time.Duration
	Prefix                 string
	RealHostname           string
	TLSCertFile            string
	TLSKeyFile             string

	// temporary placeholders for arguments that need extra processing
	rawAllowedRedirectDomains string
	rawUseRealHostname        bool
}

// ConfigError is used to signal an error with a command line argument or
// environmment variable.
//
// It carries the command's usage output, so that we can decouple configuration
// parsing from error reporting for better testability.
type ConfigError struct {
	Err   error
	Usage string
}

// Error implements the error interface.
func (e ConfigError) Error() string {
	return e.Err.Error()
}

// loadConfig parses command line arguments and env vars into a fully resolved
// Config struct. Command line arguments take precedence over env vars.
func loadConfig(args []string, getEnv func(string) string, getHostname func() (string, error)) (*config, error) {
	cfg := &config{}

	fs := flag.NewFlagSet("go-httpbin", flag.ContinueOnError)
	fs.BoolVar(&cfg.rawUseRealHostname, "use-real-hostname", false, "Expose value of os.Hostname() in the /hostname endpoint instead of dummy value")
	fs.DurationVar(&cfg.MaxDuration, "max-duration", httpbin.DefaultMaxDuration, "Maximum duration a response may take")
	fs.Int64Var(&cfg.MaxBodySize, "max-body-size", httpbin.DefaultMaxBodySize, "Maximum size of request or response, in bytes")
	fs.IntVar(&cfg.ListenPort, "port", defaultListenPort, "Port to listen on")
	fs.StringVar(&cfg.rawAllowedRedirectDomains, "allowed-redirect-domains", "", "Comma-separated list of domains the /redirect-to endpoint will allow")
	fs.StringVar(&cfg.ListenHost, "host", defaultListenHost, "Host to listen on")
	fs.StringVar(&cfg.Prefix, "prefix", "", "Path prefix (empty or start with slash and does not end with slash)")
	fs.StringVar(&cfg.TLSCertFile, "https-cert-file", "", "HTTPS Server certificate file")
	fs.StringVar(&cfg.TLSKeyFile, "https-key-file", "", "HTTPS Server private key file")
	fs.StringVar(&cfg.ExcludeHeaders, "exclude-headers", "", "Drop platform-specific headers. Comma-separated list of headers key to drop, supporting wildcard matching.")

	// in order to fully control error output whether CLI arguments or env vars
	// are used to configure the app, we need to take control away from the
	// flagset, which by defaults prints errors automatically.
	//
	// so, we capture the "usage" output it would generate and then trick it
	// into generating no output on errors, since they'll be handled by the
	// caller.
	//
	// yes, this is goofy, but it makes the CLI testable!
	buf := &bytes.Buffer{}
	fs.SetOutput(buf)
	fs.Usage()
	usage := buf.String()
	fs.SetOutput(io.Discard)

	if err := fs.Parse(args); err != nil {
		return nil, ConfigError{err, usage}
	}

	// helper to generate a new ConfigError to return
	configErr := func(format string, a ...interface{}) error {
		return ConfigError{
			Err:   fmt.Errorf(format, a...),
			Usage: usage,
		}
	}

	var err error

	// Command line flags take precedence over environment vars, so we only
	// check for environment vars if we have default values for our command
	// line flags.
	if cfg.MaxBodySize == httpbin.DefaultMaxBodySize && getEnv("MAX_BODY_SIZE") != "" {
		cfg.MaxBodySize, err = strconv.ParseInt(getEnv("MAX_BODY_SIZE"), 10, 64)
		if err != nil {
			return nil, configErr("invalid value %#v for env var MAX_BODY_SIZE: parse error", getEnv("MAX_BODY_SIZE"))
		}
	}

	if cfg.MaxDuration == httpbin.DefaultMaxDuration && getEnv("MAX_DURATION") != "" {
		cfg.MaxDuration, err = time.ParseDuration(getEnv("MAX_DURATION"))
		if err != nil {
			return nil, configErr("invalid value %#v for env var MAX_DURATION: parse error", getEnv("MAX_DURATION"))
		}
	}
	if cfg.ListenHost == defaultListenHost && getEnv("HOST") != "" {
		cfg.ListenHost = getEnv("HOST")
	}
	if cfg.Prefix == "" {
		if prefix := getEnv("PREFIX"); prefix != "" {
			cfg.Prefix = prefix
		}
	}
	if cfg.Prefix != "" {
		if !strings.HasPrefix(cfg.Prefix, "/") {
			return nil, configErr("Prefix %#v must start with a slash", cfg.Prefix)
		}
		if strings.HasSuffix(cfg.Prefix, "/") {
			return nil, configErr("Prefix %#v must not end with a slash", cfg.Prefix)
		}
	}
	if cfg.ExcludeHeaders == "" && getEnv("EXCLUDE_HEADERS") != "" {
		cfg.ExcludeHeaders = getEnv("EXCLUDE_HEADERS")
	}
	if cfg.ListenPort == defaultListenPort && getEnv("PORT") != "" {
		cfg.ListenPort, err = strconv.Atoi(getEnv("PORT"))
		if err != nil {
			return nil, configErr("invalid value %#v for env var PORT: parse error", getEnv("PORT"))
		}
	}

	if cfg.TLSCertFile == "" && getEnv("HTTPS_CERT_FILE") != "" {
		cfg.TLSCertFile = getEnv("HTTPS_CERT_FILE")
	}
	if cfg.TLSKeyFile == "" && getEnv("HTTPS_KEY_FILE") != "" {
		cfg.TLSKeyFile = getEnv("HTTPS_KEY_FILE")
	}
	if cfg.TLSCertFile != "" || cfg.TLSKeyFile != "" {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			return nil, configErr("https cert and key must both be provided")
		}
	}

	// useRealHostname will be true if either the `-use-real-hostname`
	// arg is given on the command line or if the USE_REAL_HOSTNAME env var
	// is one of "1" or "true".
	if useRealHostnameEnv := getEnv("USE_REAL_HOSTNAME"); useRealHostnameEnv == "1" || useRealHostnameEnv == "true" {
		cfg.rawUseRealHostname = true
	}
	if cfg.rawUseRealHostname {
		cfg.RealHostname, err = getHostname()
		if err != nil {
			return nil, fmt.Errorf("could not look up real hostname: %w", err)
		}
	}

	// split comma-separated list of domains into a slice, if given
	if cfg.rawAllowedRedirectDomains == "" && getEnv("ALLOWED_REDIRECT_DOMAINS") != "" {
		cfg.rawAllowedRedirectDomains = getEnv("ALLOWED_REDIRECT_DOMAINS")
	}
	for _, domain := range strings.Split(cfg.rawAllowedRedirectDomains, ",") {
		if strings.TrimSpace(domain) != "" {
			cfg.AllowedRedirectDomains = append(cfg.AllowedRedirectDomains, strings.TrimSpace(domain))
		}
	}

	// reset temporary fields to their zero values
	cfg.rawAllowedRedirectDomains = ""
	cfg.rawUseRealHostname = false
	return cfg, nil
}

func listenAndServeGracefully(srv *http.Server, cfg *config, logger *log.Logger) error {
	doneCh := make(chan error, 1)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		<-sigCh

		logger.Printf("shutting down ...")
		ctx, cancel := context.WithTimeout(context.Background(), cfg.MaxDuration+1*time.Second)
		defer cancel()
		doneCh <- srv.Shutdown(ctx)
	}()

	var err error
	if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
		logger.Printf("go-httpbin listening on https://%s", srv.Addr)
		err = srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	} else {
		logger.Printf("go-httpbin listening on http://%s", srv.Addr)
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return <-doneCh
}
