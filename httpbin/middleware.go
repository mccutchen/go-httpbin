package httpbin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"
)

const loggerDateFormat string = "2006-01-02T15:04:05.9999"

func metaRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		respHeader := w.Header()
		respHeader.Set("Access-Control-Allow-Origin", origin)
		respHeader.Set("Access-Control-Allow-Credentials", "true")

		switch r.Method {
		case "OPTIONS":
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, HEAD, PUT, DELETE, PATCH, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "3600")
			if r.Header.Get("Access-Control-Request-Headers") != "" {
				w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			}
			w.WriteHeader(200)
		case "HEAD":
			rwRec := httptest.NewRecorder()
			r.Method = "GET"
			h.ServeHTTP(rwRec, r)

			copyHeader(w.Header(), rwRec.Header())
			w.WriteHeader(rwRec.Code)
		default:
			h.ServeHTTP(w, r)
		}
	})
}

func methods(h http.HandlerFunc, methods ...string) http.HandlerFunc {
	methodMap := make(map[string]struct{}, len(methods))
	for _, m := range methods {
		methodMap[m] = struct{}{}
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

// metaResponseWriter implements http.ResponseWriter and http.Flusher in order
// to record a response's status code and body size for logging purposes.
type metaResponseWriter struct {
	w      http.ResponseWriter
	status int
	size   int
}

func (mw *metaResponseWriter) Write(b []byte) (int, error) {
	size, err := mw.w.Write(b)
	mw.size += size
	return size, err
}

func (mw *metaResponseWriter) WriteHeader(s int) {
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

func (mw *metaResponseWriter) Size() int {
	return mw.size
}

func logger(l Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMethod, reqURI := r.Method, r.URL.RequestURI()
		mw := &metaResponseWriter{w: w}
		t := time.Now()
		h.ServeHTTP(mw, r)
		end := time.Now()
		// non-obvious conversion from time.Duration to float64 milliseconds
		// https://github.com/golang/go/issues/5491#issuecomment-66079585
		duration := end.Sub(t).Seconds() * 1e3
		l.Printf("time=%q status=%d method=%q uri=%q size_bytes=%d duration_ms=%0.02f", end.Format(loggerDateFormat), mw.Status(), reqMethod, reqURI, mw.Size(), duration)
	})
}
