package httpbin

import (
	"bytes"
	crypto_rand "crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Base64MaxLen - Maximum input length for Base64 functions
const Base64MaxLen = 2000

// requestHeaders takes in incoming request and returns an http.Header map
// suitable for inclusion in our response data structures.
//
// This is necessary to ensure that the incoming Host header is included,
// because golang only exposes that header on the http.Request struct itself.
func getRequestHeaders(r *http.Request) http.Header {
	h := r.Header
	h.Set("Host", r.Host)
	return h
}

// getClientIP tries to get a reasonable value for the IP address of the
// client making the request. Note that this value will likely be trivial to
// spoof, so do not rely on it for security purposes.
func getClientIP(r *http.Request) string {
	// Special case some hosting platforms that provide the value directly.
	if clientIP := r.Header.Get("Fly-Client-IP"); clientIP != "" {
		return clientIP
	}

	// Try to pull a reasonable value from the X-Forwarded-For header, if
	// present, by taking the first entry in a comma-separated list of IPs.
	if forwardedFor := r.Header.Get("X-Forwarded-For"); forwardedFor != "" {
		return strings.TrimSpace(strings.SplitN(forwardedFor, ",", 2)[0])
	}

	// Finally, fall back on the actual remote addr from the request.
	return r.RemoteAddr
}

func getURL(r *http.Request) *url.URL {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = r.Header.Get("X-Forwarded-Protocol")
	}
	if scheme == "" && r.Header.Get("X-Forwarded-Ssl") == "on" {
		scheme = "https"
	}
	if scheme == "" {
		scheme = "http"
	}

	host := r.URL.Host
	if host == "" {
		host = r.Host
	}

	return &url.URL{
		Scheme:     scheme,
		Opaque:     r.URL.Opaque,
		User:       r.URL.User,
		Host:       host,
		Path:       r.URL.Path,
		RawPath:    r.URL.RawPath,
		ForceQuery: r.URL.ForceQuery,
		RawQuery:   r.URL.RawQuery,
		Fragment:   r.URL.Fragment,
	}
}

func writeResponse(w http.ResponseWriter, status int, contentType string, body []byte) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(body)
}

func mustMarshalJSON(w io.Writer, val interface{}) {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(val); err != nil {
		panic(err.Error())
	}
}

func writeJSON(status int, w http.ResponseWriter, val interface{}) {
	w.Header().Set("Content-Type", jsonContentType)
	w.WriteHeader(status)
	mustMarshalJSON(w, val)
}

func writeHTML(w http.ResponseWriter, body []byte, status int) {
	writeResponse(w, status, htmlContentType, body)
}

// parseBody handles parsing a request body into our standard API response,
// taking care to only consume the request body once based on the Content-Type
// of the request. The given bodyResponse will be modified.
//
// Note: this function expects callers to limit the the maximum size of the
// request body. See, e.g., the limitRequestSize middleware.
func parseBody(w http.ResponseWriter, r *http.Request, resp *bodyResponse) error {
	if r.Body == nil {
		return nil
	}

	// Always set resp.Data to the incoming request body, in case we don't know
	// how to handle the content type
	body, err := io.ReadAll(r.Body)
	if err != nil {
		r.Body.Close()
		return err
	}
	resp.Data = string(body)

	// After reading the body to populate resp.Data, we need to re-wrap it in
	// an io.Reader for further processing below
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
		// r.ParseForm() does not populate r.PostForm for DELETE or GET requests, but
		// we need it to for compatibility with the httpbin implementation, so
		// we trick it with this ugly hack.
		if r.Method == http.MethodDelete || r.Method == http.MethodGet {
			originalMethod := r.Method
			r.Method = http.MethodPost
			defer func() { r.Method = originalMethod }()
		}
		if err := r.ParseForm(); err != nil {
			return err
		}
		resp.Form = r.PostForm
	case strings.HasPrefix(ct, "multipart/form-data"):
		// The memory limit here only restricts how many parts will be kept in
		// memory before overflowing to disk:
		// https://golang.org/pkg/net/http/#Request.ParseMultipartForm
		if err := r.ParseMultipartForm(1024); err != nil {
			return err
		}
		resp.Form = r.PostForm
	case strings.HasPrefix(ct, "application/json"):
		err := json.NewDecoder(r.Body).Decode(&resp.JSON)
		if err != nil && err != io.EOF {
			return err
		}
	}

	return nil
}

// parseDuration takes a user's input as a string and attempts to convert it
// into a time.Duration. If not given as a go-style duration string, the input
// is assumed to be seconds as a float.
func parseDuration(input string) (time.Duration, error) {
	d, err := time.ParseDuration(input)
	if err != nil {
		n, err := strconv.ParseFloat(input, 64)
		if err != nil {
			return 0, err
		}
		d = time.Duration(n*1000) * time.Millisecond
	}
	return d, nil
}

// parseBoundedDuration parses a time.Duration from user input and ensures that
// it is within a given maximum and minimum time
func parseBoundedDuration(input string, min, max time.Duration) (time.Duration, error) {
	d, err := parseDuration(input)
	if err != nil {
		return 0, err
	}

	if d > max {
		err = fmt.Errorf("duration %s longer than %s", d, max)
	} else if d < min {
		err = fmt.Errorf("duration %s shorter than %s", d, min)
	}
	return d, err
}

// Returns a new rand.Rand from the given seed string.
func parseSeed(rawSeed string) (*rand.Rand, error) {
	var seed int64
	if rawSeed != "" {
		var err error
		seed, err = strconv.ParseInt(rawSeed, 10, 64)
		if err != nil {
			return nil, err
		}
	} else {
		seed = time.Now().UnixNano()
	}

	src := rand.NewSource(seed)
	rng := rand.New(src)
	return rng, nil
}

// syntheticByteStream implements the ReadSeeker interface to allow reading
// arbitrary subsets of bytes up to a maximum size given a function for
// generating the byte at a given offset.
type syntheticByteStream struct {
	mu sync.Mutex

	size    int64
	offset  int64
	factory func(int64) byte
}

// newSyntheticByteStream returns a new stream of bytes of a specific size,
// given a factory function for generating the byte at a given offset.
func newSyntheticByteStream(size int64, factory func(int64) byte) io.ReadSeeker {
	return &syntheticByteStream{
		size:    size,
		factory: factory,
	}
}

// Read implements the Reader interface for syntheticByteStream
func (s *syntheticByteStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	start := s.offset
	end := start + int64(len(p))
	var err error
	if end >= s.size {
		err = io.EOF
		end = s.size
	}

	for idx := start; idx < end; idx++ {
		p[idx-start] = s.factory(idx)
	}

	s.offset = end

	return int(end - start), err
}

// Seek implements the Seeker interface for syntheticByteStream
func (s *syntheticByteStream) Seek(offset int64, whence int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch whence {
	case io.SeekStart:
		s.offset = offset
	case io.SeekCurrent:
		s.offset += offset
	case io.SeekEnd:
		s.offset = s.size - offset
	default:
		return 0, errors.New("Seek: invalid whence")
	}

	if s.offset < 0 {
		return 0, errors.New("Seek: invalid offset")
	}

	return s.offset, nil
}

func sha1hash(input string) string {
	h := sha1.New()
	return fmt.Sprintf("%x", h.Sum([]byte(input)))
}

func uuidv4() string {
	buff := make([]byte, 16)
	_, err := crypto_rand.Read(buff[:])
	if err != nil {
		panic(err)
	}
	buff[6] = (buff[6] & 0x0f) | 0x40 // Version 4
	buff[8] = (buff[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buff[0:4], buff[4:6], buff[6:8], buff[8:10], buff[10:])
}

// base64Helper - describes the base64 operation (encode|decode) and input data
type base64Helper struct {
	operation string
	data      string
}

// newbase64Helper - create a new base64Helper struct
// Supports the following URL paths
// - /base64/input_str
// - /base64/encode/input_str
// - /base64/decode/input_str
func newBase64Helper(path string) (*base64Helper, error) {
	parts := strings.Split(path, "/")

	if len(parts) != 3 && len(parts) != 4 {
		return nil, errors.New("invalid URL")
	}

	var b base64Helper

	// Validation for - /base64/input_str
	if len(parts) == 3 {
		b.operation = "decode"
		b.data = parts[2]
	} else {
		// Validation for
		// - /base64/encode/input_str
		// - /base64/encode/input_str
		b.operation = parts[2]
		if b.operation != "encode" && b.operation != "decode" {
			return nil, fmt.Errorf("invalid operation: %s", b.operation)
		}
		b.data = parts[3]
	}
	if len(b.data) == 0 {
		return nil, errors.New("no input data")
	}
	if len(b.data) >= Base64MaxLen {
		return nil, fmt.Errorf("input length - %d, Cannot handle input >= %d", len(b.data), Base64MaxLen)
	}

	return &b, nil
}

// Encode - encode data as base64
func (b *base64Helper) Encode() ([]byte, error) {
	buff := make([]byte, base64.StdEncoding.EncodedLen(len(b.data)))
	base64.StdEncoding.Encode(buff, []byte(b.data))
	return buff, nil
}

// Decode - decode data from base64
func (b *base64Helper) Decode() ([]byte, error) {
	buff := make([]byte, base64.StdEncoding.DecodedLen(len(b.data)))
	_, err := base64.StdEncoding.Decode(buff, []byte(b.data))
	return buff, err
}
