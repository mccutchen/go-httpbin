package httpbin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const maxMemory = 1024 * 1024

var app = NewHTTPBin(&Options{
	MaxMemory: maxMemory,
})

var handler = app.Handler()

func assertStatusCode(t *testing.T, w *httptest.ResponseRecorder, code int) {
	if w.Code != code {
		t.Fatalf("expected status code %d, got %d", code, w.Code)
	}
}

func assertHeader(t *testing.T, w *httptest.ResponseRecorder, key, val string) {
	if w.Header().Get(key) != val {
		t.Fatalf("expected header %s=%#v, got %#v", key, val, w.Header().Get(key))
	}
}

func assertContentType(t *testing.T, w *httptest.ResponseRecorder, contentType string) {
	assertHeader(t, w, "Content-Type", contentType)
}

func assertBodyContains(t *testing.T, w *httptest.ResponseRecorder, needle string) {
	if !strings.Contains(w.Body.String(), needle) {
		t.Fatalf("expected string %v in body", needle)
	}
}

func TestIndex(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/html; charset=utf-8")
	assertBodyContains(t, w, "go-httpbin")
}

func TestFormsPost(t *testing.T) {
	r, _ := http.NewRequest("GET", "/forms/post", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/html; charset=utf-8")
	assertBodyContains(t, w, `<form method="post" action="/post">`)
}

func TestUTF8(t *testing.T) {
	r, _ := http.NewRequest("GET", "/encoding/utf8", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/html; charset=utf-8")
	assertBodyContains(t, w, `Hello world, Καλημέρα κόσμε, コンニチハ`)
}

func TestGet__Basic(t *testing.T) {
	r, _ := http.NewRequest("GET", "/get", nil)
	r.Host = "localhost"
	r.Header.Set("User-Agent", "test")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
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

func TestGet__OnlyAllowsGets(t *testing.T) {
	r, _ := http.NewRequest("POST", "/get", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusMethodNotAllowed)
	assertContentType(t, w, "text/plain; charset=utf-8")
}

func TestGet__CORSHeadersWithoutRequestOrigin(t *testing.T) {
	r, _ := http.NewRequest("GET", "/get", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertHeader(t, w, "Access-Control-Allow-Origin", "*")
}

func TestGet__CORSHeadersWithRequestOrigin(t *testing.T) {
	r, _ := http.NewRequest("GET", "/get", nil)
	r.Header.Set("Origin", "origin")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertHeader(t, w, "Access-Control-Allow-Origin", "origin")
}

func TestGet__CORSHeadersWithOptionsVerb(t *testing.T) {
	r, _ := http.NewRequest("OPTIONS", "/get", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	var headerTests = []struct {
		key      string
		expected string
	}{
		{"Access-Control-Allow-Origin", "*"},
		{"Access-Control-Allow-Credentials", "true"},
		{"Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS"},
		{"Access-Control-Max-Age", "3600"},
		{"Access-Control-Allow-Headers", ""},
	}
	for _, test := range headerTests {
		assertHeader(t, w, test.key, test.expected)
	}
}

func TestGet__CORSAllowHeaders(t *testing.T) {
	r, _ := http.NewRequest("OPTIONS", "/get", nil)
	r.Header.Set("Access-Control-Request-Headers", "X-Test-Header")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	var headerTests = []struct {
		key      string
		expected string
	}{
		{"Access-Control-Allow-Headers", "X-Test-Header"},
	}
	for _, test := range headerTests {
		assertHeader(t, w, test.key, test.expected)
	}
}

func TestGet__XForwardedProto(t *testing.T) {
	var tests = []struct {
		key   string
		value string
	}{
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Protocol", "https"},
		{"X-Forwarded-Ssl", "on"},
	}

	for _, test := range tests {
		r, _ := http.NewRequest("GET", "/get", nil)
		r.Header.Set(test.key, test.value)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		var resp *bodyResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
		}

		if !strings.HasPrefix(resp.URL, "https://") {
			t.Fatalf("%s=%s should result in https URL", test.key, test.value)
		}
	}
}

func TestIP(t *testing.T) {
	r, _ := http.NewRequest("GET", "/ip", nil)
	r.RemoteAddr = "192.168.0.100"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *ipResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if resp.Origin != r.RemoteAddr {
		t.Fatalf("%#v != %#v", resp.Origin, r.RemoteAddr)
	}
}

func TestUserAgent(t *testing.T) {
	r, _ := http.NewRequest("GET", "/user-agent", nil)
	r.Header.Set("User-Agent", "test")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *userAgentResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if resp.UserAgent != "test" {
		t.Fatalf("%#v != \"test\"", resp.UserAgent)
	}
}

func TestHeaders(t *testing.T) {
	r, _ := http.NewRequest("GET", "/headers", nil)
	r.Header.Set("User-Agent", "test")
	r.Header.Set("Foo-Header", "foo")
	r.Header.Add("Bar-Header", "bar1")
	r.Header.Add("Bar-Header", "bar2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *headersResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	for k, expectedValues := range r.Header {
		values, ok := resp.Headers[http.CanonicalHeaderKey(k)]
		if !ok {
			t.Fatalf("expected header %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("header value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

func TestPost__EmptyBody(t *testing.T) {
	r, _ := http.NewRequest("POST", "/post", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if len(resp.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", resp.Args)
	}
	if len(resp.Form) > 0 {
		t.Fatalf("expected no form data, got %#v", resp.Form)
	}
}

func TestPost__FormEncodedBody(t *testing.T) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	r, _ := http.NewRequest("POST", "/post", strings.NewReader(params.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %#v from JSON: %s", w.Body.String(), err)
	}

	if len(resp.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", resp.Args)
	}
	if len(resp.Form) != len(params) {
		t.Fatalf("expected %d form values, got %d", len(params), len(resp.Form))
	}
	for k, expectedValues := range params {
		values, ok := resp.Form[k]
		if !ok {
			t.Fatalf("expected form field %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("form value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

func TestPost__FormEncodedBodyNoContentType(t *testing.T) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	r, _ := http.NewRequest("POST", "/post", strings.NewReader(params.Encode()))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if len(resp.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", resp.Args)
	}
	if len(resp.Form) != 0 {
		t.Fatalf("expected no form values, got %d", len(resp.Form))
	}
	if string(resp.Data) != params.Encode() {
		t.Fatalf("response data mismatch, %#v != %#v", string(resp.Data), params.Encode())
	}
}

func TestPost__JSON(t *testing.T) {
	type testInput struct {
		Foo  string
		Bar  int
		Baz  []float64
		Quux map[int]string
	}
	input := &testInput{
		Foo:  "foo",
		Bar:  123,
		Baz:  []float64{1.0, 1.1, 1.2},
		Quux: map[int]string{1: "one", 2: "two", 3: "three"},
	}
	inputBody, _ := json.Marshal(input)

	r, _ := http.NewRequest("POST", "/post", bytes.NewReader(inputBody))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if len(resp.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", resp.Args)
	}
	if len(resp.Form) != 0 {
		t.Fatalf("expected no form values, got %d", len(resp.Form))
	}
	if resp.Data != nil {
		t.Fatalf("expected no data, got %#v", resp.Data)
	}

	// Need to re-marshall just the JSON field from the response in order to
	// re-unmarshall it into our expected type
	outputBodyBytes, _ := json.Marshal(resp.JSON)
	output := &testInput{}
	err = json.Unmarshal(outputBodyBytes, output)
	if err != nil {
		t.Fatalf("failed to round-trip JSON: coult not re-unmarshal JSON: %s", err)
	}

	if !reflect.DeepEqual(input, output) {
		t.Fatalf("failed to round-trip JSON: %#v != %#v", output, input)
	}
}

func TestPost__BodyTooBig(t *testing.T) {
	body := make([]byte, maxMemory+1)

	r, _ := http.NewRequest("POST", "/post", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusBadRequest)
	assertContentType(t, w, "application/json; encoding=utf-8")
}

func TestStatus__Simple(t *testing.T) {
	redirectHeaders := map[string]string{
		"Location": "/redirect/1",
	}
	unauthorizedHeaders := map[string]string{
		"WWW-Authenticate": `Basic realm="Fake Realm"`,
	}
	var tests = []struct {
		code    int
		headers map[string]string
		body    string
	}{
		{200, nil, ""},
		{301, redirectHeaders, ""},
		{302, redirectHeaders, ""},
		{401, unauthorizedHeaders, ""},
		{418, nil, "I'm a teapot!"},
	}

	for _, test := range tests {
		r, _ := http.NewRequest("GET", fmt.Sprintf("/status/%d", test.code), nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, test.code)

		if test.headers != nil {
			for key, val := range test.headers {
				assertHeader(t, w, key, val)
			}
		}

		if test.body != "" {
			if w.Body.String() != test.body {
				t.Fatalf("expected body %#v, got %#v", test.body, w.Body.String())
			}
		}
	}
}
