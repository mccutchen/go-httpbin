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

// IPResp is the response for the /ip endpoint
type IPResp struct {
	Origin string `json:"origin"`
}

// HeadersResp is the response for the /headers endpoint
type HeadersResp struct {
	Headers http.Header `json:"headers"`
}

// UserAgentResp is the response for the /user-agent endpoint
type UserAgentResp struct {
	UserAgent string `json:"user-agent"`
}

// Index must be wrapped by the withTemplates middleware before it can be used
func index(w http.ResponseWriter, r *http.Request, t *template.Template) {
	t = t.Lookup("index.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	t.Execute(w, nil)
}

// FormsPost must be wrapped by withTemplates middleware before it can be used
func formsPost(w http.ResponseWriter, r *http.Request, t *template.Template) {
	t = t.Lookup("forms-post.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	t.Execute(w, nil)
}

func get(w http.ResponseWriter, r *http.Request) {
	args, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing query params: %s", err), http.StatusBadRequest)
		return
	}
	resp := &Resp{
		Args:    args,
		Headers: r.Header,
	}
	writeResponse(w, r, resp)
}

func ip(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&IPResp{
		Origin: getOrigin(r),
	})
	writeJSON(w, body)
}

func userAgent(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&UserAgentResp{
		UserAgent: r.Header.Get("User-Agent"),
	})
	writeJSON(w, body)
}

func headers(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&HeadersResp{
		Headers: r.Header,
	})
	writeJSON(w, body)
}

func app() http.Handler {
	h := http.NewServeMux()
	templateWrapper := withTemplates("templates/*.html")
	h.HandleFunc("/", methods(templateWrapper(index), "GET"))
	h.HandleFunc("/forms/post", methods(templateWrapper(formsPost), "GET"))
	h.HandleFunc("/get", methods(get, "GET"))
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
