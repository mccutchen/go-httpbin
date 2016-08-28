package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGet(t *testing.T) {
	r, _ := http.NewRequest("GET", "/get", nil)
	r.Host = "localhost"
	r.Header.Set("User-Agent", "test")
	w := httptest.NewRecorder()
	get(w, r)

	if w.Code != 200 {
		t.Fatalf("expected status code 200, got %d", w.Code)
	}

	var resp *Resp
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if resp.Args.Encode() != "" {
		t.Fatalf("expected empty args, got %s", resp.Args.Encode())
	}
	if resp.Origin != "" {
		t.Fatalf("expected empty origin, got %#v", resp.Origin)
	}
	if resp.URL != "http://localhost/get" {
		t.Fatalf("unexpected url: %#v", resp.URL)
	}

	var headerTests = []struct {
		key      string
		expected string
	}{
		{"Content-Type", ""},
		{"User-Agent", "test"},
	}
	for _, test := range headerTests {
		if resp.Headers.Get(test.key) != test.expected {
			t.Fatalf("expected %s = %#v, got %#v", test.key, test.expected, resp.Headers.Get(test.key))
		}
	}
}
