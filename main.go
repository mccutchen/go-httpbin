package main

import (
	"log"
	"net/http"
)

// Max size of a request body we'll handle
const maxMemory = 1024*1024*5 + 1

func app() http.Handler {
	h := http.NewServeMux()
	templateWrapper := withTemplates("templates/*.html")
	h.HandleFunc("/", methods(templateWrapper(index), "GET"))
	h.HandleFunc("/forms/post", methods(templateWrapper(formsPost), "GET"))
	h.HandleFunc("/get", methods(get, "GET"))
	h.HandleFunc("/post", methods(post, "POST"))
	h.HandleFunc("/ip", ip)
	h.HandleFunc("/user-agent", userAgent)
	h.HandleFunc("/headers", headers)
	return logger(cors(h))
}

func main() {
	a := app()
	log.Printf("listening on 9999")
	err := http.ListenAndServe(":9999", a)
	log.Fatal(err)
}
