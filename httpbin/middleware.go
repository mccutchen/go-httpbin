package httpbin

import (
	"fmt"
	"log"
	"net/http"
)

func logger(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.RemoteAddr, r.Method, r.URL)
	})
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		respHeader := w.Header()
		respHeader.Set("Access-Control-Allow-Origin", origin)
		respHeader.Set("Access-Control-Allow-Credentials", "true")

		if r.Method == "OPTIONS" {
			respHeader.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
			respHeader.Set("Access-Control-Max-Age", "3600")
			if r.Header.Get("Access-Control-Request-Headers") != "" {
				respHeader.Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			}
		}
		h.ServeHTTP(w, r)
	})
}

func methods(h http.HandlerFunc, methods ...string) http.HandlerFunc {
	if len(methods) == 0 {
		return h
	}
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
