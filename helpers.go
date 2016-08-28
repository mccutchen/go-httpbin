package main

import (
	"encoding/json"
	"net/http"
	"net/url"
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
