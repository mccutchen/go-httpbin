package httpbin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Index renders an HTML index page
func (h *HTTPBin) Index(w http.ResponseWriter, r *http.Request) {
	t := h.templates.Lookup("index.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	t.Execute(w, nil)
}

// FormsPost renders an HTML form that submits a request to the /post endpoint
func (h *HTTPBin) FormsPost(w http.ResponseWriter, r *http.Request) {
	t := h.templates.Lookup("forms-post.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	t.Execute(w, nil)
}

// UTF8 renders an HTML encoding stress test
func (h *HTTPBin) UTF8(w http.ResponseWriter, r *http.Request) {
	t := h.templates.Lookup("utf8.html")
	if t == nil {
		http.Error(w, fmt.Sprintf("error looking up index.html"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t.Execute(w, nil)
}

// Get handles HTTP GET requests
func (h *HTTPBin) Get(w http.ResponseWriter, r *http.Request) {
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

// RequestWithBody handles POST, PUT, and PATCH requests
func (h *HTTPBin) RequestWithBody(w http.ResponseWriter, r *http.Request) {
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

	err = parseBody(w, r, resp, h.options.MaxMemory)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing request body: %s", err), http.StatusBadRequest)
	}

	body, _ := json.Marshal(resp)
	writeJSON(w, body, http.StatusOK)
}

// IP echoes the IP address of the incoming request
func (h *HTTPBin) IP(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&ipResponse{
		Origin: getOrigin(r),
	})
	writeJSON(w, body, http.StatusOK)
}

// UserAgent echoes the incoming User-Agent header
func (h *HTTPBin) UserAgent(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&userAgentResponse{
		UserAgent: r.Header.Get("User-Agent"),
	})
	writeJSON(w, body, http.StatusOK)
}

// Headers echoes the incoming request headers
func (h *HTTPBin) Headers(w http.ResponseWriter, r *http.Request) {
	body, _ := json.Marshal(&headersResponse{
		Headers: r.Header,
	})
	writeJSON(w, body, http.StatusOK)
}
