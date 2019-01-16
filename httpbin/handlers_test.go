package httpbin

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

const maxBodySize int64 = 1024 * 1024
const maxDuration time.Duration = 1 * time.Second

var app = New(
	WithMaxBodySize(maxBodySize),
	WithMaxDuration(maxDuration),
	WithObserver(StdLogObserver(log.New(os.Stderr, "", 0))),
)

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

func assertBodyEquals(t *testing.T, w *httptest.ResponseRecorder, want string) {
	have := w.Body.String()
	if want != have {
		t.Fatalf("expected body = %v, got %v", want, have)
	}
}

func TestIndex(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, htmlContentType)
	assertHeader(t, w, "Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
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

	assertContentType(t, w, htmlContentType)
	assertBodyContains(t, w, `<form method="post" action="/post">`)
}

func TestUTF8(t *testing.T) {
	r, _ := http.NewRequest("GET", "/encoding/utf8", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, htmlContentType)
	assertBodyContains(t, w, `Hello world, Καλημέρα κόσμε, コンニチハ`)
}

func TestGet(t *testing.T) {
	makeGetRequest := func(params *url.Values, headers *http.Header, expectedStatus int) (*getResponse, *httptest.ResponseRecorder) {
		urlStr := "/get"
		if params != nil {
			urlStr = fmt.Sprintf("%s?%s", urlStr, params.Encode())
		}
		r, _ := http.NewRequest("GET", urlStr, nil)
		r.Host = "localhost"
		r.Header.Set("User-Agent", "test")
		if headers != nil {
			for k, vs := range *headers {
				for _, v := range vs {
					r.Header.Set(k, v)
				}
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, expectedStatus)

		var resp *getResponse
		if expectedStatus == http.StatusOK {
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			if err != nil {
				t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
			}
		}
		return resp, w
	}

	t.Run("basic", func(t *testing.T) {
		resp, _ := makeGetRequest(nil, nil, http.StatusOK)

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
	})

	t.Run("with_query_params", func(t *testing.T) {
		params := &url.Values{}
		params.Set("foo", "foo")
		params.Add("bar", "bar1")
		params.Add("bar", "bar2")

		resp, _ := makeGetRequest(params, nil, http.StatusOK)
		if resp.Args.Encode() != params.Encode() {
			t.Fatalf("args mismatch: %s != %s", resp.Args.Encode(), params.Encode())
		}
	})

	t.Run("only_allows_gets", func(t *testing.T) {
		r, _ := http.NewRequest("POST", "/get", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusMethodNotAllowed)
		assertContentType(t, w, "text/plain; charset=utf-8")
	})

	var protoTests = []struct {
		key   string
		value string
	}{
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Protocol", "https"},
		{"X-Forwarded-Ssl", "on"},
	}
	for _, test := range protoTests {
		t.Run(test.key, func(t *testing.T) {
			headers := &http.Header{}
			headers.Set(test.key, test.value)
			resp, _ := makeGetRequest(nil, headers, http.StatusOK)
			if !strings.HasPrefix(resp.URL, "https://") {
				t.Fatalf("%s=%s should result in https URL", test.key, test.value)
			}
		})
	}
}

func TestHEAD(t *testing.T) {
	r, _ := http.NewRequest("HEAD", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, 200)
	assertBodyEquals(t, w, "")

	contentLengthStr := w.HeaderMap.Get("Content-Length")
	if contentLengthStr == "" {
		t.Fatalf("missing Content-Length header in response")
	}
	contentLength, err := strconv.Atoi(contentLengthStr)
	if err != nil {
		t.Fatalf("error converting Content-Lengh %v to integer: %s", contentLengthStr, err)
	}
	if contentLength <= 0 {
		t.Fatalf("Content-Lengh %v should be greater than 0", contentLengthStr)
	}
}

func TestCORS(t *testing.T) {
	t.Run("CORS/no_request_origin", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/get", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertHeader(t, w, "Access-Control-Allow-Origin", "*")
	})

	t.Run("CORS/with_request_origin", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/get", nil)
		r.Header.Set("Origin", "origin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertHeader(t, w, "Access-Control-Allow-Origin", "origin")
	})

	t.Run("CORS/options_request", func(t *testing.T) {
		r, _ := http.NewRequest("OPTIONS", "/get", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, 200)

		var headerTests = []struct {
			key      string
			expected string
		}{
			{"Access-Control-Allow-Origin", "*"},
			{"Access-Control-Allow-Credentials", "true"},
			{"Access-Control-Allow-Methods", "GET, POST, HEAD, PUT, DELETE, PATCH, OPTIONS"},
			{"Access-Control-Max-Age", "3600"},
			{"Access-Control-Allow-Headers", ""},
		}
		for _, test := range headerTests {
			assertHeader(t, w, test.key, test.expected)
		}
	})

	t.Run("CORS/allow_headers", func(t *testing.T) {
		r, _ := http.NewRequest("OPTIONS", "/get", nil)
		r.Header.Set("Access-Control-Request-Headers", "X-Test-Header")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, 200)

		var headerTests = []struct {
			key      string
			expected string
		}{
			{"Access-Control-Allow-Headers", "X-Test-Header"},
		}
		for _, test := range headerTests {
			assertHeader(t, w, test.key, test.expected)
		}
	})
}

func TestIP(t *testing.T) {
	r, _ := http.NewRequest("GET", "/ip", nil)
	r.RemoteAddr = "192.168.0.100"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, jsonContentType)

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
	assertContentType(t, w, jsonContentType)

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
	r.Host = "test-host"
	r.Header.Set("User-Agent", "test")
	r.Header.Set("Foo-Header", "foo")
	r.Header.Add("Bar-Header", "bar1")
	r.Header.Add("Bar-Header", "bar2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusOK)
	assertContentType(t, w, jsonContentType)

	var resp *headersResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	// Host header requires special treatment, because its an attribute of the
	// http.Request struct itself, not part of its headers map
	host := resp.Headers[http.CanonicalHeaderKey("Host")]
	if host == nil || host[0] != "test-host" {
		t.Fatalf("expected Host header \"test-host\", got %#v", host)
	}

	for k, expectedValues := range r.Header {
		values, ok := resp.Headers[http.CanonicalHeaderKey(k)]
		if !ok {
			t.Fatalf("expected header %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("header %s value mismatch: %#v != %#v", k, values, expectedValues)
		}
	}
}

func TestPost__EmptyBody(t *testing.T) {
	var tests = []struct {
		contentType string
	}{
		{""},
		{"application/json; charset=utf-8"},
		{"application/x-www-form-urlencoded"},
		{"multipart/form-data; foo"},
	}
	for _, test := range tests {
		t.Run("content type/"+test.contentType, func(t *testing.T) {
			r, _ := http.NewRequest("POST", "/post", nil)
			r.Header.Set("Content-Type", test.contentType)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, http.StatusOK)
			assertContentType(t, w, jsonContentType)

			var resp *bodyResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			if err != nil {
				t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
			}

			if resp.Data != "" {
				t.Fatalf("expected empty response data, got %#v", resp.Data)
			}
			if resp.JSON != nil {
				t.Fatalf("expected nil response json, got %#v", resp.JSON)
			}

			if len(resp.Args) > 0 {
				t.Fatalf("expected no query params, got %#v", resp.Args)
			}
			if len(resp.Form) > 0 {
				t.Fatalf("expected no form data, got %#v", resp.Form)
			}
		})
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
	assertContentType(t, w, jsonContentType)

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
	assertContentType(t, w, jsonContentType)

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
	assertContentType(t, w, jsonContentType)

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
	assertContentType(t, w, jsonContentType)

	var resp *bodyResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
	}

	if resp.Data != string(inputBody) {
		t.Fatalf("expected data == %#v, got %#v", string(inputBody), resp.Data)
	}
	if len(resp.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", resp.Args)
	}
	if len(resp.Form) != 0 {
		t.Fatalf("expected no form values, got %d", len(resp.Form))
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
	body := make([]byte, maxBodySize+1)

	r, _ := http.NewRequest("POST", "/post", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusBadRequest)
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
	assertContentType(t, w, jsonContentType)

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
	assertContentType(t, w, jsonContentType)

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
func TestStatus(t *testing.T) {
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
		t.Run(fmt.Sprintf("ok/status/%d", test.code), func(t *testing.T) {
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
		})
	}

	var errorTests = []struct {
		url    string
		status int
	}{
		{"/status", http.StatusNotFound},
		{"/status/", http.StatusBadRequest},
		{"/status/200/foo", http.StatusNotFound},
		{"/status/3.14", http.StatusBadRequest},
		{"/status/foo", http.StatusBadRequest},
	}

	for _, test := range errorTests {
		t.Run("error"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.status)
		})
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
	assertContentType(t, w, jsonContentType)

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

func TestRedirects(t *testing.T) {
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
		t.Run("ok"+test.requestURL, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.requestURL, nil)
			r.Host = "host"
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, http.StatusFound)
			assertHeader(t, w, "Location", test.expectedLocation)
		})
	}

	var errorTests = []struct {
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

	for _, test := range errorTests {
		t.Run("error"+test.requestURL, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.requestURL, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestRedirectTo(t *testing.T) {
	var okTests = []struct {
		url              string
		expectedLocation string
		expectedStatus   int
	}{
		{"/redirect-to?url=http://www.example.com/", "http://www.example.com/", http.StatusFound},
		{"/redirect-to?url=http://www.example.com/&status_code=307", "http://www.example.com/", http.StatusTemporaryRedirect},

		{"/redirect-to?url=/get", "/get", http.StatusFound},
		{"/redirect-to?url=/get&status_code=307", "/get", http.StatusTemporaryRedirect},

		{"/redirect-to?url=foo", "foo", http.StatusFound},
	}

	for _, test := range okTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, test.expectedStatus)
			assertHeader(t, w, "Location", test.expectedLocation)
		})
	}

	var badTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/redirect-to", http.StatusBadRequest},
		{"/redirect-to?status_code=302", http.StatusBadRequest},
		{"/redirect-to?url=foo&status_code=418", http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestCookies(t *testing.T) {
	testCookies := func(t *testing.T, cookies cookiesResponse) {
		r, _ := http.NewRequest("GET", "/cookies", nil)
		for k, v := range cookies {
			r.AddCookie(&http.Cookie{
				Name:  k,
				Value: v,
			})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, jsonContentType)

		resp := cookiesResponse{}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
		}

		if !reflect.DeepEqual(cookies, resp) {
			t.Fatalf("expected cookies %#v, got %#v", cookies, resp)
		}
	}

	t.Run("ok/no cookies", func(t *testing.T) {
		testCookies(t, cookiesResponse{})
	})

	t.Run("ok/cookies", func(t *testing.T) {
		testCookies(t, cookiesResponse{
			"k1": "v1",
			"k2": "v2",
		})
	})
}

func TestSetCookies(t *testing.T) {
	cookies := cookiesResponse{
		"k1": "v1",
		"k2": "v2",
	}

	params := &url.Values{}
	for k, v := range cookies {
		params.Set(k, v)
	}

	r, _ := http.NewRequest("GET", fmt.Sprintf("/cookies/set?%s", params.Encode()), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusFound)
	assertHeader(t, w, "Location", "/cookies")

	for _, c := range w.Result().Cookies() {
		v, ok := cookies[c.Name]
		if !ok {
			t.Fatalf("got unexpected cookie %s=%s", c.Name, c.Value)
		}
		if v != c.Value {
			t.Fatalf("got cookie %s=%s, expected value in %#v", c.Name, c.Value, v)
		}
	}
}

func TestDeleteCookies(t *testing.T) {
	cookies := cookiesResponse{
		"k1": "v1",
		"k2": "v2",
	}

	toDelete := "k2"
	params := &url.Values{}
	params.Set(toDelete, "")

	r, _ := http.NewRequest("GET", fmt.Sprintf("/cookies/delete?%s", params.Encode()), nil)
	for k, v := range cookies {
		r.AddCookie(&http.Cookie{
			Name:  k,
			Value: v,
		})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertStatusCode(t, w, http.StatusFound)
	assertHeader(t, w, "Location", "/cookies")

	for _, c := range w.Result().Cookies() {
		if c.Name == toDelete {
			if time.Now().Sub(c.Expires) < (24*365-1)*time.Hour {
				t.Fatalf("expected cookie %s to be deleted; got %#v", toDelete, c)
			}
		}
	}
}

func TestBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/basic-auth/user/pass", nil)
		r.SetBasicAuth("user", "pass")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, jsonContentType)

		resp := &authResponse{}
		json.Unmarshal(w.Body.Bytes(), resp)

		expectedResp := &authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(resp, expectedResp) {
			t.Fatalf("expected response %#v, got %#v", expectedResp, resp)
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/basic-auth/user/pass", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusUnauthorized)
		assertContentType(t, w, jsonContentType)
		assertHeader(t, w, "WWW-Authenticate", `Basic realm="Fake Realm"`)

		resp := &authResponse{}
		json.Unmarshal(w.Body.Bytes(), resp)

		expectedResp := &authResponse{
			Authorized: false,
			User:       "",
		}
		if !reflect.DeepEqual(resp, expectedResp) {
			t.Fatalf("expected response %#v, got %#v", expectedResp, resp)
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/basic-auth/user/pass", nil)
		r.SetBasicAuth("bad", "auth")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusUnauthorized)
		assertContentType(t, w, jsonContentType)
		assertHeader(t, w, "WWW-Authenticate", `Basic realm="Fake Realm"`)

		resp := &authResponse{}
		json.Unmarshal(w.Body.Bytes(), resp)

		expectedResp := &authResponse{
			Authorized: false,
			User:       "bad",
		}
		if !reflect.DeepEqual(resp, expectedResp) {
			t.Fatalf("expected response %#v, got %#v", expectedResp, resp)
		}
	})

	var errorTests = []struct {
		url    string
		status int
	}{
		{"/basic-auth", http.StatusNotFound},
		{"/basic-auth/user", http.StatusNotFound},
		{"/basic-auth/user/pass/extra", http.StatusNotFound},
	}
	for _, test := range errorTests {
		t.Run("error"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			r.SetBasicAuth("foo", "bar")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.status)
		})
	}
}

func TestHiddenBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/hidden-basic-auth/user/pass", nil)
		r.SetBasicAuth("user", "pass")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, jsonContentType)

		resp := &authResponse{}
		json.Unmarshal(w.Body.Bytes(), resp)

		expectedResp := &authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(resp, expectedResp) {
			t.Fatalf("expected response %#v, got %#v", expectedResp, resp)
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/hidden-basic-auth/user/pass", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusNotFound)
		if w.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("did not expect WWW-Authenticate header")
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "/hidden-basic-auth/user/pass", nil)
		r.SetBasicAuth("bad", "auth")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusNotFound)
		if w.Header().Get("WWW-Authenticate") != "" {
			t.Fatal("did not expect WWW-Authenticate header")
		}
	})

	var errorTests = []struct {
		url    string
		status int
	}{
		{"/hidden-basic-auth", http.StatusNotFound},
		{"/hidden-basic-auth/user", http.StatusNotFound},
		{"/hidden-basic-auth/user/pass/extra", http.StatusNotFound},
	}
	for _, test := range errorTests {
		t.Run("error"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			r.SetBasicAuth("foo", "bar")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.status)
		})
	}
}

func TestDigestAuth(t *testing.T) {
	var tests = []struct {
		url    string
		status int
	}{
		{"/digest-auth", http.StatusNotFound},
		{"/digest-auth/user", http.StatusNotFound},
		{"/digest-auth/user/pass", http.StatusNotFound},
		{"/digest-auth/auth/user/pass/MD5/foo", http.StatusNotFound},

		// valid but unauthenticated requests
		{"/digest-auth/auth/user/pass", http.StatusUnauthorized},
		{"/digest-auth/auth/user/pass/MD5", http.StatusUnauthorized},
		{"/digest-auth/auth/user/pass/SHA-256", http.StatusUnauthorized},

		// invalid requests
		{"/digest-auth/bad-qop/user/pass/MD5", http.StatusBadRequest},
		{"/digest-auth/auth/user/pass/SHA-512", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.status)
		})
	}

	t.Run("ok", func(t *testing.T) {
		// Example captured from a successful login in a browser
		authorization := `Digest username="user",
			realm="go-httpbin",
			nonce="6fb213c6593975c877bb1247370527ad",
			uri="/digest-auth/auth/user/pass/MD5",
			algorithm=MD5,
			response="9b7a05d78051b4f668356eedf32f55d6",
			opaque="fd1c386a015a2bb7c41585f54329ce91",
			qop=auth,
			nc=00000001,
			cnonce="aaab705226af5bd4"`

		url := "/digest-auth/auth/user/pass/MD5"
		r, _ := http.NewRequest("GET", url, nil)
		r.RequestURI = url
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)

		resp := &authResponse{}
		json.Unmarshal(w.Body.Bytes(), resp)

		expectedResp := &authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(resp, expectedResp) {
			t.Fatalf("expected response %#v, got %#v", expectedResp, resp)
		}
	})
}

func TestGzip(t *testing.T) {
	r, _ := http.NewRequest("GET", "/gzip", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "application/json; encoding=utf-8")
	assertHeader(t, w, "Content-Encoding", "gzip")
	assertStatusCode(t, w, http.StatusOK)

	zippedContentLengthStr := w.HeaderMap.Get("Content-Length")
	if zippedContentLengthStr == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	zippedContentLength, err := strconv.Atoi(zippedContentLengthStr)
	if err != nil {
		t.Fatalf("error converting Content-Lengh %v to integer: %s", zippedContentLengthStr, err)
	}

	gzipReader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("error creating gzip reader: %s", err)
	}

	unzippedBody, err := ioutil.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("error reading gzipped body: %s", err)
	}

	var resp *gzipResponse
	err = json.Unmarshal(unzippedBody, &resp)
	if err != nil {
		t.Fatalf("error unmarshalling response: %s", err)
	}

	if resp.Gzipped != true {
		t.Fatalf("expected resp.Gzipped == true")
	}

	if len(unzippedBody) >= zippedContentLength {
		t.Fatalf("expected compressed body")
	}
}

func TestDeflate(t *testing.T) {
	r, _ := http.NewRequest("GET", "/deflate", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "application/json; encoding=utf-8")
	assertHeader(t, w, "Content-Encoding", "deflate")
	assertStatusCode(t, w, http.StatusOK)

	contentLengthHeader := w.HeaderMap.Get("Content-Length")
	if contentLengthHeader == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	contentLength, err := strconv.Atoi(contentLengthHeader)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := zlib.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := ioutil.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	var resp *deflateResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		t.Fatalf("error unmarshalling response: %s", err)
	}

	if resp.Deflated != true {
		t.Fatalf("expected resp.Deflated == true")
	}

	if len(body) >= contentLength {
		t.Fatalf("expected compressed body")
	}
}

func TestStream(t *testing.T) {
	var okTests = []struct {
		url           string
		expectedLines int
	}{
		{"/stream/20", 20},
		{"/stream/100", 100},
		{"/stream/1000", 100},
		{"/stream/0", 1},
		{"/stream/-100", 1},
	}
	for _, test := range okTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			// TODO: The stdlib seems to automagically unchunk these responses
			// and I'm not quite sure how to test this:
			//
			//     assertHeader(t, w, "Transfer-Encoding", "chunked")
			//
			// Instead, we assert that we got no Content-Length header, which
			// is an indication that the Go stdlib streamed the response.
			assertHeader(t, w, "Content-Length", "")

			var resp *streamResponse
			var err error

			i := 0
			scanner := bufio.NewScanner(w.Body)
			for scanner.Scan() {
				err = json.Unmarshal(scanner.Bytes(), &resp)
				if err != nil {
					t.Fatalf("error unmarshalling response: %s", err)
				}
				if resp.ID != i {
					t.Fatalf("bad id: %v != %v", resp.ID, i)
				}
				i++
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("error scanning streaming response: %s", err)
			}
		})
	}

	var badTests = []struct {
		url  string
		code int
	}{
		{"/stream", http.StatusNotFound},
		{"/stream/foo", http.StatusBadRequest},
		{"/stream/3.1415", http.StatusBadRequest},
		{"/stream/10/foo", http.StatusNotFound},
	}

	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.code)
		})
	}
}

func TestDelay(t *testing.T) {
	var okTests = []struct {
		url           string
		expectedDelay time.Duration
	}{
		// go-style durations are supported
		{"/delay/0ms", 0},
		{"/delay/500ms", 500 * time.Millisecond},

		// as are floating point seconds
		{"/delay/0", 0},
		{"/delay/0.5", 500 * time.Millisecond},
		{"/delay/1", maxDuration},
	}
	for _, test := range okTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			start := time.Now()

			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			elapsed := time.Now().Sub(start)

			assertStatusCode(t, w, http.StatusOK)
			assertHeader(t, w, "Content-Type", jsonContentType)

			var resp *bodyResponse
			err := json.Unmarshal(w.Body.Bytes(), &resp)
			if err != nil {
				t.Fatalf("error unmarshalling response: %s", err)
			}

			if elapsed < test.expectedDelay {
				t.Fatalf("expected delay of %s, got %s", test.expectedDelay, elapsed)
			}
		})
	}

	t.Run("handle cancelation", func(t *testing.T) {
		srv := httptest.NewServer(handler)
		defer srv.Close()

		client := http.Client{
			Timeout: time.Duration(10 * time.Millisecond),
		}
		_, err := client.Get(srv.URL + "/delay/1")
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})

	var badTests = []struct {
		url  string
		code int
	}{
		{"/delay", http.StatusNotFound},
		{"/delay/foo", http.StatusBadRequest},
		{"/delay/1/foo", http.StatusNotFound},

		{"/delay/1.5s", http.StatusBadRequest},
		{"/delay/-1ms", http.StatusBadRequest},
		{"/delay/1.5", http.StatusBadRequest},
		{"/delay/-1", http.StatusBadRequest},
		{"/delay/-3.14", http.StatusBadRequest},
	}

	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.code)
		})
	}
}

func TestDrip(t *testing.T) {
	var okTests = []struct {
		params   *url.Values
		duration time.Duration
		numbytes int
		code     int
	}{
		// there are useful defaults for all values
		{&url.Values{}, 0, 10, http.StatusOK},

		// go-style durations are accepted
		{&url.Values{"duration": {"5ms"}}, 5 * time.Millisecond, 10, http.StatusOK},
		{&url.Values{"duration": {"0h"}}, 0, 10, http.StatusOK},
		{&url.Values{"delay": {"5ms"}}, 5 * time.Millisecond, 10, http.StatusOK},
		{&url.Values{"delay": {"0h"}}, 0, 10, http.StatusOK},

		// or floating point seconds
		{&url.Values{"duration": {"0.25"}}, 250 * time.Millisecond, 10, http.StatusOK},
		{&url.Values{"duration": {"0"}}, 0, 10, http.StatusOK},
		{&url.Values{"duration": {"1"}}, 1 * time.Second, 10, http.StatusOK},
		{&url.Values{"delay": {"0.25"}}, 250 * time.Millisecond, 10, http.StatusOK},
		{&url.Values{"delay": {"0"}}, 0, 10, http.StatusOK},

		{&url.Values{"numbytes": {"1"}}, 0, 1, http.StatusOK},
		{&url.Values{"numbytes": {"101"}}, 0, 101, http.StatusOK},
		{&url.Values{"numbytes": {fmt.Sprintf("%d", maxBodySize)}}, 0, int(maxBodySize), http.StatusOK},

		{&url.Values{"code": {"100"}}, 0, 10, 100},
		{&url.Values{"code": {"404"}}, 0, 10, 404},
		{&url.Values{"code": {"599"}}, 0, 10, 599},
		{&url.Values{"code": {"567"}}, 0, 10, 567},

		{&url.Values{"duration": {"750ms"}, "delay": {"250ms"}}, 1 * time.Second, 10, http.StatusOK},
		{&url.Values{"duration": {"250ms"}, "delay": {"0.25s"}}, 500 * time.Millisecond, 10, http.StatusOK},
	}
	for _, test := range okTests {
		t.Run(fmt.Sprintf("ok/%s", test.params.Encode()), func(t *testing.T) {
			url := "/drip?" + test.params.Encode()

			start := time.Now()

			r, _ := http.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			elapsed := time.Now().Sub(start)

			assertHeader(t, w, "Content-Type", "application/octet-stream")
			assertStatusCode(t, w, test.code)
			if len(w.Body.Bytes()) != test.numbytes {
				t.Fatalf("expected %d bytes, got %d", test.numbytes, len(w.Body.Bytes()))
			}

			if elapsed < test.duration {
				t.Fatalf("expected minimum duration of %s, request took %s", test.duration, elapsed)
			}
		})
	}

	t.Run("handle cancelation during initial delay", func(t *testing.T) {
		srv := httptest.NewServer(handler)
		defer srv.Close()

		client := http.Client{
			Timeout: time.Duration(10 * time.Millisecond),
		}
		resp, err := client.Get(srv.URL + "/drip?duration=500ms&delay=500ms")
		if err == nil {
			body, _ := ioutil.ReadAll(resp.Body)
			t.Fatalf("expected timeout error, got %d %s", resp.StatusCode, body)
		}
	})

	t.Run("handle cancelation during drip", func(t *testing.T) {
		srv := httptest.NewServer(handler)
		defer srv.Close()

		client := http.Client{
			Timeout: time.Duration(250 * time.Millisecond),
		}
		resp, err := client.Get(srv.URL + "/drip?duration=900ms&delay=100ms")
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		// in this case, the timeout happens while trying to read the body
		body, err := ioutil.ReadAll(resp.Body)
		if err == nil {
			t.Fatal("expected timeout reading body")
		}

		// but we should have received a partial response
		assertBytesEqual(t, body, []byte("**"))
	})

	var badTests = []struct {
		params *url.Values
		code   int
	}{
		{&url.Values{"duration": {"1m"}}, http.StatusBadRequest},
		{&url.Values{"duration": {"-1ms"}}, http.StatusBadRequest},
		{&url.Values{"duration": {"1001"}}, http.StatusBadRequest},
		{&url.Values{"duration": {"-1"}}, http.StatusBadRequest},
		{&url.Values{"duration": {"foo"}}, http.StatusBadRequest},

		{&url.Values{"delay": {"1m"}}, http.StatusBadRequest},
		{&url.Values{"delay": {"-1ms"}}, http.StatusBadRequest},
		{&url.Values{"delay": {"1001"}}, http.StatusBadRequest},
		{&url.Values{"delay": {"-1"}}, http.StatusBadRequest},
		{&url.Values{"delay": {"foo"}}, http.StatusBadRequest},

		{&url.Values{"numbytes": {"foo"}}, http.StatusBadRequest},
		{&url.Values{"numbytes": {"0"}}, http.StatusBadRequest},
		{&url.Values{"numbytes": {"-1"}}, http.StatusBadRequest},
		{&url.Values{"numbytes": {"0xff"}}, http.StatusBadRequest},
		{&url.Values{"numbytes": {fmt.Sprintf("%d", maxBodySize+1)}}, http.StatusBadRequest},

		{&url.Values{"code": {"foo"}}, http.StatusBadRequest},
		{&url.Values{"code": {"-1"}}, http.StatusBadRequest},
		{&url.Values{"code": {"25"}}, http.StatusBadRequest},
		{&url.Values{"code": {"600"}}, http.StatusBadRequest},

		// request would take too long
		{&url.Values{"duration": {"750ms"}, "delay": {"500ms"}}, http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run(fmt.Sprintf("bad/%s", test.params.Encode()), func(t *testing.T) {
			url := "/drip?" + test.params.Encode()

			r, _ := http.NewRequest("GET", url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.code)
		})
	}
}

func TestRange(t *testing.T) {
	t.Run("ok_no_range", func(t *testing.T) {
		url := "/range/1234"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertHeader(t, w, "ETag", "range1234")
		assertHeader(t, w, "Accept-Ranges", "bytes")
		assertHeader(t, w, "Content-Length", "1234")
		assertContentType(t, w, "text/plain; charset=utf-8")

		if len(w.Body.String()) != 1234 {
			t.Errorf("expected content length 1234, got %d", len(w.Body.String()))
		}
	})

	t.Run("ok_range", func(t *testing.T) {
		url := "/range/100"
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Add("Range", "bytes=10-24")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusPartialContent)
		assertHeader(t, w, "ETag", "range100")
		assertHeader(t, w, "Accept-Ranges", "bytes")
		assertHeader(t, w, "Content-Length", "15")
		assertHeader(t, w, "Content-Range", "bytes 10-24/100")
		assertBodyEquals(t, w, "klmnopqrstuvwxy")
	})

	t.Run("ok_range_first_16_bytes", func(t *testing.T) {
		url := "/range/1000"
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Add("Range", "bytes=0-15")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusPartialContent)
		assertHeader(t, w, "ETag", "range1000")
		assertHeader(t, w, "Accept-Ranges", "bytes")
		assertHeader(t, w, "Content-Length", "16")
		assertHeader(t, w, "Content-Range", "bytes 0-15/1000")
		assertBodyEquals(t, w, "abcdefghijklmnop")
	})

	t.Run("ok_range_open_ended_last_6_bytes", func(t *testing.T) {
		url := "/range/26"
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Add("Range", "bytes=20-")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusPartialContent)
		assertHeader(t, w, "ETag", "range26")
		assertHeader(t, w, "Content-Length", "6")
		assertHeader(t, w, "Content-Range", "bytes 20-25/26")
		assertBodyEquals(t, w, "uvwxyz")
	})

	t.Run("ok_range_suffix", func(t *testing.T) {
		url := "/range/26"
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Add("Range", "bytes=-5")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		t.Logf("headers = %v", w.HeaderMap)
		assertStatusCode(t, w, http.StatusPartialContent)
		assertHeader(t, w, "ETag", "range26")
		assertHeader(t, w, "Content-Length", "5")
		assertHeader(t, w, "Content-Range", "bytes 21-25/26")
		assertBodyEquals(t, w, "vwxyz")
	})

	t.Run("err_range_out_of_bounds", func(t *testing.T) {
		url := "/range/26"
		r, _ := http.NewRequest("GET", url, nil)
		r.Header.Add("Range", "bytes=-5")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusPartialContent)
		assertHeader(t, w, "ETag", "range26")
		assertHeader(t, w, "Content-Length", "5")
		assertHeader(t, w, "Content-Range", "bytes 21-25/26")
		assertBodyEquals(t, w, "vwxyz")
	})

	// Note: httpbin rejects these requests with invalid range headers, but the
	// go stdlib just ignores them.
	var badRangeTests = []struct {
		url         string
		rangeHeader string
	}{
		{"/range/26", "bytes=10-5"},
		{"/range/26", "bytes=32-40"},
		{"/range/26", "bytes=0-40"},
	}
	for _, test := range badRangeTests {
		t.Run(fmt.Sprintf("ok_bad_range_header/%s", test.rangeHeader), func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, http.StatusOK)
			assertBodyEquals(t, w, "abcdefghijklmnopqrstuvwxyz")
		})
	}

	var badTests = []struct {
		url  string
		code int
	}{
		{"/range/1/foo", http.StatusNotFound},

		{"/range/", http.StatusBadRequest},
		{"/range/foo", http.StatusBadRequest},
		{"/range/1.5", http.StatusBadRequest},
		{"/range/-1", http.StatusBadRequest},
	}

	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.code)
		})
	}
}

func TestHTML(t *testing.T) {
	r, _ := http.NewRequest("GET", "/html", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, htmlContentType)
	assertBodyContains(t, w, `<h1>Herman Melville - Moby-Dick</h1>`)
}

func TestRobots(t *testing.T) {
	r, _ := http.NewRequest("GET", "/robots.txt", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/plain")
	assertBodyContains(t, w, `Disallow: /deny`)
}

func TestDeny(t *testing.T) {
	r, _ := http.NewRequest("GET", "/deny", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "text/plain")
	assertBodyContains(t, w, `YOU SHOULDN'T BE HERE`)
}

func TestCache(t *testing.T) {
	t.Run("ok_no_cache", func(t *testing.T) {
		url := "/cache"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, jsonContentType)

		lastModified := w.Header().Get("Last-Modified")
		if lastModified == "" {
			t.Fatalf("did get Last-Modified header")
		}

		etag := w.Header().Get("ETag")
		if etag != sha1hash(lastModified) {
			t.Fatalf("expected ETag header %v, got %v", sha1hash(lastModified), etag)
		}

		var resp *getResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		if err != nil {
			t.Fatalf("failed to unmarshal body %s from JSON: %s", w.Body, err)
		}
	})

	var tests = []struct {
		headerKey string
		headerVal string
	}{
		{"If-None-Match", "my-custom-etag"},
		{"If-Modified-Since", "my-custom-date"},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("ok_cache/%s", test.headerKey), func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/cache", nil)
			r.Header.Add(test.headerKey, test.headerVal)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, http.StatusNotModified)
		})
	}
}

func TestCacheControl(t *testing.T) {
	t.Run("ok_cache_control", func(t *testing.T) {
		url := "/cache/60"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, jsonContentType)
		assertHeader(t, w, "Cache-Control", "public, max-age=60")
	})

	var badTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/cache/60/foo", http.StatusNotFound},
		{"/cache/foo", http.StatusBadRequest},
		{"/cache/3.14", http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestETag(t *testing.T) {
	t.Run("ok_no_headers", func(t *testing.T) {
		url := "/etag/abc"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		assertStatusCode(t, w, http.StatusOK)
		assertHeader(t, w, "ETag", `"abc"`)
	})

	var tests = []struct {
		name           string
		etag           string
		headerKey      string
		headerVal      string
		expectedStatus int
	}{
		{"if_none_match_matches", "abc", "If-None-Match", `"abc"`, http.StatusNotModified},
		{"if_none_match_matches_list", "abc", "If-None-Match", `"123", "abc"`, http.StatusNotModified},
		{"if_none_match_matches_star", "abc", "If-None-Match", "*", http.StatusNotModified},
		{"if_none_match_matches_w_prefix", "c3piozzzz", "If-None-Match", `W/"xyzzy", W/"r2d2xxxx", W/"c3piozzzz"`, http.StatusNotModified},
		{"if_none_match_has_no_match", "abc", "If-None-Match", `"123"`, http.StatusOK},

		{"if_match_matches", "abc", "If-Match", `"abc"`, http.StatusOK},
		{"if_match_matches_list", "abc", "If-Match", `"123", "abc"`, http.StatusOK},
		{"if_match_matches_star", "abc", "If-Match", "*", http.StatusOK},
		{"if_match_has_no_match", "abc", "If-Match", `"xxxxxx"`, http.StatusPreconditionFailed},
	}
	for _, test := range tests {
		t.Run("ok_"+test.name, func(t *testing.T) {
			url := "/etag/" + test.etag
			r, _ := http.NewRequest("GET", url, nil)
			r.Header.Add(test.headerKey, test.headerVal)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.expectedStatus)
		})
	}

	var badTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/etag/foo/bar", http.StatusNotFound},
	}
	for _, test := range badTests {
		t.Run(fmt.Sprintf("bad/%s", test.url), func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestBytes(t *testing.T) {
	t.Run("ok_no_seed", func(t *testing.T) {
		url := "/bytes/1024"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, "application/octet-stream")
		if len(w.Body.String()) != 1024 {
			t.Errorf("expected content length 1024, got %d", len(w.Body.String()))
		}
	})

	t.Run("ok_seed", func(t *testing.T) {
		url := "/bytes/16?seed=1234567890"
		r, _ := http.NewRequest("GET", url, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		assertStatusCode(t, w, http.StatusOK)
		assertContentType(t, w, "application/octet-stream")

		bodyHex := fmt.Sprintf("%x", w.Body.Bytes())
		wantHex := "bfcd2afa15a2b372c707985a22024a8e"
		if bodyHex != wantHex {
			t.Errorf("expected body in hexadecimal = %v, got %v", wantHex, bodyHex)
		}
	})

	var edgeCaseTests = []struct {
		url                   string
		expectedContentLength int
	}{
		{"/bytes/-1", 1},
		{"/bytes/99999999", 100 * 1024},

		// negative seed allowed
		{"/bytes/16?seed=-12345", 16},
	}
	for _, test := range edgeCaseTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, http.StatusOK)
			assertHeader(t, w, "Content-Length", fmt.Sprintf("%d", test.expectedContentLength))
			if len(w.Body.Bytes()) != test.expectedContentLength {
				t.Errorf("expected body of length %d, got %d", test.expectedContentLength, len(w.Body.Bytes()))
			}
		})
	}

	var badTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/bytes", http.StatusNotFound},
		{"/bytes/16/foo", http.StatusNotFound},

		{"/bytes/foo", http.StatusBadRequest},
		{"/bytes/3.14", http.StatusBadRequest},

		{"/bytes/16?seed=12345678901234567890", http.StatusBadRequest}, // seed too big
		{"/bytes/16?seed=foo", http.StatusBadRequest},
		{"/bytes/16?seed=3.14", http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestStreamBytes(t *testing.T) {
	var okTests = []struct {
		url                   string
		expectedContentLength int
	}{
		{"/stream-bytes/256", 256},
		{"/stream-bytes/256?chunk_size=1", 256},
		{"/stream-bytes/256?chunk_size=256", 256},
		{"/stream-bytes/256?chunk_size=7", 256},

		// too-large chunk size is okay
		{"/stream-bytes/256?chunk_size=512", 256},

		// as is negative chunk size
		{"/stream-bytes/256?chunk_size=-10", 256},
	}
	for _, test := range okTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			// TODO: The stdlib seems to automagically unchunk these responses
			// and I'm not quite sure how to test this:
			//
			//     assertHeader(t, w, "Transfer-Encoding", "chunked")
			//
			// Instead, we assert that we got no Content-Length header, which
			// is an indication that the Go stdlib streamed the response.
			assertHeader(t, w, "Content-Length", "")

			if len(w.Body.Bytes()) != test.expectedContentLength {
				t.Fatalf("expected body of length %d, got %d", test.expectedContentLength, len(w.Body.Bytes()))
			}
		})
	}

	var badTests = []struct {
		url  string
		code int
	}{
		{"/stream-bytes", http.StatusNotFound},
		{"/stream-bytes/10/foo", http.StatusNotFound},

		{"/stream-bytes/foo", http.StatusBadRequest},
		{"/stream-bytes/3.1415", http.StatusBadRequest},

		{"/stream-bytes/16?chunk_size=foo", http.StatusBadRequest},
		{"/stream-bytes/16?chunk_size=3.14", http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.code)
		})
	}
}

func TestLinks(t *testing.T) {
	var redirectTests = []struct {
		url              string
		expectedLocation string
	}{
		{"/links/1", "/links/1/0"},
		{"/links/100", "/links/100/0"},
	}

	for _, test := range redirectTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, http.StatusFound)
			assertHeader(t, w, "Location", test.expectedLocation)
		})
	}

	var errorTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/links/10/1/foo", http.StatusNotFound},

		// invalid N
		{"/links/3.14", http.StatusBadRequest},
		{"/links/-1", http.StatusBadRequest},
		{"/links/257", http.StatusBadRequest},

		// invalid offset
		{"/links/1/3.14", http.StatusBadRequest},
		{"/links/1/foo", http.StatusBadRequest},
	}

	for _, test := range errorTests {
		t.Run("error"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, test.expectedStatus)
		})
	}

	var linksPageTests = []struct {
		url             string
		expectedContent string
	}{
		{"/links/2/0", `<html><head><title>Links</title></head><body>0 <a href="/links/2/1">1</a> </body></html>`},
		{"/links/2/1", `<html><head><title>Links</title></head><body><a href="/links/2/0">0</a> 1 </body></html>`},

		// offsets too large and too small are ignored
		{"/links/2/2", `<html><head><title>Links</title></head><body><a href="/links/2/0">0</a> <a href="/links/2/1">1</a> </body></html>`},
		{"/links/2/10", `<html><head><title>Links</title></head><body><a href="/links/2/0">0</a> <a href="/links/2/1">1</a> </body></html>`},
		{"/links/2/-1", `<html><head><title>Links</title></head><body><a href="/links/2/0">0</a> <a href="/links/2/1">1</a> </body></html>`},
	}
	for _, test := range linksPageTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, http.StatusOK)
			assertContentType(t, w, htmlContentType)
			assertBodyEquals(t, w, test.expectedContent)
		})
	}
}

func TestImage(t *testing.T) {
	var acceptTests = []struct {
		acceptHeader        string
		expectedContentType string
		expectedStatus      int
	}{
		{"", "image/png", http.StatusOK},
		{"image/*", "image/png", http.StatusOK},
		{"image/png", "image/png", http.StatusOK},
		{"image/jpeg", "image/jpeg", http.StatusOK},
		{"image/webp", "image/webp", http.StatusOK},
		{"image/svg+xml", "image/svg+xml", http.StatusOK},

		{"image/raw", "", http.StatusUnsupportedMediaType},
		{"image/jpg", "", http.StatusUnsupportedMediaType},
		{"image/svg", "", http.StatusUnsupportedMediaType},
	}

	for _, test := range acceptTests {
		t.Run("ok/accept="+test.acceptHeader, func(t *testing.T) {
			r, _ := http.NewRequest("GET", "/image", nil)
			r.Header.Set("Accept", test.acceptHeader)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			assertStatusCode(t, w, test.expectedStatus)
			if test.expectedContentType != "" {
				assertContentType(t, w, test.expectedContentType)
			}
		})
	}

	var imageTests = []struct {
		url            string
		expectedStatus int
	}{
		{"/image/png", http.StatusOK},
		{"/image/jpeg", http.StatusOK},
		{"/image/webp", http.StatusOK},
		{"/image/svg", http.StatusOK},

		{"/image/raw", http.StatusNotFound},
		{"/image/jpg", http.StatusNotFound},
		{"/image/png/foo", http.StatusNotFound},
	}

	for _, test := range imageTests {
		t.Run("error"+test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, test.expectedStatus)
		})
	}
}

func TestXML(t *testing.T) {
	r, _ := http.NewRequest("GET", "/xml", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assertContentType(t, w, "application/xml")
	assertBodyContains(t, w, `<?xml version='1.0' encoding='us-ascii'?>`)
}

func TestNotImplemented(t *testing.T) {
	var tests = []struct {
		url string
	}{
		{"/brotli"},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			r, _ := http.NewRequest("GET", test.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assertStatusCode(t, w, http.StatusNotImplemented)
		})
	}
}
