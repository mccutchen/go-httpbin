package httpbin

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func preflight(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		respHeader := w.Header()
		respHeader.Set("Access-Control-Allow-Origin", origin)
		respHeader.Set("Access-Control-Allow-Credentials", "true")

		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "3600")
			if r.Header.Get("Access-Control-Request-Headers") != "" {
				w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			}
			w.WriteHeader(200)
			return
		}

		h.ServeHTTP(w, r)
	})
}

func methods(h http.HandlerFunc, methods ...string) http.HandlerFunc {
	methodMap := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		methodMap[m] = struct{}{}
		// GET implies support for HEAD
		if m == "GET" {
			methodMap["HEAD"] = struct{}{}
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := methodMap[r.Method]; !ok {
			http.Error(w, fmt.Sprintf("method %s not allowed", r.Method), http.StatusMethodNotAllowed)
			return
		}
		h.ServeHTTP(w, r)
	}
}

func limitRequestSize(maxSize int64, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
		}
		h.ServeHTTP(w, r)
	})
}

// headResponseWriter implements http.ResponseWriter in order to discard the
// body of the response
type headResponseWriter struct {
	*metaResponseWriter
}

func (hw *headResponseWriter) Write(b []byte) (int, error) {
	return len(b), nil
}

// autohead automatically discards the body of responses to HEAD requests
func autohead(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w = &headResponseWriter{&metaResponseWriter{w: w}}
		}
		h.ServeHTTP(w, r)
	})
}

// testMode enables additional safety checks to be enabled in the test suite.
var testMode = false

// metaResponseWriter implements http.ResponseWriter and http.Flusher in order
// to record a response's status code and body size for logging purposes.
type metaResponseWriter struct {
	w      http.ResponseWriter
	status int
	size   int64
}

func (mw *metaResponseWriter) Write(b []byte) (int, error) {
	size, err := mw.w.Write(b)
	mw.size += int64(size)
	return size, err
}

func (mw *metaResponseWriter) WriteHeader(s int) {
	if testMode && mw.status != 0 {
		panic(fmt.Errorf("HTTP status already set to %d, cannot set to %d", mw.status, s))
	}
	mw.w.WriteHeader(s)
	mw.status = s
}

func (mw *metaResponseWriter) Flush() {
	f := mw.w.(http.Flusher)
	f.Flush()
}

func (mw *metaResponseWriter) Header() http.Header {
	return mw.w.Header()
}

func (mw *metaResponseWriter) Status() int {
	if mw.status == 0 {
		return http.StatusOK
	}
	return mw.status
}

func (mw *metaResponseWriter) Size() int64 {
	return mw.size
}

func (mw *metaResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return mw.w.(http.Hijacker).Hijack()
}

func observe(o Observer, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw := &metaResponseWriter{w: w}
		t := time.Now()
		h.ServeHTTP(mw, r)
		o(Result{
			Status:    mw.Status(),
			Method:    r.Method,
			URI:       r.URL.RequestURI(),
			Size:      mw.Size(),
			Duration:  time.Since(t),
			UserAgent: r.Header.Get("User-Agent"),
			ClientIP:  getClientIP(r),
		})
	})
}

// Result is the result of handling a request, used for instrumentation
type Result struct {
	Status    int
	Method    string
	URI       string
	Size      int64
	Duration  time.Duration
	UserAgent string
	ClientIP  string
}

// Observer is a function that will be called with the details of a handled
// request, which can be used for logging, instrumentation, etc
type Observer func(result Result)

// StdLogObserver creates an Observer that will log each request in structured
// format using the given stdlib logger
func StdLogObserver(l *slog.Logger) Observer {
	const (
		dateFmt = "2006-01-02T15:04:05.9999"
	)
	return func(result Result) {
		l.With("time", time.Now().Format(dateFmt)).
			With("status", result.Status).
			With("method", result.Method).
			With("uri", result.URI).
			With("size_bytes", result.Size).
			With("duration_ms", result.Duration.Seconds()*1e3).
			With("user_agent", result.UserAgent).
			With("client_ip", result.ClientIP).
			Info("request")
	}
}
