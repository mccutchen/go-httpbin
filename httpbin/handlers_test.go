package httpbin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func TestNewHTTPBin__NilOptions(t *testing.T) {
	h := NewHTTPBin(nil)
	if h.options.MaxMemory != 0 {
		t.Fatalf("expected default MaxMemory == 0, got %#v", h.options.MaxMemory)
	}
}

func TestIndex(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/html; charset=utf-8")
	assertBodyContains(t, w, "go-httpbin")
}

func TestIndex__NotFound(t *testing.T) {
	r, _ := http.NewRequest("GET", "/foo", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatusCode(t, w, http.StatusNotFound)
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

func TestGet__WithParams(t *testing.T) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	r, _ := http.NewRequest("GET", fmt.Sprintf("/get?%s", params.Encode()), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if resp.Args.Encode() != params.Encode() {
		t.Fatalf("args mismatch: %s != %s", resp.Args.Encode(), params.Encode())
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

func TestPost__MultiPartBody(t *testing.T) {
	params := map[string][]string{
		"foo": {"foo"},
		"bar": {"bar1", "bar2"},
	}

	// Prepare a form that you will submit to that URL.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	for k, vs := range params {
		for _, v := range vs {
			fw, err := mw.CreateFormField(k)
			if err != nil {
				t.Fatalf("error creating multipart form field %s: %s", k, err)
			}
			if _, err := fw.Write([]byte(v)); err != nil {
				t.Fatalf("error writing multipart form value %#v for key %s: %s", v, k, err)
			}
		}
	}
	mw.Close()

	r, _ := http.NewRequest("POST", "/post", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", mw.FormDataContentType())
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

func TestPost__InvalidFormEncodedBody(t *testing.T) {
	r, _ := http.NewRequest("POST", "/post", strings.NewReader("%ZZ"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestPost__InvalidMultiPartBody(t *testing.T) {
	r, _ := http.NewRequest("POST", "/post", strings.NewReader("%ZZ"))
	r.Header.Set("Content-Type", "multipart/form-data; etc")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatusCode(t, w, http.StatusBadRequest)
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

func TestPost__InvalidJSON(t *testing.T) {
	r, _ := http.NewRequest("POST", "/post", bytes.NewReader([]byte("foo")))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	assertStatusCode(t, w, http.StatusBadRequest)
}

func TestPost__BodyTooBig(t *testing.T) {
	body := make([]byte, maxMemory+1)

	r, _ := http.NewRequest("POST", "/post", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusBadRequest)
	assertContentType(t, w, "application/json; encoding=utf-8")
}

func TestPost__QueryParams(t *testing.T) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	r, _ := http.NewRequest("POST", fmt.Sprintf("/post?%s", params.Encode()), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %#v from JSON: %s", w.Body.String(), err)
	}

	if resp.Args.Encode() != params.Encode() {
		t.Fatalf("expected args = %#v in response, got %#v", params.Encode(), resp.Args.Encode())
	}

	if len(resp.Form) > 0 {
		t.Fatalf("expected form data, got %#v", resp.Form)
	}
}

func TestPost__QueryParamsAndBody(t *testing.T) {
	args := url.Values{}
	args.Set("query1", "foo")
	args.Add("query2", "bar1")
	args.Add("query2", "bar2")

	form := url.Values{}
	form.Set("form1", "foo")
	form.Add("form2", "bar1")
	form.Add("form2", "bar2")

	url := fmt.Sprintf("/post?%s", args.Encode())
	body := strings.NewReader(form.Encode())

	r, _ := http.NewRequest("POST", url, body)
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

	if resp.Args.Encode() != args.Encode() {
		t.Fatalf("expected args = %#v in response, got %#v", args.Encode(), resp.Args.Encode())
	}

	if len(resp.Form) != len(form) {
		t.Fatalf("expected %d form values, got %d", len(form), len(resp.Form))
	}
	for k, expectedValues := range form {
		values, ok := resp.Form[k]
		if !ok {
			t.Fatalf("expected form field %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("form value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

// TODO: implement and test more complex /status endpoint
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

func TestStatus__Errors(t *testing.T) {
	var tests = []struct {
		given  interface{}
		status int
	}{
		{"", http.StatusBadRequest},
		{"200/foo", http.StatusNotFound},
		{3.14, http.StatusBadRequest},
		{"foo", http.StatusBadRequest},
	}

	for _, test := range tests {
		url := fmt.Sprintf("/status/%v", test.given)
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertStatusCode(t, w, test.status)
	}
}

func TestResponseHeaders__OK(t *testing.T) {
	headers := map[string][]string{
		"Foo": {"foo"},
		"Bar": {"bar1, bar2"},
	}

	params := url.Values{}
	for k, vs := range headers {
		for _, v := range vs {
			params.Add(k, v)
		}
	}

	r, _ := http.NewRequest("GET", fmt.Sprintf("/response-headers?%s", params.Encode()), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, "application/json; encoding=utf-8")

	for k, expectedValues := range headers {
		values, ok := w.HeaderMap[k]
		if !ok {
			t.Fatalf("expected header %s in response headers", k)
		}
		if !reflect.DeepEqual(values, expectedValues) {
			t.Fatalf("expected key values %#v for header %s, got %#v", expectedValues, k, values)
		}
	}

	resp := &http.Header{}
	err := json.Unmarshal(w.Body.Bytes(), resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	for k, expectedValues := range headers {
		values, ok := (*resp)[k]
		if !ok {
			t.Fatalf("expected header %s in response body", k)
		}
		if !reflect.DeepEqual(values, expectedValues) {
			t.Fatalf("expected key values %#v for header %s, got %#v", expectedValues, k, values)
		}
	}
}

func TestResponseHeaders__OverrideContentType(t *testing.T) {
	contentType := "text/test"

	params := url.Values{}
	params.Set("Content-Type", contentType)

	r, _ := http.NewRequest("GET", fmt.Sprintf("/response-headers?%s", params.Encode()), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, contentType)
}

func TestRedirects__OK(t *testing.T) {
	var tests = []struct {
		requestURL       string
		expectedLocation string
	}{
		{"/redirect/1", "/get"},
		{"/redirect/2", "/relative-redirect/1"},
		{"/redirect/100", "/relative-redirect/99"},

		{"/redirect/1?absolute=true", "http://host/get"},
		{"/redirect/2?absolute=TRUE", "http://host/absolute-redirect/1"},
		{"/redirect/100?absolute=True", "http://host/absolute-redirect/99"},

		{"/redirect/100?absolute=t", "/relative-redirect/99"},
		{"/redirect/100?absolute=1", "/relative-redirect/99"},
		{"/redirect/100?absolute=yes", "/relative-redirect/99"},

		{"/relative-redirect/1", "/get"},
		{"/relative-redirect/2", "/relative-redirect/1"},
		{"/relative-redirect/100", "/relative-redirect/99"},

		{"/absolute-redirect/1", "http://host/get"},
		{"/absolute-redirect/2", "http://host/absolute-redirect/1"},
		{"/absolute-redirect/100", "http://host/absolute-redirect/99"},
	}

	for _, test := range tests {
		r, _ := http.NewRequest("GET", test.requestURL, nil)
		r.Host = "host"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusFound)
		assertHeader(t, w, "Location", test.expectedLocation)
	}
}

func TestRedirects__Errors(t *testing.T) {
	var tests = []struct {
		requestURL     string
		expectedStatus int
	}{
		{"/redirect", http.StatusNotFound},
		{"/redirect/", http.StatusBadRequest},
		{"/redirect/3.14", http.StatusBadRequest},
		{"/redirect/foo", http.StatusBadRequest},
		{"/redirect/10/foo", http.StatusNotFound},

		{"/relative-redirect", http.StatusNotFound},
		{"/relative-redirect/", http.StatusBadRequest},
		{"/relative-redirect/3.14", http.StatusBadRequest},
		{"/relative-redirect/foo", http.StatusBadRequest},
		{"/relative-redirect/10/foo", http.StatusNotFound},

		{"/absolute-redirect", http.StatusNotFound},
		{"/absolute-redirect/", http.StatusBadRequest},
		{"/absolute-redirect/3.14", http.StatusBadRequest},
		{"/absolute-redirect/foo", http.StatusBadRequest},
		{"/absolute-redirect/10/foo", http.StatusNotFound},
	}

	for _, test := range tests {
		r, _ := http.NewRequest("GET", test.requestURL, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, test.expectedStatus)
	}
}
