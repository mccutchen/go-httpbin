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
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
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
// This is necessary to ensure that the incoming Host and Transfer-Encoding
// headers are included, because golang only exposes those values on the
// http.Request struct itself.
func getRequestHeaders(r *http.Request, fn headersProcessorFunc) http.Header {
	h := r.Header
	h.Set("Host", r.Host)
	if len(r.TransferEncoding) > 0 {
		h.Set("Transfer-Encoding", strings.Join(r.TransferEncoding, ","))
	}
	if fn != nil {
		return fn(h)
	}
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
	if clientIP := r.Header.Get("CF-Connecting-IP"); clientIP != "" {
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
	if scheme == "" && r.TLS != nil {
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

func writeError(w http.ResponseWriter, code int, err error) {
	resp := errorRespnose{
		Error:      http.StatusText(code),
		StatusCode: code,
	}
	if err != nil {
		resp.Detail = err.Error()
	}
	writeJSON(code, w, resp)
}

// parseFiles handles reading the contents of files in a multipart FileHeader
// and returning a map that can be used as the Files attribute of a response
func parseFiles(fileHeaders map[string][]*multipart.FileHeader) (map[string][]string, error) {
	files := map[string][]string{}
	for k, fs := range fileHeaders {
		files[k] = []string{}

		for _, f := range fs {
			fh, err := f.Open()
			if err != nil {
				return nil, err
			}
			contents, err := io.ReadAll(fh)
			if err != nil {
				return nil, err
			}
			files[k] = append(files[k], string(contents))
		}
	}
	return files, nil
}

// parseBody handles parsing a request body into our standard API response,
// taking care to only consume the request body once based on the Content-Type
// of the request. The given bodyResponse will be modified.
//
// Note: this function expects callers to limit the the maximum size of the
// request body. See, e.g., the limitRequestSize middleware.
func parseBody(w http.ResponseWriter, r *http.Request, resp *bodyResponse) error {
	defer r.Body.Close()

	// Always set resp.Data to the incoming request body, in case we don't know
	// how to handle the content type
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}

	// After reading the body to populate resp.Data, we need to re-wrap it in
	// an io.Reader for further processing below
	r.Body = io.NopCloser(bytes.NewBuffer(body))

	// if we read an empty body, there's no need to do anything further
	if len(body) == 0 {
		return nil
	}

	// Always store the "raw" incoming request body
	resp.Data = string(body)

	contentType, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")

	switch contentType {
	case "text/html", "text/plain":
		// no need for extra parsing, string body is already set above
		return nil

	case "application/x-www-form-urlencoded":
		// r.ParseForm() does not populate r.PostForm for DELETE or GET
		// requests, but we need it to for compatibility with the httpbin
		// implementation, so we trick it with this ugly hack.
		if r.Method == http.MethodDelete || r.Method == http.MethodGet {
			originalMethod := r.Method
			r.Method = http.MethodPost
			defer func() { r.Method = originalMethod }()
		}
		if err := r.ParseForm(); err != nil {
			return err
		}
		resp.Form = r.PostForm

	case "multipart/form-data":
		// The memory limit here only restricts how many parts will be kept in
		// memory before overflowing to disk:
		// https://golang.org/pkg/net/http/#Request.ParseMultipartForm
		if err := r.ParseMultipartForm(1024); err != nil {
			return err
		}
		resp.Form = r.PostForm
		files, err := parseFiles(r.MultipartForm.File)
		if err != nil {
			return err
		}
		resp.Files = files

	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&resp.JSON); err != nil {
			return err
		}

	default:
		// If we don't have a special case for the content type, return it
		// encoded as base64 data url
		resp.Data = encodeData(body, contentType)
	}

	return nil
}

// return provided string as base64 encoded data url, with the given content type
func encodeData(body []byte, contentType string) string {
	// If no content type is provided, default to application/octet-stream
	if contentType == "" {
		contentType = binaryContentType
	}
	data := base64.URLEncoding.EncodeToString(body)
	return string("data:" + contentType + ";base64," + data)
}

func parseStatusCode(input string) (int, error) {
	return parseBoundedStatusCode(input, 100, 599)
}

func parseBoundedStatusCode(input string, min, max int) (int, error) {
	code, err := strconv.Atoi(input)
	if err != nil {
		return 0, fmt.Errorf("invalid status code: %q: %w", input, err)
	}
	if code < min || code > max {
		return 0, fmt.Errorf("invalid status code: %d not in range [%d, %d]", code, min, max)
	}
	return code, nil
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
	if _, err := crypto_rand.Read(buff[:]); err != nil {
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

// Encode - encode data as URL-safe base64
func (b *base64Helper) Encode() ([]byte, error) {
	buff := make([]byte, base64.URLEncoding.EncodedLen(len(b.data)))
	base64.URLEncoding.Encode(buff, []byte(b.data))
	return buff, nil
}

// Decode - decode data from base64, attempting both URL-safe and standard
// encodings.
func (b *base64Helper) Decode() ([]byte, error) {
	if result, err := base64.URLEncoding.DecodeString(b.data); err == nil {
		return result, nil
	}
	return base64.StdEncoding.DecodeString(b.data)
}

func wildCardToRegexp(pattern string) string {
	components := strings.Split(pattern, "*")
	if len(components) == 1 {
		// if len is 1, there are no *'s, return exact match pattern
		return "^" + pattern + "$"
	}
	var result strings.Builder
	for i, literal := range components {

		// Replace * with .*
		if i > 0 {
			result.WriteString(".*")
		}

		// Quote any regular expression meta characters in the
		// literal text.
		result.WriteString(regexp.QuoteMeta(literal))
	}
	return "^" + result.String() + "$"
}

func createExcludeHeadersProcessor(excludeRegex *regexp.Regexp) headersProcessorFunc {
	return func(headers http.Header) http.Header {
		result := make(http.Header)
		for k, v := range headers {
			matched := excludeRegex.Match([]byte(k))
			if matched {
				continue
			}
			result[k] = v
		}

		return result
	}
}

func createFullExcludeRegex(excludeHeaders string) *regexp.Regexp {
	// comma separated list of headers to exclude from response
	tmp := strings.Split(excludeHeaders, ",")

	tmpRegexStrings := make([]string, 0)
	for _, v := range tmp {
		s := strings.TrimSpace(v)
		if len(s) == 0 {
			continue
		}
		pattern := wildCardToRegexp(s)
		tmpRegexStrings = append(tmpRegexStrings, pattern)
	}

	if len(tmpRegexStrings) > 0 {
		tmpRegexStr := strings.Join(tmpRegexStrings, "|")
		result := regexp.MustCompile("(?i)" + "(" + tmpRegexStr + ")")
		return result
	}

	return nil
}
