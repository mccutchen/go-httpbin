package main

import (
	"encoding/json"
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

func getURL(r *http.Request) string {
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

	u := &url.URL{
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
	return u.String()
}

func writeResponse(w http.ResponseWriter, r *http.Request, resp *Resp) {
	resp.Origin = getOrigin(r)
	resp.URL = getURL(r)
	body, _ := json.Marshal(resp)
	writeJSON(w, body)
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json; encoding=utf-8")
	w.Write(body)
}

// parseBody handles parsing a request body into our standard API response,
// taking care to only consume the request body once based on the Content-Type
// of the request. The given Resp will be updated.
func parseBody(w http.ResponseWriter, r *http.Request, resp *Resp) error {
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
	case ct == "multipart/form-data":
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
