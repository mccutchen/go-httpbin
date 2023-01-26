package httpbin

import (
	"net/http"
	"time"
)

// Default configuration values
const (
	DefaultMaxBodySize int64 = 1024 * 1024
	DefaultMaxDuration       = 10 * time.Second
	DefaultHostname          = "go-httpbin"
)

// DefaultParams defines default parameter values
type DefaultParams struct {
	DripDuration time.Duration
	DripDelay    time.Duration
	DripNumBytes int64
}

// DefaultDefaultParams defines the DefaultParams that are used by default. In
// general, these should match the original httpbin.org's defaults.
var DefaultDefaultParams = DefaultParams{
	DripDuration: 2 * time.Second,
	DripDelay:    2 * time.Second,
	DripNumBytes: 10,
}

// HTTPBin contains the business logic
type HTTPBin struct {
	// Max size of an incoming request generated response body, in bytes
	MaxBodySize int64

	// Max duration of a request, for those requests that allow user control
	// over timing (e.g. /delay)
	MaxDuration time.Duration

	// Observer called with the result of each handled request
	Observer Observer

	// Default parameter values
	DefaultParams DefaultParams

	// Set of hosts to which the /redirect-to endpoint will allow redirects
	AllowedRedirectDomains map[string]struct{}

	// The hostname to expose via /hostname.
	hostname string

	// The app's http handler
	handler http.Handler
}

// New creates a new HTTPBin instance
func New(opts ...OptionFunc) *HTTPBin {
	h := &HTTPBin{
		MaxBodySize:   DefaultMaxBodySize,
		MaxDuration:   DefaultMaxDuration,
		DefaultParams: DefaultDefaultParams,
		hostname:      DefaultHostname,
	}
	for _, opt := range opts {
		opt(h)
	}
	h.handler = h.Handler()
	return h
}

// ServeHTTP implememnts the http.Handler interface.
func (h *HTTPBin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

// Assert that HTTPBin implements http.Handler interface
var _ http.Handler = &HTTPBin{}

// Handler returns an http.Handler that exposes all HTTPBin endpoints
func (h *HTTPBin) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", methods(h.Index, "GET"))
	mux.HandleFunc("/forms/post", methods(h.FormsPost, "GET"))
	mux.HandleFunc("/encoding/utf8", methods(h.UTF8, "GET"))

	mux.HandleFunc("/delete", methods(h.RequestWithBody, "DELETE"))
	mux.HandleFunc("/get", methods(h.Get, "GET"))
	mux.HandleFunc("/head", methods(h.Get, "HEAD"))
	mux.HandleFunc("/patch", methods(h.RequestWithBody, "PATCH"))
	mux.HandleFunc("/post", methods(h.RequestWithBody, "POST"))
	mux.HandleFunc("/put", methods(h.RequestWithBody, "PUT"))

	mux.HandleFunc("/anything", h.Anything)
	mux.HandleFunc("/anything/", h.Anything)

	mux.HandleFunc("/ip", h.IP)
	mux.HandleFunc("/user-agent", h.UserAgent)
	mux.HandleFunc("/headers", h.Headers)
	mux.HandleFunc("/response-headers", h.ResponseHeaders)
	mux.HandleFunc("/hostname", h.Hostname)

	mux.HandleFunc("/status/", h.Status)
	mux.HandleFunc("/unstable", h.Unstable)

	mux.HandleFunc("/redirect/", h.Redirect)
	mux.HandleFunc("/relative-redirect/", h.RelativeRedirect)
	mux.HandleFunc("/absolute-redirect/", h.AbsoluteRedirect)
	mux.HandleFunc("/redirect-to", h.RedirectTo)

	mux.HandleFunc("/cookies", h.Cookies)
	mux.HandleFunc("/cookies/set", h.SetCookies)
	mux.HandleFunc("/cookies/delete", h.DeleteCookies)

	mux.HandleFunc("/basic-auth/", h.BasicAuth)
	mux.HandleFunc("/hidden-basic-auth/", h.HiddenBasicAuth)
	mux.HandleFunc("/digest-auth/", h.DigestAuth)
	mux.HandleFunc("/bearer", h.Bearer)

	mux.HandleFunc("/deflate", h.Deflate)
	mux.HandleFunc("/gzip", h.Gzip)

	mux.HandleFunc("/stream/", h.Stream)
	mux.HandleFunc("/delay/", h.Delay)
	mux.HandleFunc("/drip", h.Drip)

	mux.HandleFunc("/range/", h.Range)
	mux.HandleFunc("/bytes/", h.Bytes)
	mux.HandleFunc("/stream-bytes/", h.StreamBytes)

	mux.HandleFunc("/html", h.HTML)
	mux.HandleFunc("/robots.txt", h.Robots)
	mux.HandleFunc("/deny", h.Deny)

	mux.HandleFunc("/cache", h.Cache)
	mux.HandleFunc("/cache/", h.CacheControl)
	mux.HandleFunc("/etag/", h.ETag)

	mux.HandleFunc("/links/", h.Links)

	mux.HandleFunc("/image", h.ImageAccept)
	mux.HandleFunc("/image/", h.Image)
	mux.HandleFunc("/xml", h.XML)
	mux.HandleFunc("/json", h.JSON)

	mux.HandleFunc("/uuid", h.UUID)
	mux.HandleFunc("/base64/", h.Base64)

	mux.HandleFunc("/dump/request", h.DumpRequest)

	// existing httpbin endpoints that we do not support
	mux.HandleFunc("/brotli", notImplementedHandler)

	// Make sure our ServeMux doesn't "helpfully" redirect these invalid
	// endpoints by adding a trailing slash. See the ServeMux docs for more
	// info: https://golang.org/pkg/net/http/#ServeMux
	mux.HandleFunc("/absolute-redirect", http.NotFound)
	mux.HandleFunc("/basic-auth", http.NotFound)
	mux.HandleFunc("/delay", http.NotFound)
	mux.HandleFunc("/digest-auth", http.NotFound)
	mux.HandleFunc("/hidden-basic-auth", http.NotFound)
	mux.HandleFunc("/redirect", http.NotFound)
	mux.HandleFunc("/relative-redirect", http.NotFound)
	mux.HandleFunc("/status", http.NotFound)
	mux.HandleFunc("/stream", http.NotFound)
	mux.HandleFunc("/bytes", http.NotFound)
	mux.HandleFunc("/stream-bytes", http.NotFound)
	mux.HandleFunc("/links", http.NotFound)

	// Apply global middleware
	var handler http.Handler
	handler = mux
	handler = limitRequestSize(h.MaxBodySize, handler)
	handler = preflight(handler)
	handler = autohead(handler)
	if h.Observer != nil {
		handler = observe(h.Observer, handler)
	}

	return handler
}
