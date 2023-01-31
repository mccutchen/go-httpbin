package httpbin

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin/digest"
)

func notImplementedHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not implemented", http.StatusNotImplemented)
}

// Index renders an HTML index page
func (h *HTTPBin) Index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		msg := fmt.Sprintf("Not Found (go-httpbin does not handle the path %s)", r.URL.Path)
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
	writeHTML(w, mustStaticAsset("index.html"), http.StatusOK)
}

// FormsPost renders an HTML form that submits a request to the /post endpoint
func (h *HTTPBin) FormsPost(w http.ResponseWriter, r *http.Request) {
	writeHTML(w, mustStaticAsset("forms-post.html"), http.StatusOK)
}

// UTF8 renders an HTML encoding stress test
func (h *HTTPBin) UTF8(w http.ResponseWriter, r *http.Request) {
	writeHTML(w, mustStaticAsset("utf8.html"), http.StatusOK)
}

// Get handles HTTP GET requests
func (h *HTTPBin) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, &noBodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r),
		Origin:  getClientIP(r),
		URL:     getURL(r).String(),
	})
}

// Anything returns anything that is passed to request.
func (h *HTTPBin) Anything(w http.ResponseWriter, r *http.Request) {
	// Short-circuit for HEAD requests, which should be handled like regular
	// GET requests (where the autohead middleware will take care of discarding
	// the body)
	if r.Method == http.MethodHead {
		h.Get(w, r)
		return
	}
	// All other requests will be handled the same.  For compatibility with
	// httpbin, the /anything endpoint even allows GET requests to have bodies.
	h.RequestWithBody(w, r)
}

// RequestWithBody handles POST, PUT, and PATCH requests
func (h *HTTPBin) RequestWithBody(w http.ResponseWriter, r *http.Request) {
	resp := &bodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r),
		Origin:  getClientIP(r),
		URL:     getURL(r).String(),
	}

	err := parseBody(w, r, resp)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing request body: %s", err), http.StatusBadRequest)
		return
	}

	writeJSON(http.StatusOK, w, resp)
}

// Gzip returns a gzipped response
func (h *HTTPBin) Gzip(w http.ResponseWriter, r *http.Request) {
	var (
		buf bytes.Buffer
		gzw = gzip.NewWriter(&buf)
	)
	mustMarshalJSON(gzw, &noBodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r),
		Origin:  getClientIP(r),
		Gzipped: true,
	})
	gzw.Close()

	body := buf.Bytes()
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// Deflate returns a gzipped response
func (h *HTTPBin) Deflate(w http.ResponseWriter, r *http.Request) {
	var (
		buf bytes.Buffer
		zw  = zlib.NewWriter(&buf)
	)
	mustMarshalJSON(zw, &noBodyResponse{
		Args:     r.URL.Query(),
		Headers:  getRequestHeaders(r),
		Origin:   getClientIP(r),
		Deflated: true,
	})
	zw.Close()

	body := buf.Bytes()
	w.Header().Set("Content-Encoding", "deflate")
	w.Header().Set("Content-Type", jsonContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// IP echoes the IP address of the incoming request
func (h *HTTPBin) IP(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, &ipResponse{
		Origin: getClientIP(r),
	})
}

// UserAgent echoes the incoming User-Agent header
func (h *HTTPBin) UserAgent(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, &userAgentResponse{
		UserAgent: r.Header.Get("User-Agent"),
	})
}

// Headers echoes the incoming request headers
func (h *HTTPBin) Headers(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, &headersResponse{
		Headers: getRequestHeaders(r),
	})
}

type statusCase struct {
	headers map[string]string
	body    []byte
}

var (
	statusRedirectHeaders = &statusCase{
		headers: map[string]string{
			"Location": "/redirect/1",
		},
	}
	statusNotAcceptableBody = []byte(`{
  "message": "Client did not request a supported media type",
  "accept": [
    "image/webp",
    "image/svg+xml",
    "image/jpeg",
    "image/png",
    "image/"
  ]
}
`)
	statusHTTP300body = []byte(`<!doctype html>
<head>
<title>Multiple Choices</title>
</head>
<body>
<ul>
<li><a href="/image/jpeg">/image/jpeg</a></li>
<li><a href="/image/png">/image/png</a></li>
<li><a href="/image/svg">/image/svg</a></li>
</body>
</html>`)

	statusHTTP308Body = []byte(`<!doctype html>
<head>
<title>Permanent Redirect</title>
</head>
<body>Permanently redirected to <a href="/image/jpeg">/image/jpeg</a>
</body>
</html>`)

	statusSpecialCases = map[int]*statusCase{
		300: {
			body: statusHTTP300body,
			headers: map[string]string{
				"Location": "/image/jpeg",
			},
		},
		301: statusRedirectHeaders,
		302: statusRedirectHeaders,
		303: statusRedirectHeaders,
		305: statusRedirectHeaders,
		307: statusRedirectHeaders,
		308: {
			body: statusHTTP308Body,
			headers: map[string]string{
				"Location": "/image/jpeg",
			},
		},
		401: {
			headers: map[string]string{
				"WWW-Authenticate": `Basic realm="Fake Realm"`,
			},
		},
		402: {
			body: []byte("Fuck you, pay me!"),
			headers: map[string]string{
				"X-More-Info": "http://vimeo.com/22053820",
			},
		},
		406: {
			body: statusNotAcceptableBody,
			headers: map[string]string{
				"Content-Type": jsonContentType,
			},
		},
		407: {
			headers: map[string]string{
				"Proxy-Authenticate": `Basic realm="Fake Realm"`,
			},
		},
		418: {
			body: []byte("I'm a teapot!"),
			headers: map[string]string{
				"X-More-Info": "http://tools.ietf.org/html/rfc2324",
			},
		},
	}
)

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
		return
	}

	if specialCase, ok := statusSpecialCases[code]; ok {
		for key, val := range specialCase.headers {
			w.Header().Set(key, val)
		}
		w.WriteHeader(code)
		if specialCase.body != nil {
			w.Write(specialCase.body)
		}
		return
	}

	w.WriteHeader(code)
}

// Unstable - returns 500, sometimes
func (h *HTTPBin) Unstable(w http.ResponseWriter, r *http.Request) {
	var err error

	// rng/seed
	rng, err := parseSeed(r.URL.Query().Get("seed"))
	if err != nil {
		http.Error(w, "invalid seed", http.StatusBadRequest)
		return
	}

	// failure_rate
	var failureRate float64
	rawFailureRate := r.URL.Query().Get("failure_rate")
	if rawFailureRate != "" {
		failureRate, err = strconv.ParseFloat(rawFailureRate, 64)
		if err != nil || failureRate < 0 || failureRate > 1 {
			http.Error(w, "invalid failure_rate", http.StatusBadRequest)
			return
		}
	} else {
		failureRate = 0.5
	}

	var status int
	if rng.Float64() < failureRate {
		status = http.StatusInternalServerError
	} else {
		status = http.StatusOK
	}
	w.WriteHeader(status)
}

// ResponseHeaders responds with a map of header values
func (h *HTTPBin) ResponseHeaders(w http.ResponseWriter, r *http.Request) {
	args := r.URL.Query()
	for k, vs := range args {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if contentType := w.Header().Get("Content-Type"); contentType == "" {
		w.Header().Set("Content-Type", jsonContentType)
	}
	mustMarshalJSON(w, args)
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
		return
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

// RedirectTo responds with a redirect to a specific URL with an optional
// status code, which defaults to 302
func (h *HTTPBin) RedirectTo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	inputURL := q.Get("url")
	if inputURL == "" {
		http.Error(w, "Missing URL", http.StatusBadRequest)
		return
	}

	u, err := url.Parse(inputURL)
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}

	if u.IsAbs() && len(h.AllowedRedirectDomains) > 0 {
		if _, ok := h.AllowedRedirectDomains[u.Hostname()]; !ok {
			domainListItems := make([]string, 0, len(h.AllowedRedirectDomains))
			for domain := range h.AllowedRedirectDomains {
				domainListItems = append(domainListItems, fmt.Sprintf("- %s", domain))
			}
			sort.Strings(domainListItems)
			formattedDomains := strings.Join(domainListItems, "\n")
			msg := fmt.Sprintf(`Forbidden redirect URL. Please be careful with this link.

Allowed redirect destinations:
%s`, formattedDomains)
			http.Error(w, msg, http.StatusForbidden)
			return
		}
	}

	statusCode := http.StatusFound
	rawStatusCode := q.Get("status_code")
	if rawStatusCode != "" {
		statusCode, err = strconv.Atoi(q.Get("status_code"))
		if err != nil || statusCode < 300 || statusCode > 399 {
			http.Error(w, "Invalid status code", http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Location", u.String())
	w.WriteHeader(statusCode)
}

// Cookies responds with the cookies in the incoming request
func (h *HTTPBin) Cookies(w http.ResponseWriter, r *http.Request) {
	resp := cookiesResponse{}
	for _, c := range r.Cookies() {
		resp[c.Name] = c.Value
	}
	writeJSON(http.StatusOK, w, resp)
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
		w.Header().Set("WWW-Authenticate", `Basic realm="Fake Realm"`)
	}

	writeJSON(status, w, authResponse{
		Authorized: authorized,
		User:       givenUser,
	})
}

// HiddenBasicAuth requires HTTP Basic authentication but returns a status of
// 404 if the request is unauthorized
func (h *HTTPBin) HiddenBasicAuth(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	expectedUser := parts[2]
	expectedPass := parts[3]

	givenUser, givenPass, _ := r.BasicAuth()

	authorized := givenUser == expectedUser && givenPass == expectedPass
	if !authorized {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	writeJSON(http.StatusOK, w, authResponse{
		Authorized: authorized,
		User:       givenUser,
	})
}

// Stream responds with max(n, 100) lines of JSON-encoded request data.
func (h *HTTPBin) Stream(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, "Invalid integer", http.StatusBadRequest)
		return
	}

	if n > 100 {
		n = 100
	} else if n < 1 {
		n = 1
	}

	resp := &streamResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r),
		Origin:  getClientIP(r),
		URL:     getURL(r).String(),
	}

	f := w.(http.Flusher)
	for i := 0; i < n; i++ {
		resp.ID = i
		// Call json.Marshal directly to avoid pretty printing
		line, _ := json.Marshal(resp)
		w.Write(append(line, '\n'))
		f.Flush()
	}
}

// Delay waits for a given amount of time before responding, where the time may
// be specified as a golang-style duration or seconds in floating point.
func (h *HTTPBin) Delay(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	delay, err := parseBoundedDuration(parts[2], 0, h.MaxDuration)
	if err != nil {
		http.Error(w, "Invalid duration", http.StatusBadRequest)
		return
	}

	select {
	case <-r.Context().Done():
		w.WriteHeader(499) // "Client Closed Request" https://httpstatuses.com/499
		return
	case <-time.After(delay):
	}
	h.RequestWithBody(w, r)
}

// Drip returns data over a duration after an optional initial delay, then
// (optionally) returns with the given status code.
func (h *HTTPBin) Drip(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var (
		duration = h.DefaultParams.DripDuration
		delay    = h.DefaultParams.DripDelay
		numBytes = h.DefaultParams.DripNumBytes
		code     = http.StatusOK

		err error
	)

	if userDuration := q.Get("duration"); userDuration != "" {
		duration, err = parseBoundedDuration(userDuration, 0, h.MaxDuration)
		if err != nil {
			http.Error(w, "Invalid duration", http.StatusBadRequest)
			return
		}
	}

	if userDelay := q.Get("delay"); userDelay != "" {
		delay, err = parseBoundedDuration(userDelay, 0, h.MaxDuration)
		if err != nil {
			http.Error(w, "Invalid delay", http.StatusBadRequest)
			return
		}
	}

	if userNumBytes := q.Get("numbytes"); userNumBytes != "" {
		numBytes, err = strconv.ParseInt(userNumBytes, 10, 64)
		if err != nil || numBytes <= 0 || numBytes > h.MaxBodySize {
			http.Error(w, "Invalid numbytes", http.StatusBadRequest)
			return
		}
	}

	if userCode := q.Get("code"); userCode != "" {
		code, err = strconv.Atoi(userCode)
		if err != nil || code < 100 || code >= 600 {
			http.Error(w, "Invalid code", http.StatusBadRequest)
			return
		}
	}

	if duration+delay > h.MaxDuration {
		http.Error(w, "Too much time", http.StatusBadRequest)
		return
	}

	pause := duration / time.Duration(numBytes)
	flusher := w.(http.Flusher)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", numBytes))
	w.WriteHeader(code)
	flusher.Flush()

	select {
	case <-r.Context().Done():
		return
	case <-time.After(delay):
	}

	b := []byte{'*'}
	for i := int64(0); i < numBytes; i++ {
		w.Write(b)
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-time.After(pause):
		}
	}
}

// Range returns up to N bytes, with support for HTTP Range requests.
//
// This departs from httpbin by not supporting the chunk_size or duration
// parameters.
func (h *HTTPBin) Range(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	numBytes, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Add("ETag", fmt.Sprintf("range%d", numBytes))
	w.Header().Add("Accept-Ranges", "bytes")

	if numBytes <= 0 || numBytes > h.MaxBodySize {
		http.Error(w, "Invalid number of bytes", http.StatusBadRequest)
		return
	}

	content := newSyntheticByteStream(numBytes, func(offset int64) byte {
		return byte(97 + (offset % 26))
	})
	var modtime time.Time
	http.ServeContent(w, r, "", modtime, content)
}

// HTML renders a basic HTML page
func (h *HTTPBin) HTML(w http.ResponseWriter, r *http.Request) {
	writeHTML(w, mustStaticAsset("moby.html"), http.StatusOK)
}

// Robots renders a basic robots.txt file
func (h *HTTPBin) Robots(w http.ResponseWriter, r *http.Request) {
	robotsTxt := []byte(`User-agent: *
Disallow: /deny
`)
	writeResponse(w, http.StatusOK, "text/plain", robotsTxt)
}

// Deny renders a basic page that robots should never access
func (h *HTTPBin) Deny(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, "text/plain", []byte(`YOU SHOULDN'T BE HERE`))
}

// Cache returns a 304 if an If-Modified-Since or an If-None-Match header is
// present, otherwise returns the same response as Get.
func (h *HTTPBin) Cache(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("If-Modified-Since") != "" || r.Header.Get("If-None-Match") != "" {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	lastModified := time.Now().Format(time.RFC1123)
	w.Header().Add("Last-Modified", lastModified)
	w.Header().Add("ETag", sha1hash(lastModified))
	h.Get(w, r)
}

// CacheControl sets a Cache-Control header for N seconds for /cache/N requests
func (h *HTTPBin) CacheControl(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	seconds, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Add("Cache-Control", fmt.Sprintf("public, max-age=%d", seconds))
	h.Get(w, r)
}

// ETag assumes the resource has the given etag and responds to If-None-Match
// and If-Match headers appropriately.
func (h *HTTPBin) ETag(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	etag := parts[2]
	w.Header().Set("ETag", fmt.Sprintf(`"%s"`, etag))

	var buf bytes.Buffer
	mustMarshalJSON(&buf, noBodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r),
		Origin:  getClientIP(r),
		URL:     getURL(r).String(),
	})

	// Let http.ServeContent deal with If-None-Match and If-Match headers:
	// https://golang.org/pkg/net/http/#ServeContent
	http.ServeContent(w, r, "response.json", time.Now(), bytes.NewReader(buf.Bytes()))
}

// Bytes returns N random bytes generated with an optional seed
func (h *HTTPBin) Bytes(w http.ResponseWriter, r *http.Request) {
	handleBytes(w, r, false)
}

// StreamBytes streams N random bytes generated with an optional seed in chunks
// of a given size.
func (h *HTTPBin) StreamBytes(w http.ResponseWriter, r *http.Request) {
	handleBytes(w, r, true)
}

// handleBytes consolidates the logic for validating input params of the Bytes
// and StreamBytes endpoints and knows how to write the response in chunks if
// streaming is true.
func handleBytes(w http.ResponseWriter, r *http.Request, streaming bool) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	numBytes, err := strconv.Atoi(parts[2])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if numBytes < 1 {
		numBytes = 1
	} else if numBytes > 100*1024 {
		numBytes = 100 * 1024
	}

	var chunkSize int
	var write func([]byte)

	if streaming {
		if r.URL.Query().Get("chunk_size") != "" {
			chunkSize, err = strconv.Atoi(r.URL.Query().Get("chunk_size"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		} else {
			chunkSize = 10 * 1024
		}

		write = func() func(chunk []byte) {
			f := w.(http.Flusher)
			return func(chunk []byte) {
				w.Write(chunk)
				f.Flush()
			}
		}()
	} else {
		chunkSize = numBytes
		write = func(chunk []byte) {
			w.Header().Set("Content-Length", strconv.Itoa(len(chunk)))
			w.Write(chunk)
		}
	}

	// rng/seed
	rng, err := parseSeed(r.URL.Query().Get("seed"))
	if err != nil {
		http.Error(w, "invalid seed", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)

	var chunk []byte
	for i := 0; i < numBytes; i++ {
		chunk = append(chunk, byte(rng.Intn(256)))
		if len(chunk) == chunkSize {
			write(chunk)
			chunk = nil
		}
	}
	if len(chunk) > 0 {
		write(chunk)
	}
}

// Links redirects to the first page in a series of N links
func (h *HTTPBin) Links(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 && len(parts) != 4 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	n, err := strconv.Atoi(parts[2])
	if err != nil || n < 0 || n > 256 {
		http.Error(w, "Invalid link count", http.StatusBadRequest)
		return
	}

	// Are we handling /links/<n>/<offset>? If so, render an HTML page
	if len(parts) == 4 {
		offset, err := strconv.Atoi(parts[3])
		if err != nil {
			http.Error(w, "Invalid offset", http.StatusBadRequest)
		}
		doLinksPage(w, r, n, offset)
		return
	}

	// Otherwise, redirect from /links/<n> to /links/<n>/0
	r.URL.Path = r.URL.Path + "/0"
	w.Header().Set("Location", r.URL.String())
	w.WriteHeader(http.StatusFound)
}

// doLinksPage renders a page with a series of N links
func doLinksPage(w http.ResponseWriter, r *http.Request, n int, offset int) {
	w.Header().Add("Content-Type", htmlContentType)
	w.WriteHeader(http.StatusOK)

	w.Write([]byte("<html><head><title>Links</title></head><body>"))
	for i := 0; i < n; i++ {
		if i == offset {
			fmt.Fprintf(w, "%d ", i)
		} else {
			fmt.Fprintf(w, `<a href="/links/%d/%d">%d</a> `, n, i, i)
		}
	}
	w.Write([]byte("</body></html>"))
}

// ImageAccept responds with an appropriate image based on the Accept header
func (h *HTTPBin) ImageAccept(w http.ResponseWriter, r *http.Request) {
	accept := r.Header.Get("Accept")
	if accept == "" || strings.Contains(accept, "image/png") || strings.Contains(accept, "image/*") {
		doImage(w, "png")
	} else if strings.Contains(accept, "image/webp") {
		doImage(w, "webp")
	} else if strings.Contains(accept, "image/svg+xml") {
		doImage(w, "svg")
	} else if strings.Contains(accept, "image/jpeg") {
		doImage(w, "jpeg")
	} else {
		http.Error(w, "Unsupported media type", http.StatusUnsupportedMediaType)
	}
}

// Image responds with an image of a specific kind, from /image/<kind>
func (h *HTTPBin) Image(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	doImage(w, parts[2])
}

// doImage responds with a specific kind of image, if there is an image asset
// of the given kind.
func doImage(w http.ResponseWriter, kind string) {
	img, err := staticAsset("image." + kind)
	if err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
	}
	contentType := "image/" + kind
	if kind == "svg" {
		contentType = "image/svg+xml"
	}
	writeResponse(w, http.StatusOK, contentType, img)
}

// XML responds with an XML document
func (h *HTTPBin) XML(w http.ResponseWriter, r *http.Request) {
	writeResponse(w, http.StatusOK, "application/xml", mustStaticAsset("sample.xml"))
}

// DigestAuth handles a simple implementation of HTTP Digest Authentication,
// which supports the "auth" QOP and the MD5 and SHA-256 crypto algorithms.
//
// /digest-auth/<qop>/<user>/<passwd>
// /digest-auth/<qop>/<user>/<passwd>/<algorithm>
func (h *HTTPBin) DigestAuth(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	count := len(parts)

	if count != 5 && count != 6 {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}

	qop := strings.ToLower(parts[2])
	user := parts[3]
	password := parts[4]

	algoName := "MD5"
	if count == 6 {
		algoName = strings.ToUpper(parts[5])
	}

	if qop != "auth" {
		http.Error(w, "Invalid QOP directive", http.StatusBadRequest)
		return
	}
	if algoName != "MD5" && algoName != "SHA-256" {
		http.Error(w, "Invalid algorithm", http.StatusBadRequest)
		return
	}

	algorithm := digest.MD5
	if algoName == "SHA-256" {
		algorithm = digest.SHA256
	}

	if !digest.Check(r, user, password) {
		w.Header().Set("WWW-Authenticate", digest.Challenge("go-httpbin", algorithm))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	writeJSON(http.StatusOK, w, authResponse{
		Authorized: true,
		User:       user,
	})
}

// UUID - responds with a generated UUID
func (h *HTTPBin) UUID(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, uuidResponse{
		UUID: uuidv4(),
	})
}

// Base64 - encodes/decodes input data
func (h *HTTPBin) Base64(w http.ResponseWriter, r *http.Request) {
	b, err := newBase64Helper(r.URL.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("%s", err), http.StatusBadRequest)
		return
	}

	var result []byte
	var base64Error error

	if b.operation == "decode" {
		result, base64Error = b.Decode()
	} else {
		result, base64Error = b.Encode()
	}

	if base64Error != nil {
		http.Error(w, fmt.Sprintf("%s failed: %s", b.operation, base64Error), http.StatusBadRequest)
		return
	}
	writeResponse(w, http.StatusOK, "text/plain", result)
}

// DumpRequest - returns the given request in its HTTP/1.x wire representation.
// The returned representation is an approximation only;
// some details of the initial request are lost while parsing it into
// an http.Request. In particular, the order and case of header field
// names are lost.
func (h *HTTPBin) DumpRequest(w http.ResponseWriter, r *http.Request) {
	dump, err := httputil.DumpRequest(r, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(dump)
}

// JSON - returns a sample json
func (h *HTTPBin) JSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(http.StatusOK)
	w.Write(mustStaticAsset("sample.json"))
}

// Bearer - Prompts the user for authorization using bearer authentication.
func (h *HTTPBin) Bearer(w http.ResponseWriter, r *http.Request) {
	reqToken := r.Header.Get("Authorization")
	tokenFields := strings.Fields(reqToken)
	if len(tokenFields) != 2 || tokenFields[0] != "Bearer" {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	writeJSON(http.StatusOK, w, bearerResponse{
		Authenticated: true,
		Token:         tokenFields[1],
	})
}

// Hostname - returns the hostname.
func (h *HTTPBin) Hostname(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, hostnameResponse{
		Hostname: h.hostname,
	})
}
