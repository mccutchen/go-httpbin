package httpbin

import (
	"net/http"
	"net/url"
)

const jsonContentType = "application/json; encoding=utf-8"

type headersResponse struct {
	Headers http.Header `json:"headers"`
}

type ipResponse struct {
	Origin string `json:"origin"`
}

type userAgentResponse struct {
	UserAgent string `json:"user-agent"`
}

type getResponse struct {
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`
}

// A generic response for any incoming request that might contain a body
type bodyResponse struct {
	Args    url.Values  `json:"args"`
	Headers http.Header `json:"headers"`
	Origin  string      `json:"origin"`
	URL     string      `json:"url"`

	Data  []byte              `json:"data"`
	Files map[string][]string `json:"files"`
	Form  map[string][]string `json:"form"`
	JSON  interface{}         `json:"json"`
}

type cookiesResponse map[string]string

type authResponse struct {
	Authorized bool   `json:"authorized"`
	User       string `json:"user"`
}

// Options are used to configure HTTPBin
type Options struct {
	MaxMemory int64
}

// HTTPBin contains the business logic
type HTTPBin struct {
	options *Options
}

// Handler returns an http.Handler that exposes all HTTPBin endpoints
func (h *HTTPBin) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", methods(h.Index, "GET"))
	mux.HandleFunc("/forms/post", methods(h.FormsPost, "GET"))
	mux.HandleFunc("/encoding/utf8", methods(h.UTF8, "GET"))

	mux.HandleFunc("/get", methods(h.Get, "GET"))
	mux.HandleFunc("/post", methods(h.RequestWithBody, "POST"))
	mux.HandleFunc("/put", methods(h.RequestWithBody, "PUT"))
	mux.HandleFunc("/patch", methods(h.RequestWithBody, "PATCH"))
	mux.HandleFunc("/delete", methods(h.RequestWithBody, "DELETE"))

	mux.HandleFunc("/ip", h.IP)
	mux.HandleFunc("/user-agent", h.UserAgent)
	mux.HandleFunc("/headers", h.Headers)
	mux.HandleFunc("/response-headers", h.ResponseHeaders)

	mux.HandleFunc("/status/", h.Status)
	mux.HandleFunc("/redirect/", h.Redirect)
	mux.HandleFunc("/relative-redirect/", h.RelativeRedirect)
	mux.HandleFunc("/absolute-redirect/", h.AbsoluteRedirect)

	mux.HandleFunc("/cookies", h.Cookies)
	mux.HandleFunc("/cookies/set", h.SetCookies)
	mux.HandleFunc("/cookies/delete", h.DeleteCookies)

	mux.HandleFunc("/basic-auth/", h.BasicAuth)
	mux.HandleFunc("/hidden-basic-auth/", h.HiddenBasicAuth)

	// Make sure our ServeMux doesn't "helpfully" redirect these invalid
	// endpoints by adding a trailing slash. See the ServeMux docs for more
	// info: https://golang.org/pkg/net/http/#ServeMux
	mux.HandleFunc("/absolute-redirect", http.NotFound)
	mux.HandleFunc("/basic-auth", http.NotFound)
	mux.HandleFunc("/hidden-basic-auth", http.NotFound)
	mux.HandleFunc("/redirect", http.NotFound)
	mux.HandleFunc("/relative-redirect", http.NotFound)
	mux.HandleFunc("/status", http.NotFound)

	return logger(cors(mux))
}

// NewHTTPBin creates a new HTTPBin
func NewHTTPBin(options *Options) *HTTPBin {
	if options == nil {
		options = &Options{}
	}
	return &HTTPBin{
		options: options,
	}
}
