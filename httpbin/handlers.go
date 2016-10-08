package httpbin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var acceptedMediaTypes = []string{
	"image/webp",
	"image/svg+xml",
	"image/jpeg",
	"image/png",
	"image/",
}

// Index renders an HTML index page
func (h *HTTPBin) Index(w http.ResponseWriter, r *http.Request) {
	w.Write(MustAsset("index.html"))
}

// FormsPost renders an HTML form that submits a request to the /post endpoint
func (h *HTTPBin) FormsPost(w http.ResponseWriter, r *http.Request) {
	w.Write(MustAsset("forms-post.html"))
}

// UTF8 renders an HTML encoding stress test
func (h *HTTPBin) UTF8(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(MustAsset("utf8.html"))
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

// Status responds with the specified status code. TODO: support random choice
// from multiple, optionally weighted status codes.
func (h *HTTPBin) Status(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	code, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid status", http.StatusBadRequest)
	}

	type statusCase struct {
		headers map[string]string
		body    []byte
	}

	redirectHeaders := &statusCase{
		headers: map[string]string{
			"Location": "/redirect/1",
		},
	}
	notAcceptableBody, _ := json.Marshal(map[string]interface{}{
		"message": "Client did not request a supported media type",
		"accept":  acceptedMediaTypes,
	})

	specialCases := map[int]*statusCase{
		301: redirectHeaders,
		302: redirectHeaders,
		303: redirectHeaders,
		305: redirectHeaders,
		307: redirectHeaders,
		401: &statusCase{
			headers: map[string]string{
				"WWW-Authenticate": `Basic realm="Fake Realm"`,
			},
		},
		402: &statusCase{
			body: []byte("Fuck you, pay me!"),
			headers: map[string]string{
				"X-More-Info": "http://vimeo.com/22053820",
			},
		},
		406: &statusCase{
			body: notAcceptableBody,
			headers: map[string]string{
				"Content-Type": "application/json; encoding=utf-8",
			},
		},
		407: &statusCase{
			headers: map[string]string{
				"Proxy-Authenticate": `Basic realm="Fake Realm"`,
			},
		},
		418: &statusCase{
			body: []byte("I'm a teapot!"),
			headers: map[string]string{
				"X-More-Info": "http://tools.ietf.org/html/rfc2324",
			},
		},
	}

	if specialCase, ok := specialCases[code]; ok {
		if specialCase.headers != nil {
			for key, val := range specialCase.headers {
				w.Header().Set(key, val)
			}
		}
		w.WriteHeader(code)
		if specialCase.body != nil {
			w.Write(specialCase.body)
		}
	} else {
		w.WriteHeader(code)
	}
}
