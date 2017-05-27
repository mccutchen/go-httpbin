package httpbin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func getOrigin(r *http.Request) string {
	origin := r.Header.Get("X-Forwarded-For")
	if origin == "" {
		origin = r.RemoteAddr
	}
	return origin
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
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(status)
	w.Write(body)
}

func writeJSON(w http.ResponseWriter, body []byte, status int) {
	writeResponse(w, status, jsonContentType, body)
}

func writeHTML(w http.ResponseWriter, body []byte, status int) {
	writeResponse(w, status, htmlContentType, body)
}

// parseBody handles parsing a request body into our standard API response,
// taking care to only consume the request body once based on the Content-Type
// of the request. The given Resp will be updated.
func parseBody(w http.ResponseWriter, r *http.Request, resp *bodyResponse, maxMemory int64) error {
	if r.Body == nil {
		return nil
	}

	// Restrict size of request body
	r.Body = http.MaxBytesReader(w, r.Body, maxMemory)

	ct := r.Header.Get("Content-Type")
	switch {
	case ct == "application/x-www-form-urlencoded":
		err := r.ParseForm()
		if err != nil {
			return err
		}
		resp.Form = r.PostForm
	case strings.HasPrefix(ct, "multipart/form-data"):
		err := r.ParseMultipartForm(maxMemory)
		if err != nil {
			return err
		}
		resp.Form = r.PostForm
	case strings.HasPrefix(ct, "application/json"):
		dec := json.NewDecoder(r.Body)
		err := dec.Decode(&resp.JSON)
		if err != nil {
			return err
		}
	default:
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return err
		}
		resp.Data = data
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

// syntheticReadSeeker implements the ReadSeeker interface to allow reading
// arbitrary subsets of bytes up to a maximum size given a function for
// generating the byte at a given offset.
type syntheticReadSeeker struct {
	numBytes    int64
	offset      int64
	byteFactory func(int64) byte
}

// Read implements the Reader interface for syntheticReadSeeker
func (s *syntheticReadSeeker) Read(p []byte) (int, error) {
	start := s.offset
	end := start + int64(len(p))
	var err error
	if end > s.numBytes {
		err = io.EOF
		end = s.numBytes - start
	}

	for idx := start; idx < end; idx++ {
		p[idx-start] = s.byteFactory(idx)
	}

	return int(end - start), err
}

// Seek implements the Seeker interface for syntheticReadSeeker
func (s *syntheticReadSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		s.offset = offset
	case io.SeekCurrent:
		s.offset += offset
	case io.SeekEnd:
		s.offset = s.numBytes - offset
	default:
		return 0, errors.New("Seek: invalid whence")
	}

	if s.offset < 0 {
		return 0, errors.New("Seek: invalid offset")
	}

	return s.offset, nil
}
