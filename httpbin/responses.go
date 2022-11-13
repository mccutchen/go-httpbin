package httpbin

import (
	"net/http"
	"net/url"
)

const (
	jsonContentType = "application/json; encoding=utf-8"
	htmlContentType = "text/html; charset=utf-8"
)

type headersResponse struct {
	Headers http.Header `json:"headers"`
}

type ipResponse struct {
	Origin string `json:"origin"`
}

type userAgentResponse struct {
	UserAgent string `json:"user-agent"`
}

// A generic response for any incoming request that should not contain a body
// (GET, HEAD, OPTIONS, etc).
type noBodyResponse struct {
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`

	Deflated bool `json:"deflated,omitempty"`
	Gzipped  bool `json:"gzipped,omitempty"`
}

// A generic response for any incoming request that might contain a body (POST,
// PUT, PATCH, etc).
type bodyResponse struct {
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`

	Data  string              `json:"data"`
	Files map[string][]string `json:"files"`
	Form  map[string][]string `json:"form"`
	JSON  interface{}         `json:"json"`
}

type cookiesResponse map[string]string

type authResponse struct {
	Authorized bool   `json:"authorized"`
	User       string `json:"user"`
}

// An actual stream response body will be made up of one or more of these
// structs, encoded as JSON and separated by newlines
type streamResponse struct {
	ID      int         `json:"id"`
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`
}

type uuidResponse struct {
	UUID string `json:"uuid"`
}

type bearerResponse struct {
	Authenticated bool   `json:"authenticated"`
	Token         string `json:"token"`
}

type hostnameResponse struct {
	Hostname string `json:"hostname"`
}
