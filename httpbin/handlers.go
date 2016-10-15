package httpbin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
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
	if r.URL.Path != "/" {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
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
	resp := &getResponse{
		Args:    r.URL.Query(),
		Headers: r.Header,
		Origin:  getOrigin(r),
		URL:     getURL(r).String(),
	}
	body, _ := json.Marshal(resp)
	writeJSON(w, body, http.StatusOK)
}

// RequestWithBody handles POST, PUT, and PATCH requests
func (h *HTTPBin) RequestWithBody(w http.ResponseWriter, r *http.Request) {
	resp := &bodyResponse{
		Args:    r.URL.Query(),
		Headers: r.Header,
		Origin:  getOrigin(r),
		URL:     getURL(r).String(),
	}

	err := parseBody(w, r, resp, h.options.MaxMemory)
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
				"Content-Type": jsonContentType,
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

// ResponseHeaders responds with a map of header values
func (h *HTTPBin) ResponseHeaders(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	for k, vs := range args {
		for _, v := range vs {
			w.Header().Add(http.CanonicalHeaderKey(k), v)
		}
	}
	body, _ := json.Marshal(args)
	if contentType := w.Header().Get("Content-Type"); contentType == "" {
		w.Header().Set("Content-Type", jsonContentType)
	}
	w.Write(body)
}

func redirectLocation(r *http.Request, relative bool, n int) string {
	var location string
	var path string

	if n < 1 {
		path = "/get"
	} else if relative {
		path = fmt.Sprintf("/relative-redirect/%d", n)
	} else {
		path = fmt.Sprintf("/absolute-redirect/%d", n)
	}

	if relative {
		location = path
	} else {
		u := getURL(r)
		u.Path = path
		u.RawQuery = ""
		location = u.String()
	}

	return location
}

func doRedirect(w http.ResponseWriter, r *http.Request, relative bool) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 1 {
		http.Error(w, "Invalid redirect", http.StatusBadRequest)
	}

	w.Header().Set("Location", redirectLocation(r, relative, n-1))
	w.WriteHeader(http.StatusFound)
}

// Redirect responds with 302 redirect a given number of times. Defaults to a
// relative redirect, but an ?absolute=true query param will trigger an
// absolute redirect.
func (h *HTTPBin) Redirect(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	relative := strings.ToLower(params.Get("absolute")) != "true"
	doRedirect(w, r, relative)
}

// RelativeRedirect responds with an HTTP 302 redirect a given number of times
func (h *HTTPBin) RelativeRedirect(w http.ResponseWriter, r *http.Request) {
	doRedirect(w, r, true)
}

// AbsoluteRedirect responds with an HTTP 302 redirect a given number of times
func (h *HTTPBin) AbsoluteRedirect(w http.ResponseWriter, r *http.Request) {
	doRedirect(w, r, false)
}

// Cookies responds with the cookies in the incoming request
func (h *HTTPBin) Cookies(w http.ResponseWriter, r *http.Request) {
	resp := cookiesResponse{}
	for _, c := range r.Cookies() {
		resp[c.Name] = c.Value
	}
	body, _ := json.Marshal(resp)
	writeJSON(w, body, http.StatusOK)
}

// SetCookies sets cookies as specified in query params and redirects to
// Cookies endpoint
func (h *HTTPBin) SetCookies(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	for k := range params {
		http.SetCookie(w, &http.Cookie{
			Name:     k,
			Value:    params.Get(k),
			HttpOnly: true,
		})
	}
	w.Header().Set("Location", "/cookies")
	w.WriteHeader(http.StatusFound)
}

// DeleteCookies deletes cookies specified in query params and redirects to
// Cookies endpoint
func (h *HTTPBin) DeleteCookies(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	for k := range params {
		http.SetCookie(w, &http.Cookie{
			Name:     k,
			Value:    params.Get(k),
			HttpOnly: true,
			MaxAge:   -1,
			Expires:  time.Now().Add(-1 * 24 * 365 * time.Hour),
		})
	}
	w.Header().Set("Location", "/cookies")
	w.WriteHeader(http.StatusFound)
}

// BasicAuth requires basic authentication
func (h *HTTPBin) BasicAuth(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	expectedUser := parts[2]
	expectedPass := parts[3]

	givenUser, givenPass, _ := r.BasicAuth()

	status := http.StatusOK
	authorized := givenUser == expectedUser && givenPass == expectedPass
	if !authorized {
		status = http.StatusUnauthorized
	}

	body, _ := json.Marshal(&authResponse{
		Authorized: authorized,
		User:       givenUser,
	})
	writeJSON(w, body, status)
}
