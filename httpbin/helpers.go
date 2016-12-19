package httpbin

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
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
