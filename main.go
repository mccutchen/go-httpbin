package main

import (
	"encoding/json"
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

func getOrigin(r *http.Request) string {
	origin := r.Header.Get("X-Forwarded-For")
	if origin == "" {
		origin = r.RemoteAddr
	}
	return origin
}

func getURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = r.Header.Get("X-Forwarded-Protocol")
	}
	if scheme == "" && r.Header.Get("X-Forwarded-Ssl") == "on" {
		scheme = "https"
	}
	if scheme == "" {
		scheme = "http"
	}

	host := r.URL.Host
	if host == "" {
		host = r.Host
	}

	u := &url.URL{
		Scheme:     scheme,
		Opaque:     r.URL.Opaque,
		User:       r.URL.User,
		Host:       host,
		Path:       r.URL.Path,
		RawPath:    r.URL.RawPath,
		ForceQuery: r.URL.ForceQuery,
		RawQuery:   r.URL.RawQuery,
		Fragment:   r.URL.Fragment,
	}
	return u.String()
}

func writeResponse(w http.ResponseWriter, r *http.Request, resp *Resp) {
	resp.Origin = getOrigin(r)
	resp.URL = getURL(r)

	body, err := json.Marshal(resp)
	if err != nil {
		log.Printf("error marshalling %v as JSON: %s", resp, err)
	}
	w.Write(body)
}

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
