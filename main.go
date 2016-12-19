package main

import (
	"log"
	"net/http"
	"time"

	"github.com/mccutchen/go-httpbin/httpbin"
)

func main() {
	h := httpbin.NewHTTPBin(&httpbin.Options{
		MaxMemory:       1024 * 1024 * 5,
		MaxResponseTime: 10 * time.Second,
	})
	log.Printf("listening on 9999")
	err := http.ListenAndServe(":9999", h.Handler())
	log.Fatal(err)
}
