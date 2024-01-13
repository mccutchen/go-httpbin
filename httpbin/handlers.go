package httpbin

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin/digest"
	"github.com/mccutchen/go-httpbin/v2/httpbin/websocket"
)

var nilValues = url.Values{}

func notImplementedHandler(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotImplemented, nil)
}

// Index renders an HTML index page
func (h *HTTPBin) Index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
	writeHTML(w, h.indexHTML, http.StatusOK)
}

// FormsPost renders an HTML form that submits a request to the /post endpoint
func (h *HTTPBin) FormsPost(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, h.formsPostHTML, http.StatusOK)
}

// UTF8 renders an HTML encoding stress test
func (h *HTTPBin) UTF8(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, mustStaticAsset("utf8.html"), http.StatusOK)
}

// Get handles HTTP GET requests
func (h *HTTPBin) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(http.StatusOK, w, &noBodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
		Method:  r.Method,
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
		Files:   nilValues,
		Form:    nilValues,
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
		Method:  r.Method,
		Origin:  getClientIP(r),
		URL:     getURL(r).String(),
	}

	if err := parseBody(r, resp); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("error parsing request body: %w", err))
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
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
		Method:  r.Method,
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
		Headers:  getRequestHeaders(r, h.excludeHeadersProcessor),
		Method:   r.Method,
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
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
	})
}

type statusCase struct {
	headers map[string]string
	body    []byte
}

func createSpecialCases(prefix string) map[int]*statusCase {
	statusRedirectHeaders := &statusCase{
		headers: map[string]string{
			"Location": prefix + "/redirect/1",
		},
	}
	statusNotAcceptableBody := []byte(`{
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
	statusHTTP300body := []byte(fmt.Sprintf(`<!doctype html>
<head>
<title>Multiple Choices</title>
</head>
<body>
<ul>
<li><a href="%[1]s/image/jpeg">/image/jpeg</a></li>
<li><a href="%[1]s/image/png">/image/png</a></li>
<li><a href="%[1]s/image/svg">/image/svg</a></li>
</body>
</html>`, prefix))

	statusHTTP308Body := []byte(fmt.Sprintf(`<!doctype html>
<head>
<title>Permanent Redirect</title>
</head>
<body>Permanently redirected to <a href="%[1]s/image/jpeg">%[1]s/image/jpeg</a>
</body>
</html>`, prefix))

	return map[int]*statusCase{
		300: {
			body: statusHTTP300body,
			headers: map[string]string{
				"Location": prefix + "/image/jpeg",
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
				"Location": prefix + "/image/jpeg",
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
}

// Status responds with the specified status code. TODO: support random choice
// from multiple, optionally weighted status codes.
func (h *HTTPBin) Status(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	rawStatus := parts[2]

	// simple case, specific status code is requested
	if !strings.Contains(rawStatus, ",") {
		code, err := parseStatusCode(parts[2])
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		h.doStatus(w, code)
		return
	}

	// complex case, make a weighted choice from multiple status codes
	choices, err := parseWeightedChoices(rawStatus, strconv.Atoi)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	choice := weightedRandomChoice(choices)
	h.doStatus(w, choice)
}

func (h *HTTPBin) doStatus(w http.ResponseWriter, code int) {
	// default to plain text content type, which may be overriden by headers
	// for special cases
	w.Header().Set("Content-Type", textContentType)
	if specialCase, ok := h.statusSpecialCases[code]; ok {
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
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid seed: %w", err))
		return
	}

	// failure_rate
	failureRate := 0.5
	if rawFailureRate := r.URL.Query().Get("failure_rate"); rawFailureRate != "" {
		failureRate, err = strconv.ParseFloat(rawFailureRate, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid failure rate: %w", err))
			return
		} else if failureRate < 0 || failureRate > 1 {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid failure rate: %d not in range [0, 1]", err))
			return
		}
	}

	status := http.StatusOK
	if rng.Float64() < failureRate {
		status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", textContentType)
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

func (h *HTTPBin) redirectLocation(r *http.Request, relative bool, n int) string {
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

func (h *HTTPBin) handleRedirect(w http.ResponseWriter, r *http.Request, relative bool) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid redirect count: %w", err))
		return
	} else if n < 1 {
		writeError(w, http.StatusBadRequest, errors.New("redirect count must be > 0"))
		return
	}

	h.doRedirect(w, h.redirectLocation(r, relative, n-1), http.StatusFound)
}

// Redirect responds with 302 redirect a given number of times. Defaults to a
// relative redirect, but an ?absolute=true query param will trigger an
// absolute redirect.
func (h *HTTPBin) Redirect(w http.ResponseWriter, r *http.Request) {
	params := r.URL.Query()
	relative := strings.ToLower(params.Get("absolute")) != "true"
	h.handleRedirect(w, r, relative)
}

// RelativeRedirect responds with an HTTP 302 redirect a given number of times
func (h *HTTPBin) RelativeRedirect(w http.ResponseWriter, r *http.Request) {
	h.handleRedirect(w, r, true)
}

// AbsoluteRedirect responds with an HTTP 302 redirect a given number of times
func (h *HTTPBin) AbsoluteRedirect(w http.ResponseWriter, r *http.Request) {
	h.handleRedirect(w, r, false)
}

// RedirectTo responds with a redirect to a specific URL with an optional
// status code, which defaults to 302
func (h *HTTPBin) RedirectTo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	inputURL := q.Get("url")
	if inputURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing required query parameter: url"))
		return
	}

	u, err := url.Parse(inputURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid url: %w", err))
		return
	}

	if u.IsAbs() && len(h.AllowedRedirectDomains) > 0 {
		if _, ok := h.AllowedRedirectDomains[u.Hostname()]; !ok {
			// for this error message we do not use our standard JSON response
			// because we want it to be more obviously human readable.
			writeResponse(w, http.StatusForbidden, "text/plain", []byte(h.forbiddenRedirectError))
			return
		}
	}

	statusCode := http.StatusFound
	if userStatusCode := q.Get("status_code"); userStatusCode != "" {
		statusCode, err = parseBoundedStatusCode(userStatusCode, 300, 399)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	h.doRedirect(w, u.String(), statusCode)
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
	h.doRedirect(w, "/cookies", http.StatusFound)
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
	h.doRedirect(w, "/cookies", http.StatusFound)
}

// BasicAuth requires basic authentication
func (h *HTTPBin) BasicAuth(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 4 {
		writeError(w, http.StatusNotFound, nil)
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
		writeError(w, http.StatusNotFound, nil)
		return
	}
	expectedUser := parts[2]
	expectedPass := parts[3]

	givenUser, givenPass, _ := r.BasicAuth()

	authorized := givenUser == expectedUser && givenPass == expectedPass
	if !authorized {
		writeError(w, http.StatusNotFound, nil)
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
		writeError(w, http.StatusNotFound, nil)
		return
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid count: %w", err))
		return
	}

	if n > 100 {
		n = 100
	} else if n < 1 {
		n = 1
	}

	resp := &streamResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	delay, err := parseBoundedDuration(parts[2], 0, h.MaxDuration)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid duration: %w", err))
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
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid duration: %w", err))
			return
		}
	}

	if userDelay := q.Get("delay"); userDelay != "" {
		delay, err = parseBoundedDuration(userDelay, 0, h.MaxDuration)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid delay: %w", err))
			return
		}
	}

	if userNumBytes := q.Get("numbytes"); userNumBytes != "" {
		numBytes, err = strconv.ParseInt(userNumBytes, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid numbytes: %w", err))
			return
		} else if numBytes < 1 || numBytes > h.MaxBodySize {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid numbytes: %d not in range [1, %d]", numBytes, h.MaxBodySize))
			return
		}
	}

	if userCode := q.Get("code"); userCode != "" {
		code, err = parseStatusCode(userCode)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	if duration+delay > h.MaxDuration {
		http.Error(w, "Too much time", http.StatusBadRequest)
		return
	}

	pause := duration
	if numBytes > 1 {
		// compensate for lack of pause after final write (i.e. if we're
		// writing 10 bytes, we will only pause 9 times)
		pause = duration / time.Duration(numBytes-1)
	}

	// Initial delay before we send any response data
	if delay > 0 {
		select {
		case <-time.After(delay):
			// ok
		case <-r.Context().Done():
			w.WriteHeader(499) // "Client Closed Request" https://httpstatuses.com/499
			return
		}
	}

	w.Header().Set("Content-Type", binaryContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", numBytes))
	w.WriteHeader(code)

	// special case when we do not need to pause between each write
	if pause == 0 {
		for i := int64(0); i < numBytes; i++ {
			w.Write([]byte{'*'})
		}
		return
	}

	// otherwise, write response body byte-by-byte
	ticker := time.NewTicker(pause)
	defer ticker.Stop()

	b := []byte{'*'}
	flusher := w.(http.Flusher)
	for i := int64(0); i < numBytes; i++ {
		w.Write(b)
		flusher.Flush()

		// don't pause after last byte
		if i == numBytes-1 {
			return
		}

		select {
		case <-ticker.C:
			// ok
		case <-r.Context().Done():
			return
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	numBytes, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid count: %w", err))
		return
	}

	w.Header().Add("ETag", fmt.Sprintf("range%d", numBytes))
	w.Header().Add("Accept-Ranges", "bytes")

	if numBytes <= 0 || numBytes > h.MaxBodySize {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid count: %d not in range [1, %d]", numBytes, h.MaxBodySize))
		return
	}

	content := newSyntheticByteStream(numBytes, func(offset int64) byte {
		return byte(97 + (offset % 26))
	})
	var modtime time.Time
	http.ServeContent(w, r, "", modtime, content)
}

// HTML renders a basic HTML page
func (h *HTTPBin) HTML(w http.ResponseWriter, _ *http.Request) {
	writeHTML(w, mustStaticAsset("moby.html"), http.StatusOK)
}

// Robots renders a basic robots.txt file
func (h *HTTPBin) Robots(w http.ResponseWriter, _ *http.Request) {
	robotsTxt := []byte(`User-agent: *
Disallow: /deny
`)
	writeResponse(w, http.StatusOK, textContentType, robotsTxt)
}

// Deny renders a basic page that robots should never access
func (h *HTTPBin) Deny(w http.ResponseWriter, _ *http.Request) {
	writeResponse(w, http.StatusOK, textContentType, []byte(`YOU SHOULDN'T BE HERE`))
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	seconds, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid seconds: %w", err))
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	etag := parts[2]
	w.Header().Set("ETag", fmt.Sprintf(`"%s"`, etag))
	w.Header().Set("Content-Type", textContentType)

	var buf bytes.Buffer
	mustMarshalJSON(&buf, noBodyResponse{
		Args:    r.URL.Query(),
		Headers: getRequestHeaders(r, h.excludeHeadersProcessor),
		Method:  r.Method,
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	numBytes, err := strconv.Atoi(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid byte count: %w", err))
		return
	}

	// rng/seed
	rng, err := parseSeed(r.URL.Query().Get("seed"))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid seed: %w", err))
		return
	}

	if numBytes < 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid byte count: %d must be greater than 0", numBytes))
		return
	}

	// Special case 0 bytes and exit early, since streaming & chunk size do not
	// matter here.
	if numBytes == 0 {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
		return
	}

	if numBytes > 100*1024 {
		numBytes = 100 * 1024
	}

	var chunkSize int
	var write func([]byte)

	if streaming {
		if r.URL.Query().Get("chunk_size") != "" {
			chunkSize, err = strconv.Atoi(r.URL.Query().Get("chunk_size"))
			if err != nil {
				writeError(w, http.StatusBadRequest, fmt.Errorf("invalid chunk_size: %w", err))
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
		// if not streaming, we will write the whole response at once
		chunkSize = numBytes
		w.Header().Set("Content-Length", strconv.Itoa(numBytes))
		write = func(chunk []byte) {
			w.Write(chunk)
		}
	}

	w.Header().Set("Content-Type", binaryContentType)
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
		writeError(w, http.StatusNotFound, nil)
		return
	}

	n, err := strconv.Atoi(parts[2])
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid link count: %w", err))
		return
	} else if n < 0 || n > 256 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid link count: %d must be in range [0, 256]", n))
		return
	}

	// Are we handling /links/<n>/<offset>? If so, render an HTML page
	if len(parts) == 4 {
		offset, err := strconv.Atoi(parts[3])
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid offset: %w", err))
			return
		}
		h.doLinksPage(w, r, n, offset)
		return
	}

	// Otherwise, redirect from /links/<n> to /links/<n>/0
	r.URL.Path = r.URL.Path + "/0"
	h.doRedirect(w, r.URL.String(), http.StatusFound)
}

// doLinksPage renders a page with a series of N links
func (h *HTTPBin) doLinksPage(w http.ResponseWriter, _ *http.Request, n int, offset int) {
	w.Header().Add("Content-Type", htmlContentType)
	w.WriteHeader(http.StatusOK)

	w.Write([]byte("<html><head><title>Links</title></head><body>"))
	for i := 0; i < n; i++ {
		if i == offset {
			fmt.Fprintf(w, "%d ", i)
		} else {
			fmt.Fprintf(w, `<a href="%s/links/%d/%d">%d</a> `, h.prefix, n, i, i)
		}
	}
	w.Write([]byte("</body></html>"))
}

// doRedirect set redirect header
func (h *HTTPBin) doRedirect(w http.ResponseWriter, path string, code int) {
	var sb strings.Builder
	if strings.HasPrefix(path, "/") {
		sb.WriteString(h.prefix)
	}
	sb.WriteString(path)
	w.Header().Set("Location", sb.String())
	w.WriteHeader(code)
}

// ImageAccept responds with an appropriate image based on the Accept header
func (h *HTTPBin) ImageAccept(w http.ResponseWriter, r *http.Request) {
	accept := r.Header.Get("Accept")
	switch {
	case accept == "":
		fallthrough // default to png
	case strings.Contains(accept, "image/*"):
		fallthrough // default to png
	case strings.Contains(accept, "image/png"):
		doImage(w, "png")
	case strings.Contains(accept, "image/webp"):
		doImage(w, "webp")
	case strings.Contains(accept, "image/svg+xml"):
		doImage(w, "svg")
	case strings.Contains(accept, "image/jpeg"):
		doImage(w, "jpeg")
	default:
		writeError(w, http.StatusUnsupportedMediaType, nil)
	}
}

// Image responds with an image of a specific kind, from /image/<kind>
func (h *HTTPBin) Image(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) != 3 {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	doImage(w, parts[2])
}

// doImage responds with a specific kind of image, if there is an image asset
// of the given kind.
func doImage(w http.ResponseWriter, kind string) {
	img, err := staticAsset("image." + kind)
	if err != nil {
		writeError(w, http.StatusNotFound, nil)
		return
	}
	contentType := "image/" + kind
	if kind == "svg" {
		contentType = "image/svg+xml"
	}
	writeResponse(w, http.StatusOK, contentType, img)
}

// XML responds with an XML document
func (h *HTTPBin) XML(w http.ResponseWriter, _ *http.Request) {
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
		writeError(w, http.StatusNotFound, nil)
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
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid QOP directive: %q != \"auth\"", qop))
		return
	}
	if algoName != "MD5" && algoName != "SHA-256" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid algorithm: %s must be one of MD5 or SHA-256", algoName))
		return
	}

	algorithm := digest.MD5
	if algoName == "SHA-256" {
		algorithm = digest.SHA256
	}

	if !digest.Check(r, user, password) {
		w.Header().Set("WWW-Authenticate", digest.Challenge("go-httpbin", algorithm))
		writeError(w, http.StatusUnauthorized, nil)
		return
	}

	writeJSON(http.StatusOK, w, authResponse{
		Authorized: true,
		User:       user,
	})
}

// UUID - responds with a generated UUID
func (h *HTTPBin) UUID(w http.ResponseWriter, _ *http.Request) {
	writeJSON(http.StatusOK, w, uuidResponse{
		UUID: uuidv4(),
	})
}

// Base64 - encodes/decodes input data
func (h *HTTPBin) Base64(w http.ResponseWriter, r *http.Request) {
	result, err := newBase64Helper(r.URL.Path, h.MaxBodySize).transform()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeResponse(w, http.StatusOK, textContentType, result)
}

// DumpRequest - returns the given request in its HTTP/1.x wire representation.
// The returned representation is an approximation only;
// some details of the initial request are lost while parsing it into
// an http.Request. In particular, the order and case of header field
// names are lost.
func (h *HTTPBin) DumpRequest(w http.ResponseWriter, r *http.Request) {
	dump, err := httputil.DumpRequest(r, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to dump request: %w", err))
		return
	}
	w.Write(dump)
}

// JSON - returns a sample json
func (h *HTTPBin) JSON(w http.ResponseWriter, _ *http.Request) {
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
		writeError(w, http.StatusUnauthorized, nil)
		return
	}
	writeJSON(http.StatusOK, w, bearerResponse{
		Authenticated: true,
		Token:         tokenFields[1],
	})
}

// Hostname - returns the hostname.
func (h *HTTPBin) Hostname(w http.ResponseWriter, _ *http.Request) {
	writeJSON(http.StatusOK, w, hostnameResponse{
		Hostname: h.hostname,
	})
}

// SSE writes a stream of events over a duration after an optional
// initial delay.
func (h *HTTPBin) SSE(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var (
		count    = h.DefaultParams.SSECount
		duration = h.DefaultParams.SSEDuration
		delay    = h.DefaultParams.SSEDelay
		err      error
	)

	if userCount := q.Get("count"); userCount != "" {
		count, err = strconv.Atoi(userCount)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid count: %w", err))
			return
		}
		if count < 1 || int64(count) > h.maxSSECount {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid count: must in range [1, %d]", h.maxSSECount))
			return
		}
	}

	if userDuration := q.Get("duration"); userDuration != "" {
		duration, err = parseBoundedDuration(userDuration, 1, h.MaxDuration)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid duration: %w", err))
			return
		}
	}

	if userDelay := q.Get("delay"); userDelay != "" {
		delay, err = parseBoundedDuration(userDelay, 0, h.MaxDuration)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid delay: %w", err))
			return
		}
	}

	if duration+delay > h.MaxDuration {
		http.Error(w, "Too much time", http.StatusBadRequest)
		return
	}

	pause := duration
	if count > 1 {
		// compensate for lack of pause after final write (i.e. if we're
		// writing 10 events, we will only pause 9 times)
		pause = duration / time.Duration(count-1)
	}

	// Initial delay before we send any response data
	if delay > 0 {
		select {
		case <-time.After(delay):
			// ok
		case <-r.Context().Done():
			w.WriteHeader(499) // "Client Closed Request" https://httpstatuses.com/499
			return
		}
	}

	w.Header().Set("Content-Type", sseContentType)
	w.WriteHeader(http.StatusOK)

	flusher := w.(http.Flusher)

	// special case when we only have one event to write
	if count == 1 {
		writeServerSentEvent(w, 0, time.Now())
		flusher.Flush()
		return
	}

	ticker := time.NewTicker(pause)
	defer ticker.Stop()

	for i := 0; i < count; i++ {
		writeServerSentEvent(w, i, time.Now())
		flusher.Flush()

		// don't pause after last byte
		if i == count-1 {
			return
		}

		select {
		case <-ticker.C:
			// ok
		case <-r.Context().Done():
			return
		}
	}
}

// writeServerSentEvent writes the bytes that constitute a single server-sent
// event message, including both the event type and data.
func writeServerSentEvent(dst io.Writer, id int, ts time.Time) {
	dst.Write([]byte("event: ping\n"))
	dst.Write([]byte("data: "))
	json.NewEncoder(dst).Encode(serverSentEvent{
		ID:        id,
		Timestamp: ts.UnixMilli(),
	})
	// each SSE ends with two newlines (\n\n), the first of which is written
	// automatically by json.NewEncoder().Encode()
	dst.Write([]byte("\n"))
}

// WebSocketEcho - simple websocket echo server, where the max fragment size
// and max message size can be controlled by clients.
func (h *HTTPBin) WebSocketEcho(w http.ResponseWriter, r *http.Request) {
	var (
		maxFragmentSize = h.MaxBodySize / 2
		maxMessageSize  = h.MaxBodySize
		q               = r.URL.Query()
		err             error
	)

	if userMaxFragmentSize := q.Get("max_fragment_size"); userMaxFragmentSize != "" {
		maxFragmentSize, err = strconv.ParseInt(userMaxFragmentSize, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid max_fragment_size: %w", err))
			return
		} else if maxFragmentSize < 1 || maxFragmentSize > h.MaxBodySize {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid max_fragment_size: %d not in range [1, %d]", maxFragmentSize, h.MaxBodySize))
			return
		}
	}

	if userMaxMessageSize := q.Get("max_message_size"); userMaxMessageSize != "" {
		maxMessageSize, err = strconv.ParseInt(userMaxMessageSize, 10, 32)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid max_message_size: %w", err))
			return
		} else if maxMessageSize < 1 || maxMessageSize > h.MaxBodySize {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid max_message_size: %d not in range [1, %d]", maxMessageSize, h.MaxBodySize))
			return
		}
	}

	if maxFragmentSize > maxMessageSize {
		writeError(w, http.StatusBadRequest, fmt.Errorf("max_fragment_size %d must be less than or equal to max_message_size %d", maxFragmentSize, maxMessageSize))
		return
	}

	ws := websocket.New(w, r, websocket.Limits{
		MaxDuration:     h.MaxDuration,
		MaxFragmentSize: int(maxFragmentSize),
		MaxMessageSize:  int(maxMessageSize),
	})
	if err := ws.Handshake(); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ws.Serve(websocket.EchoHandler)
}
