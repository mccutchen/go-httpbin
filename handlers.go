package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
)

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

// utf8 must be wrapped by withTemplates middleware before it can be used
func utf8(w http.ResponseWriter, r *http.Request, t *template.Template) {
	t = t.Lookup("utf8.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, nil)
}

func get(w http.ResponseWriter, r *http.Request) {
	args, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing query params: %s", err), http.StatusBadRequest)
		return
	}
	resp := &getResponse{
		Args:    args,
		Headers: r.Header,
		Origin:  getOrigin(r),
		URL:     getURL(r),
	}
	body, _ := json.Marshal(resp)
	writeJSON(w, body, http.StatusOK)
}

func requestWithBody(w http.ResponseWriter, r *http.Request) {
	args, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing query params: %s", err), http.StatusBadRequest)
		return
	}

	resp := &bodyResponse{
		Args:    args,
		Headers: r.Header,
		Origin:  getOrigin(r),
		URL:     getURL(r),
	}

	err = parseBody(w, r, resp)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing request body: %s", err), http.StatusBadRequest)
	}

	body, _ := json.Marshal(resp)
	writeJSON(w, body, http.StatusOK)
}

func ip(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&ipResponse{
		Origin: getOrigin(r),
	})
	writeJSON(w, body, http.StatusOK)
}

func userAgent(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&userAgentResponse{
		UserAgent: r.Header.Get("User-Agent"),
	})
	writeJSON(w, body, http.StatusOK)
}

func headers(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&headersResponse{
		Headers: r.Header,
	})
	writeJSON(w, body, http.StatusOK)
}
