// Package main demonstrates how to instrument httpbin with custom metrics.
package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/DataDog/datadog-go/statsd"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
)

func main() {
	statsdClient, _ := statsd.New("")

	app := httpbin.New(
		httpbin.WithObserver(datadogObserver(statsdClient)),
	)

	listenAddr := "0.0.0.0:8080"
	http.ListenAndServe(listenAddr, app)
}

func datadogObserver(client statsd.ClientInterface) httpbin.Observer {
	return func(result httpbin.Result) {
		// Log the request
		log.Printf("%d %s %s %s", result.Status, result.Method, result.URI, result.Duration)

		// Submit a new distribution metric to datadog with tags that allow
		// graphing request rate, timing, errors broken down by
		// method/status/path.
		tags := []string{
			fmt.Sprintf("method:%s", result.Method),
			fmt.Sprintf("status_code:%d", result.Status),
			fmt.Sprintf("status_class:%dxx", result.Status/100),
			fmt.Sprintf("uri:%s", result.URI),
		}
		client.Distribution("httpbin.request", float64(result.Duration.Milliseconds()), tags, 1.0)
	}
}
