package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// loggingHandler wraps the given handler with a middleware chain that ensures
// it produces structured logs that can be correlated to APM traces.
func loggingHandler(logger zerolog.Logger, handler http.Handler) http.Handler {
	handler = accessLogHandler(handler)
	handler = traceContextHandler(handler)
	handler = hlog.NewHandler(logger)(handler)
	return handler
}

// accessLogHandler logs each request according to DataDog's standard
// structured logging attributes:
// https://docs.datadoghq.com/logs/processing/attributes_naming_convention/#default-standard-attribute-list
func accessLogHandler(next http.Handler) http.Handler {
	return hlog.AccessHandler(func(r *http.Request, status, size int, duration time.Duration) {
		lvl := zerolog.InfoLevel
		switch {
		case status >= 500:
			lvl = zerolog.ErrorLevel
		case status >= 400:
			lvl = zerolog.WarnLevel
		}

		hlog.FromRequest(r).
			WithLevel(lvl).
			Int64("duration", duration.Nanoseconds()).
			Dict("http", zerolog.Dict().
				Str("host", r.Host).
				Str("method", r.Method).
				Int("status_code", status).
				Str("url", r.URL.String()).
				Str("useragent", r.Header.Get("User-Agent"))).
			Dict("network", zerolog.Dict().
				Int64("bytes_read", r.ContentLength).
				Int("bytes_written", size).
				Dict("client", zerolog.Dict().
					Str("ip", getClientIP(r)))).
			Msgf("%d %s %s (%.1fms)",
				status, r.Method, r.RequestURI, float64(duration)/float64(time.Millisecond))
	})(next)
}

// traceContextHandler ensures that dd.trace_id and dd.span_id are added to all
// logging output, so that DataDog will automatically correlate logs to traces.
func traceContextHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// span should be present on all requests via our use of DataDog's
		// mux (see main.go)
		span, _ := tracer.SpanFromContext(r.Context())
		if span.Context().TraceID() != 0 {
			hlog.FromRequest(r).UpdateContext(func(c zerolog.Context) zerolog.Context {
				return c.Dict("dd", zerolog.Dict().
					Uint64("trace_id", span.Context().TraceID()).
					Uint64("span_id", span.Context().SpanID()))
			})
		}
		next.ServeHTTP(w, r)
	})
}

// getClientIP tries to get a reasonable value for the IP address of the
// client making the request. Copied from httpbin's internals.
func getClientIP(r *http.Request) string {
	// Special case some hosting platforms that provide the value directly.
	if clientIP := r.Header.Get("Fly-Client-IP"); clientIP != "" {
		return clientIP
	}
	if clientIP := r.Header.Get("CF-Connecting-IP"); clientIP != "" {
		return clientIP
	}

	// Try to pull a reasonable value from the X-Forwarded-For header, if
	// present, by taking the first entry in a comma-separated list of IPs.
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		return strings.TrimSpace(strings.SplitN(forwardedFor, ",", 2)[0])
	}

	// Finally, fall back on the actual remote addr from the request.
	return r.RemoteAddr
}
