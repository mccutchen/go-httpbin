package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
)

// Resp is the standard JSON response from httpbin
type Resp struct {
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`

	Data  string              `json:"data,omitempty"`
	Files map[string][]string `json:"files,omitempty"`
	Form  map[string][]string `json:"form,omitempty"`
	JSON  map[string][]string `json:"json,omitempty"`
}

func index(w http.ResponseWriter, r *http.Request) {
	t, err := template.ParseGlob("templates/*.html")
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing templates: %s", err), http.StatusInternalServerError)
		return
	}
	t = t.Lookup("index.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	t.Execute(w, nil)
}

func get(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	resp := &Resp{
		Args:    r.Form,
		Headers: r.Header,
	}
	writeResponse(w, r, resp)
}

func app() http.Handler {
	h := http.NewServeMux()
	h.HandleFunc("/", index)
	h.HandleFunc("/get", get)
	return logger(cors(h))
}

func main() {
	a := app()
	log.Printf("listening on 9999")
	err := http.ListenAndServe(":9999", a)
	log.Fatal(err)
}
