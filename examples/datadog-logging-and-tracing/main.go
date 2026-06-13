package main

import (
	stdlog "log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	ddhttp "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
)

const (
	readHeaderTimeout = 500 * time.Millisecond
	readTimeout       = 1 * time.Second
)

func main() {
	tracer.Start()
	defer tracer.Stop()

	listenAddr := os.Getenv("LISTEN_ADDR")
	if listenAddr == "" {
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		listenAddr = net.JoinHostPort("", port)
	}

	serviceName := os.Getenv("DD_SERVICE")
	if serviceName == "" {
		serviceName = "go-httpbin"
	}

	logger := newLogger()

	var handler http.Handler = httpbin.New()
	handler = loggingHandler(logger, handler)
	handler = ddhttp.WrapHandler(handler, serviceName, "", ddhttp.WithResourceNamer(func(r *http.Request) string {
		return r.Method + " " + r.URL.String()
	}))

	srv := &http.Server{
		Handler:           handler,
		Addr:              listenAddr,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
	}

	logger.Info().Msgf("go-httpbin listening on %s", listenAddr)
	if err := srv.ListenAndServe(); err != nil {
		logger.Error().Err(err).Msg("failed to start server")
		os.Exit(1)
	}
}

func newLogger() zerolog.Logger {
	// This logging configuration can only be set globally
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = "2006-01-02T15:04:05.999Z"

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger().Level(zerolog.InfoLevel)

	// Also capture any uses of the global stdlib logger
	stdlog.SetFlags(0)
	stdlog.SetOutput(logger)

	return logger
}
