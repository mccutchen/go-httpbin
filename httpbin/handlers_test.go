package httpbin

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	maxBodySize int64         = 1024
	maxDuration time.Duration = 1 * time.Second
)

// "Global" test app, server, & client to be reused across test cases.
// Initialized in TestMain.
var (
	app    *HTTPBin
	srv    *httptest.Server
	client *http.Client
)

func TestMain(m *testing.M) {
	app = New(
		WithAllowedRedirectDomains([]string{
			"httpbingo.org",
			"example.org",
			"www.example.com",
		}),
		WithDefaultParams(DefaultParams{
			DripDelay:    0,
			DripDuration: 100 * time.Millisecond,
			DripNumBytes: 10,
		}),
		WithMaxBodySize(maxBodySize),
		WithMaxDuration(maxDuration),
		WithObserver(StdLogObserver(log.New(io.Discard, "", 0))),
	)
	srv, client = newTestServer(app)
	defer srv.Close()
	os.Exit(m.Run())
}

func TestIndex(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/")
		resp := mustDoReq(t, req)

		assertContentType(t, resp, htmlContentType)
		assertHeader(t, resp, "Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
		assertBodyContains(t, resp, "go-httpbin")
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/foo")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusNotFound)
		assertBodyContains(t, resp, "/foo")
	})
}

func TestFormsPost(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/forms/post")
	resp := mustDoReq(t, req)

	assertContentType(t, resp, htmlContentType)
	assertBodyContains(t, resp, `<form method="post" action="/post">`)
}

func TestUTF8(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/encoding/utf8")
	resp := mustDoReq(t, req)

	assertContentType(t, resp, htmlContentType)
	assertBodyContains(t, resp, `Hello world, Καλημέρα κόσμε, コンニチハ`)
}

func TestGet(t *testing.T) {
	doGetRequest := func(t *testing.T, path string, params *url.Values, headers *http.Header) noBodyResponse {
		t.Helper()

		if params != nil {
			path = fmt.Sprintf("%s?%s", path, params.Encode())
		}
		req := newTestRequest(t, "GET", path)
		req.Header.Set("User-Agent", "test")
		if headers != nil {
			for k, vs := range *headers {
				for _, v := range vs {
					req.Header.Set(k, v)
				}
			}
		}

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		var result noBodyResponse
		mustUnmarshal(t, resp.Body, &result)

		return result
	}

	t.Run("basic", func(t *testing.T) {
		t.Parallel()

		result := doGetRequest(t, "/get", nil, nil)
		if result.Args.Encode() != "" {
			t.Fatalf("expected empty args, got %s", result.Args.Encode())
		}
		if result.Method != "GET" {
			t.Fatalf("expected method to be GET, got %s", result.Method)
		}
		if !strings.HasPrefix(result.Origin, "127.0.0.1") {
			t.Fatalf("expected 127.0.0.1 origin, got %q", result.Origin)
		}
		if result.URL != srv.URL+"/get" {
			t.Fatalf("unexpected url: %#v", result.URL)
		}

		wantHeaders := map[string]string{
			"Content-Type": "",
			"User-Agent":   "test",
		}
		for key, val := range wantHeaders {
			if result.Headers.Get(key) != val {
				t.Fatalf("expected %s = %#v, got %#v", key, val, result.Headers.Get(key))
			}
		}
	})

	t.Run("with_query_params", func(t *testing.T) {
		t.Parallel()

		params := &url.Values{}
		params.Set("foo", "foo")
		params.Add("bar", "bar1")
		params.Add("bar", "bar2")

		result := doGetRequest(t, "/get", params, nil)
		if result.Args.Encode() != params.Encode() {
			t.Fatalf("args mismatch: %s != %s", result.Args.Encode(), params.Encode())
		}
		if result.Method != "GET" {
			t.Fatalf("expected method to be GET, got %s", result.Method)
		}
	})

	t.Run("only_allows_gets", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "POST", "/get")
		resp := mustDoReq(t, req)

		assertStatusCode(t, resp, http.StatusMethodNotAllowed)
		assertContentType(t, resp, "text/plain; charset=utf-8")
	})

	protoTests := []struct {
		key   string
		value string
	}{
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Protocol", "https"},
		{"X-Forwarded-Ssl", "on"},
	}
	for _, test := range protoTests {
		test := test
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()
			headers := &http.Header{}
			headers.Set(test.key, test.value)
			result := doGetRequest(t, "/get", nil, headers)
			if !strings.HasPrefix(result.URL, "https://") {
				t.Fatalf("%s=%s should result in https URL", test.key, test.value)
			}
		})
	}
}

func TestHead(t *testing.T) {
	testCases := []struct {
		verb     string
		path     string
		wantCode int
	}{
		{"HEAD", "/", http.StatusOK},
		{"HEAD", "/get", http.StatusOK},
		{"HEAD", "/head", http.StatusOK},
		{"HEAD", "/post", http.StatusMethodNotAllowed},
		{"GET", "/head", http.StatusMethodNotAllowed},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run(fmt.Sprintf("%s %s", tc.verb, tc.path), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, tc.verb, tc.path)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, tc.wantCode)

			// we only do further validation when we get an OK response
			if tc.wantCode != http.StatusOK {
				return
			}

			assertStatusCode(t, resp, http.StatusOK)
			assertBodyEquals(t, resp, "")
			if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
				t.Fatalf("did not expect Content-Length in response to HEAD request")
			}
		})
	}
}

func TestCORS(t *testing.T) {
	t.Run("CORS/no_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/get")
		resp := mustDoReq(t, req)
		assertHeader(t, resp, "Access-Control-Allow-Origin", "*")
	})

	t.Run("CORS/with_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/get")
		req.Header.Set("Origin", "origin")
		resp := mustDoReq(t, req)
		assertHeader(t, resp, "Access-Control-Allow-Origin", "origin")
	})

	t.Run("CORS/options_request", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "OPTIONS", "/get")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, 200)

		headerTests := []struct {
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
			assertHeader(t, resp, test.key, test.expected)
		}
	})

	t.Run("CORS/allow_headers", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "OPTIONS", "/get")
		req.Header.Set("Access-Control-Request-Headers", "X-Test-Header")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, 200)

		headerTests := []struct {
			key      string
			expected string
		}{
			{"Access-Control-Allow-Headers", "X-Test-Header"},
		}
		for _, test := range headerTests {
			assertHeader(t, resp, test.key, test.expected)
		}
	})
}

func TestIP(t *testing.T) {
	testCases := map[string]struct {
		remoteAddr string
		headers    map[string]string
		wantOrigin string
	}{
		"remote addr used if no x-forwarded-for": {
			remoteAddr: "192.168.0.100",
			wantOrigin: "192.168.0.100",
		},
		"remote addr used if x-forwarded-for empty": {
			remoteAddr: "192.168.0.100",
			headers:    map[string]string{"X-Forwarded-For": ""},
			wantOrigin: "192.168.0.100",
		},
		"first entry in x-forwarded-for used if present": {
			remoteAddr: "192.168.0.100",
			headers:    map[string]string{"X-Forwarded-For": "10.1.1.1, 10.2.2.2, 10.3.3.3"},
			wantOrigin: "10.1.1.1",
		},
		"single entry x-forwarded-for ok": {
			remoteAddr: "192.168.0.100",
			headers:    map[string]string{"X-Forwarded-For": "10.1.1.1"},
			wantOrigin: "10.1.1.1",
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req, _ := http.NewRequest("GET", "/ip", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			// this test does not use a real server, because we need to control
			// the RemoteAddr field on the request object to make the test
			// deterministic.
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("wanted status code %d, got %d", http.StatusOK, w.Code)
			}

			if ct := w.Header().Get("Content-Type"); ct != jsonContentType {
				t.Errorf("expected content type %q, got %q", jsonContentType, ct)
			}

			var result ipResponse
			if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
				t.Fatalf("failed to unmarshal app response from JSON: %s", err)
			}

			if result.Origin != tc.wantOrigin {
				t.Fatalf("got %q, want %q", result.Origin, tc.wantOrigin)
			}
		})
	}
}

func TestUserAgent(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/user-agent")
	req.Header.Set("User-Agent", "test")

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result userAgentResponse
	err := json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		t.Fatalf("failed to unmarshal app response from JSON: %s", err)
	}

	if result.UserAgent != "test" {
		t.Fatalf("%#v != \"test\"", result.UserAgent)
	}
}

func TestHeaders(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/headers")
	req.Host = "test-host"
	req.Header.Set("User-Agent", "test")
	req.Header.Set("Foo-Header", "foo")
	req.Header.Add("Bar-Header", "bar1")
	req.Header.Add("Bar-Header", "bar2")

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result headersResponse
	mustUnmarshal(t, resp.Body, &result)

	// Host header requires special treatment, because its an attribute of the
	// http.Request struct itself, not part of its headers map
	host := result.Headers[http.CanonicalHeaderKey("Host")]
	if host == nil || host[0] != "test-host" {
		t.Fatalf("expected Host header \"test-host\", got %#v", host)
	}

	for k, expectedValues := range req.Header {
		values, ok := result.Headers[http.CanonicalHeaderKey(k)]
		if !ok {
			t.Fatalf("expected header %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("header %s value mismatch: %#v != %#v", k, values, expectedValues)
		}
	}
}

func TestPost(t *testing.T) {
	testRequestWithBody(t, "POST", "/post")
}

func TestPut(t *testing.T) {
	testRequestWithBody(t, "PUT", "/put")
}

func TestDelete(t *testing.T) {
	testRequestWithBody(t, "DELETE", "/delete")
}

func TestPatch(t *testing.T) {
	testRequestWithBody(t, "PATCH", "/patch")
}

func TestAnything(t *testing.T) {
	var (
		verbs = []string{
			"GET",
			"DELETE",
			"PATCH",
			"POST",
			"PUT",
		}
		paths = []string{
			"/anything",
			"/anything/else",
		}
	)
	for _, path := range paths {
		for _, verb := range verbs {
			testRequestWithBody(t, verb, path)
		}
	}

	t.Run("HEAD", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "HEAD", "/anything")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertBodyEquals(t, resp, "")
		if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
			t.Fatalf("did not expect Content-Length in response to HEAD request")
		}
	})
}

func testRequestWithBody(t *testing.T, verb, path string) {
	// getFuncName uses runtime type reflection to get the name of the given
	// function.
	//
	// Cribbed from https://stackoverflow.com/a/70535822/151221
	getFuncName := func(f interface{}) string {
		parts := strings.Split((runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()), ".")
		return parts[len(parts)-1]
	}

	// getTestName expects a function named like testRequestWithBody__BodyTooBig
	// and returns only the trailing BodyTooBig part.
	getTestName := func(prefix string, f interface{}) string {
		name := strings.TrimPrefix(getFuncName(f), "testRequestWithBody")
		return fmt.Sprintf("%s/%s", prefix, name)
	}

	type testFunc func(t *testing.T, verb, path string)
	testFuncs := []testFunc{
		testRequestWithBodyBinaryBody,
		testRequestWithBodyBodyTooBig,
		testRequestWithBodyEmptyBody,
		testRequestWithBodyFormEncodedBody,
		testRequestWithBodyFormEncodedBodyNoContentType,
		testRequestWithBodyHTML,
		testRequestWithBodyInvalidFormEncodedBody,
		testRequestWithBodyInvalidJSON,
		testRequestWithBodyInvalidMultiPartBody,
		testRequestWithBodyJSON,
		testRequestWithBodyMultiPartBody,
		testRequestWithBodyMultiPartBodyFiles,
		testRequestWithBodyQueryParams,
		testRequestWithBodyQueryParamsAndBody,
		testRequestWithBodyTransferEncoding,
	}
	for _, testFunc := range testFuncs {
		testFunc := testFunc
		t.Run(getTestName(verb, testFunc), func(t *testing.T) {
			t.Parallel()
			testFunc(t, verb, path)
		})
	}
}

func testRequestWithBodyBinaryBody(t *testing.T, verb string, path string) {
	tests := []struct {
		contentType string
		requestBody string
	}{
		{"application/octet-stream", "encodeMe"},
		{"image/png", "encodeMe-png"},
		{"image/webp", "encodeMe-webp"},
		{"image/jpeg", "encodeMe-jpeg"},
		{"unknown", "encodeMe-unknown"},
	}
	for _, test := range tests {
		test := test
		t.Run("content type/"+test.contentType, func(t *testing.T) {
			t.Parallel()

			req := newTestRequestWithBody(t, verb, path, bytes.NewReader([]byte(test.requestBody)))
			req.Header.Set("Content-Type", test.contentType)

			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)
			assertContentType(t, resp, jsonContentType)

			var result bodyResponse
			mustUnmarshal(t, resp.Body, &result)

			expected := "data:" + test.contentType + ";base64," + base64.StdEncoding.EncodeToString([]byte(test.requestBody))
			if result.Data != expected {
				t.Fatalf("expected binary encoded response data: %#v got %#v", expected, result.Data)
			}
			if result.JSON != nil {
				t.Fatalf("expected nil response json, got %#v", result.JSON)
			}

			if len(result.Args) > 0 {
				t.Fatalf("expected no query params, got %#v", result.Args)
			}
			if result.Method != verb {
				t.Fatalf("expected method to be %s, got %s", verb, result.Method)
			}
			if len(result.Form) > 0 {
				t.Fatalf("expected no form data, got %#v", result.Form)
			}
		})
	}
}

func testRequestWithBodyEmptyBody(t *testing.T, verb string, path string) {
	tests := []struct {
		contentType string
	}{
		{""},
		{"application/json; charset=utf-8"},
		{"application/x-www-form-urlencoded"},
		{"multipart/form-data; foo"},
	}
	for _, test := range tests {
		test := test
		t.Run("content type/"+test.contentType, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, verb, path)
			req.Header.Set("Content-Type", test.contentType)
			resp := mustDoReq(t, req)

			assertStatusCode(t, resp, http.StatusOK)
			assertContentType(t, resp, jsonContentType)

			var result bodyResponse
			mustUnmarshal(t, resp.Body, &result)

			if result.Data != "" {
				t.Fatalf("expected empty response data, got %#v", result.Data)
			}
			if result.JSON != nil {
				t.Fatalf("expected nil response json, got %#v", result.JSON)
			}

			if len(result.Args) > 0 {
				t.Fatalf("expected no query params, got %#v", result.Args)
			}
			if result.Method != verb {
				t.Fatalf("expected method to be %s, got %s", verb, result.Method)
			}
			if len(result.Form) > 0 {
				t.Fatalf("expected no form data, got %#v", result.Form)
			}
		})
	}
}

func testRequestWithBodyFormEncodedBody(t *testing.T, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequestWithBody(t, verb, path, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := mustDoReq(t, req)

	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if len(result.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", result.Args)
	}
	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}
	if len(result.Form) != len(params) {
		t.Fatalf("expected %d form values, got %d", len(params), len(result.Form))
	}
	for k, expectedValues := range params {
		values, ok := result.Form[k]
		if !ok {
			t.Fatalf("expected form field %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("form value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

func testRequestWithBodyHTML(t *testing.T, verb, path string) {
	data := "<html><body><h1>hello world</h1></body></html>"

	req := newTestRequestWithBody(t, verb, path, strings.NewReader(data))
	req.Header.Set("Content-Type", htmlContentType)

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	// We do not use json.Unmarshal here which would unescape any escaped characters.
	// For httpbin compatibility, we need to verify the data is returned as-is without
	// escaping.
	respBody := mustReadAll(t, resp.Body)
	if !strings.Contains(respBody, data) {
		t.Fatalf("substring %q not found in response body %q", data, respBody)
	}
}

func testRequestWithBodyFormEncodedBodyNoContentType(t *testing.T, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequestWithBody(t, verb, path, strings.NewReader(params.Encode()))
	resp := mustDoReq(t, req)

	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if len(result.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", result.Args)
	}
	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}
	if len(result.Form) != 0 {
		t.Fatalf("expected no form values, got %d", len(result.Form))
	}
	// Because we did not set an content type, httpbin will return the base64 encoded data.
	expectedBody := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString([]byte(params.Encode()))
	if string(result.Data) != expectedBody {
		t.Fatalf("response data mismatch, %#v != %#v", string(result.Data), expectedBody)
	}
}

func testRequestWithBodyMultiPartBody(t *testing.T, verb, path string) {
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

	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if len(result.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", result.Args)
	}
	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}
	if len(result.Form) != len(params) {
		t.Fatalf("expected %d form values, got %d", len(params), len(result.Form))
	}
	for k, expectedValues := range params {
		values, ok := result.Form[k]
		if !ok {
			t.Fatalf("expected form field %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("form value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

func testRequestWithBodyMultiPartBodyFiles(t *testing.T, verb, path string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Add a file to the multipart request
	part, _ := mw.CreateFormFile("fieldname", "filename")
	part.Write([]byte("hello world"))
	mw.Close()

	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if len(result.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", result.Args)
	}

	// verify that the file we added is present in the `files` attribute of the
	// response, with the field as key and content as value
	wantFiles := map[string][]string{
		"fieldname": {"hello world"},
	}
	if !reflect.DeepEqual(result.Files, wantFiles) {
		t.Fatalf("want resp.Files = %#v, got %#v", wantFiles, result.Files)
	}

	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}
}

func testRequestWithBodyInvalidFormEncodedBody(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyInvalidMultiPartBody(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "multipart/form-data; etc")
	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyJSON(t *testing.T, verb, path string) {
	type testInput struct {
		Foo  string
		Bar  int
		Baz  []float64
		Quux map[int]string
	}
	input := testInput{
		Foo:  "foo",
		Bar:  123,
		Baz:  []float64{1.0, 1.1, 1.2},
		Quux: map[int]string{1: "one", 2: "two", 3: "three"},
	}
	inputBody, _ := json.Marshal(input)

	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(inputBody))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if result.Data != string(inputBody) {
		t.Fatalf("expected data == %#v, got %#v", string(inputBody), result.Data)
	}
	if len(result.Args) > 0 {
		t.Fatalf("expected no query params, got %#v", result.Args)
	}
	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}
	if len(result.Form) != 0 {
		t.Fatalf("expected no form values, got %d", len(result.Form))
	}

	// Need to re-marshall just the JSON field from the response in order to
	// re-unmarshall it into our expected type
	roundTrippedInputBytes, err := json.Marshal(result.JSON)
	assertNilError(t, err)

	var roundTrippedInput testInput
	if err := json.Unmarshal(roundTrippedInputBytes, &roundTrippedInput); err != nil {
		t.Fatalf("failed to round-trip JSON: coult not re-unmarshal JSON: %s", err)
	}

	if !reflect.DeepEqual(input, roundTrippedInput) {
		t.Fatalf("failed to round-trip JSON: %#v != %#v", roundTrippedInput, input)
	}
}

func testRequestWithBodyInvalidJSON(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("foo"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyBodyTooBig(t *testing.T, verb, path string) {
	body := make([]byte, maxBodySize+1)
	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body))
	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyQueryParams(t *testing.T, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequest(t, verb, fmt.Sprintf("%s?%s", path, params.Encode()))
	resp := mustDoReq(t, req)

	t.Logf("request:  %s %s", verb, req.URL)
	t.Logf("response: %s %v", resp.Status, resp.Header)

	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if result.Args.Encode() != params.Encode() {
		t.Fatalf("expected args = %#v in response, got %#v", params.Encode(), result.Args.Encode())
	}

	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}

	if len(result.Form) > 0 {
		t.Fatalf("expected form data, got %#v", result.Form)
	}
}

func testRequestWithBodyQueryParamsAndBody(t *testing.T, verb, path string) {
	args := url.Values{}
	args.Set("query1", "foo")
	args.Add("query2", "bar1")
	args.Add("query2", "bar2")

	form := url.Values{}
	form.Set("form1", "foo")
	form.Add("form2", "bar1")
	form.Add("form2", "bar2")

	url := fmt.Sprintf("%s?%s", path, args.Encode())
	req := newTestRequestWithBody(t, verb, url, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := mustDoReq(t, req)

	t.Logf("request:  %s %s", verb, url)
	t.Logf("response: %s %v", resp.Status, resp.Header)

	assertStatusCode(t, resp, http.StatusOK)
	assertContentType(t, resp, jsonContentType)

	var result bodyResponse
	mustUnmarshal(t, resp.Body, &result)

	if result.Args.Encode() != args.Encode() {
		t.Fatalf("expected args = %#v in response, got %#v", args.Encode(), result.Args.Encode())
	}

	if result.Method != verb {
		t.Fatalf("expected method to be %s, got %s", verb, result.Method)
	}

	if len(result.Form) != len(form) {
		t.Fatalf("expected %d form values, got %d", len(form), len(result.Form))
	}
	for k, expectedValues := range form {
		values, ok := result.Form[k]
		if !ok {
			t.Fatalf("expected form field %#v in response", k)
		}
		if !reflect.DeepEqual(expectedValues, values) {
			t.Fatalf("form value mismatch: %#v != %#v", values, expectedValues)
		}
	}
}

func testRequestWithBodyTransferEncoding(t *testing.T, verb, path string) {
	testCases := []struct {
		given string
		want  string
	}{
		{"", ""},
		{"identity", ""},
		{"chunked", "chunked"},
	}
	for _, tc := range testCases {
		tc := tc
		t.Run("transfer-encoding/"+tc.given, func(t *testing.T) {
			t.Parallel()

			req := newTestRequestWithBody(t, verb, path, bytes.NewReader([]byte("{}")))
			if tc.given != "" {
				req.TransferEncoding = []string{tc.given}
			}

			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)

			var result bodyResponse
			mustUnmarshal(t, resp.Body, &result)

			got := result.Headers.Get("Transfer-Encoding")
			if got != tc.want {
				t.Errorf("expected Transfer-Encoding %#v, got %#v", tc.want, got)
			}
		})
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
	tests := []struct {
		code    int
		headers map[string]string
		body    string
	}{
		{200, nil, ""},
		{300, map[string]string{"Location": "/image/jpeg"}, `<!doctype html>
<head>
<title>Multiple Choices</title>
</head>
<body>
<ul>
<li><a href="/image/jpeg">/image/jpeg</a></li>
<li><a href="/image/png">/image/png</a></li>
<li><a href="/image/svg">/image/svg</a></li>
</body>
</html>`},
		{301, redirectHeaders, ""},
		{302, redirectHeaders, ""},
		{308, map[string]string{"Location": "/image/jpeg"}, `<!doctype html>
<head>
<title>Permanent Redirect</title>
</head>
<body>Permanently redirected to <a href="/image/jpeg">/image/jpeg</a>
</body>
</html>`},
		{401, unauthorizedHeaders, ""},
		{418, nil, "I'm a teapot!"},
	}

	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("ok/status/%d", test.code), func(t *testing.T) {
			t.Parallel()

			req, _ := http.NewRequest("GET", srv.URL+fmt.Sprintf("/status/%d", test.code), nil)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)

			if test.headers != nil {
				for key, val := range test.headers {
					assertHeader(t, resp, key, val)
				}
			}

			if test.body != "" {
				got := mustReadAll(t, resp.Body)
				if got != test.body {
					t.Fatalf("expected body %#v, got %#v", test.body, got)
				}
			}
		})
	}

	errorTests := []struct {
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
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.status)
		})
	}
}

func TestUnstable(t *testing.T) {
	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/unstable")
		resp := mustDoReq(t, req)
		if resp.StatusCode != 200 && resp.StatusCode != 500 {
			t.Fatalf("expected status code 200 or 500, got %d", resp.StatusCode)
		}
	})

	// rand.NewSource(1234567890).Float64() => 0.08
	tests := []struct {
		url    string
		status int
	}{
		{"/unstable?seed=1234567890", 500},
		{"/unstable?seed=1234567890&failure_rate=0.07", 200},
	}
	for _, test := range tests {
		test := test
		t.Run("ok_"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.status)
		})
	}

	edgeCaseTests := []string{
		// strange but valid seed
		"/unstable?seed=-12345",
	}
	for _, test := range edgeCaseTests {
		test := test
		t.Run("bad"+test, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test)
			resp := mustDoReq(t, req)
			if resp.StatusCode != 200 && resp.StatusCode != 500 {
				t.Fatalf("expected status code 200 or 500, got %d", resp.StatusCode)
			}
		})
	}

	badTests := []string{
		// bad failure_rate
		"/unstable?failure_rate=foo",
		"/unstable?failure_rate=-1",
		"/unstable?failure_rate=1.23",
		// bad seed
		"/unstable?seed=3.14",
		"/unstable?seed=foo",
	}
	for _, test := range badTests {
		test := test
		t.Run("bad"+test, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusBadRequest)
		})
	}
}

func TestResponseHeaders(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

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

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/response-headers?%s", srv.URL, params.Encode()), nil)
		resp := mustDoReq(t, req)

		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, jsonContentType)

		for k, expectedValues := range headers {
			values, ok := resp.Header[k]
			if !ok {
				t.Fatalf("expected header %s in response headers", k)
			}
			if !reflect.DeepEqual(values, expectedValues) {
				t.Fatalf("expected key values %#v for header %s, got %#v", expectedValues, k, values)
			}
		}

		var gotHeaders http.Header
		if err := json.NewDecoder(resp.Body).Decode(&gotHeaders); err != nil {
			t.Fatalf("failed to unmarshal app response from JSON: %s", err)
		}

		for k, expectedValues := range headers {
			values, ok := gotHeaders[k]
			if !ok {
				t.Fatalf("expected header %s in response body", k)
			}
			if !reflect.DeepEqual(values, expectedValues) {
				t.Fatalf("expected key values %#v for header %s, got %#v", expectedValues, k, values)
			}
		}
	})

	t.Run("override content-type", func(t *testing.T) {
		t.Parallel()

		contentType := "text/test"

		params := url.Values{}
		params.Set("Content-Type", contentType)

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/response-headers?%s", srv.URL, params.Encode()), nil)
		resp := mustDoReq(t, req)

		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, contentType)
	})
}

func TestRedirects(t *testing.T) {
	tests := []struct {
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
		test := test
		t.Run("ok"+test.requestURL, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.requestURL)
			req.Host = "host"
			resp := mustDoReq(t, req)

			assertStatusCode(t, resp, http.StatusFound)
			assertHeader(t, resp, "Location", test.expectedLocation)
		})
	}

	errorTests := []struct {
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
		test := test
		t.Run("error"+test.requestURL, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.requestURL)
			resp := mustDoReq(t, req)

			assertStatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestRedirectTo(t *testing.T) {
	okTests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
			assertHeader(t, resp, "Location", test.expectedLocation)
		})
	}

	badTests := []struct {
		url            string
		expectedStatus int
	}{
		{"/redirect-to", http.StatusBadRequest},                                               // missing url
		{"/redirect-to?status_code=302", http.StatusBadRequest},                               // missing url
		{"/redirect-to?url=foo&status_code=201", http.StatusBadRequest},                       // invalid status code
		{"/redirect-to?url=foo&status_code=418", http.StatusBadRequest},                       // invalid status code
		{"/redirect-to?url=http%3A%2F%2Ffoo%25%25bar&status_code=418", http.StatusBadRequest}, // invalid URL
	}
	for _, test := range badTests {
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}

	// error message matches redirect configuration in global shared test app
	allowedDomainsError := `Forbidden redirect URL. Please be careful with this link.

Allowed redirect destinations:
- example.org
- httpbingo.org
- www.example.com
`

	allowListTests := []struct {
		url            string
		expectedStatus int
	}{
		{"/redirect-to?url=http://httpbingo.org", http.StatusFound},                // allowlist ok
		{"/redirect-to?url=https://httpbingo.org", http.StatusFound},               // scheme doesn't matter
		{"/redirect-to?url=https://example.org/foo/bar", http.StatusFound},         // paths don't matter
		{"/redirect-to?url=https://foo.example.org/foo/bar", http.StatusForbidden}, // subdomains of allowed domains do not match
		{"/redirect-to?url=https://evil.com", http.StatusForbidden},                // not in allowlist
	}
	for _, test := range allowListTests {
		test := test
		t.Run("allowlist"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
			if test.expectedStatus >= 400 {
				assertBodyEquals(t, resp, allowedDomainsError)
			}
		})
	}
}

func TestCookies(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		testCases := map[string]struct {
			cookies cookiesResponse
		}{
			"ok/no cookies": {
				cookies: cookiesResponse{},
			},
			"ok/one cookie": {
				cookies: cookiesResponse{
					"k1": "v1",
				},
			},
			"ok/many cookies": {
				cookies: cookiesResponse{
					"k1": "v1",
					"k2": "v2",
					"k3": "v3",
				},
			},
		}

		for name, tc := range testCases {
			tc := tc
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				req := newTestRequest(t, "GET", "/cookies")
				for k, v := range tc.cookies {
					req.AddCookie(&http.Cookie{
						Name:  k,
						Value: v,
					})
				}
				resp := mustDoReq(t, req)

				assertStatusCode(t, resp, http.StatusOK)
				assertContentType(t, resp, jsonContentType)

				var result cookiesResponse
				mustUnmarshal(t, resp.Body, &result)
				if !reflect.DeepEqual(tc.cookies, result) {
					t.Fatalf("expected cookies %#v, got %#v", tc.cookies, result)
				}
			})
		}
	})

	t.Run("set", func(t *testing.T) {
		t.Parallel()

		cookies := cookiesResponse{
			"k1": "v1",
			"k2": "v2",
		}
		params := &url.Values{}
		for k, v := range cookies {
			params.Set(k, v)
		}

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/cookies/set?%s", srv.URL, params.Encode()), nil)
		resp := mustDoReq(t, req)

		assertStatusCode(t, resp, http.StatusFound)
		assertHeader(t, resp, "Location", "/cookies")

		for _, c := range resp.Cookies() {
			v, ok := cookies[c.Name]
			if !ok {
				t.Fatalf("got unexpected cookie %s=%s", c.Name, c.Value)
			}
			if v != c.Value {
				t.Fatalf("got cookie %s=%s, expected value in %#v", c.Name, c.Value, v)
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()

		cookies := cookiesResponse{
			"k1": "v1",
			"k2": "v2",
		}

		toDelete := "k2"
		params := &url.Values{}
		params.Set(toDelete, "")

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/cookies/delete?%s", srv.URL, params.Encode()), nil)
		for k, v := range cookies {
			req.AddCookie(&http.Cookie{
				Name:  k,
				Value: v,
			})
		}

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusFound)
		assertHeader(t, resp, "Location", "/cookies")

		for _, c := range resp.Cookies() {
			if c.Name == toDelete {
				if time.Since(c.Expires) < (24*365-1)*time.Hour {
					t.Fatalf("expected cookie %s to be deleted; got %#v", toDelete, c)
				}
			}
		}
	})
}

func TestBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/basic-auth/user/pass")
		req.SetBasicAuth("user", "pass")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, jsonContentType)

		var result authResponse
		mustUnmarshal(t, resp.Body, &result)

		expectedResult := authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(result, expectedResult) {
			t.Fatalf("expected response %#v, got %#v", expectedResult, result)
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/basic-auth/user/pass")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusUnauthorized)
		assertContentType(t, resp, jsonContentType)
		assertHeader(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

		var result authResponse
		mustUnmarshal(t, resp.Body, &result)

		expectedResult := authResponse{
			Authorized: false,
			User:       "",
		}
		if !reflect.DeepEqual(result, expectedResult) {
			t.Fatalf("expected response %#v, got %#v", expectedResult, result)
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/basic-auth/user/pass")
		req.SetBasicAuth("bad", "auth")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusUnauthorized)
		assertContentType(t, resp, jsonContentType)
		assertHeader(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

		var result authResponse
		mustUnmarshal(t, resp.Body, &result)

		expectedResult := authResponse{
			Authorized: false,
			User:       "bad",
		}
		if !reflect.DeepEqual(result, expectedResult) {
			t.Fatalf("expected response %#v, got %#v", expectedResult, result)
		}
	})

	errorTests := []struct {
		url    string
		status int
	}{
		{"/basic-auth", http.StatusNotFound},
		{"/basic-auth/user", http.StatusNotFound},
		{"/basic-auth/user/pass/extra", http.StatusNotFound},
	}
	for _, test := range errorTests {
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			req.SetBasicAuth("foo", "bar")
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.status)
		})
	}
}

func TestHiddenBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		req.SetBasicAuth("user", "pass")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, jsonContentType)

		var result authResponse
		mustUnmarshal(t, resp.Body, &result)

		expectedResult := authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(result, expectedResult) {
			t.Fatalf("expected response %#v, got %#v", expectedResult, result)
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusNotFound)
		if resp.Header.Get("WWW-Authenticate") != "" {
			t.Fatal("did not expect WWW-Authenticate header")
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		req.SetBasicAuth("bad", "auth")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusNotFound)
		if resp.Header.Get("WWW-Authenticate") != "" {
			t.Fatal("did not expect WWW-Authenticate header")
		}
	})

	errorTests := []struct {
		url    string
		status int
	}{
		{"/hidden-basic-auth", http.StatusNotFound},
		{"/hidden-basic-auth/user", http.StatusNotFound},
		{"/hidden-basic-auth/user/pass/extra", http.StatusNotFound},
	}
	for _, test := range errorTests {
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			req.SetBasicAuth("foo", "bar")
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.status)
		})
	}
}

func TestDigestAuth(t *testing.T) {
	tests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.status)
		})
	}

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		// Example captured from a successful login in a browser
		authorization := strings.Join([]string{
			`Digest username="user"`,
			`realm="go-httpbin"`,
			`nonce="6fb213c6593975c877bb1247370527ad"`,
			`uri="/digest-auth/auth/user/pass/MD5"`,
			`algorithm=MD5`,
			`response="9b7a05d78051b4f668356eedf32f55d6"`,
			`opaque="fd1c386a015a2bb7c41585f54329ce91"`,
			`qop=auth`,
			`nc=00000001`,
			`cnonce="aaab705226af5bd4"`,
		}, ", ")

		req := newTestRequest(t, "GET", "/digest-auth/auth/user/pass/MD5")
		req.Header.Set("Authorization", authorization)

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		var result authResponse
		mustUnmarshal(t, resp.Body, &result)

		expectedResult := authResponse{
			Authorized: true,
			User:       "user",
		}
		if !reflect.DeepEqual(result, expectedResult) {
			t.Fatalf("expected response %#v, got %#v", expectedResult, result)
		}
	})
}

func TestGzip(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/gzip")
	req.Header.Set("Accept-Encoding", "none") // disable automagic gzip decompression in default http client

	resp := mustDoReq(t, req)
	assertContentType(t, resp, "application/json; encoding=utf-8")
	assertHeader(t, resp, "Content-Encoding", "gzip")
	assertStatusCode(t, resp, http.StatusOK)

	zippedContentLengthStr := resp.Header.Get("Content-Length")
	if zippedContentLengthStr == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	zippedContentLength, err := strconv.Atoi(zippedContentLengthStr)
	if err != nil {
		t.Fatalf("error converting Content-Lengh %v to integer: %s", zippedContentLengthStr, err)
	}

	gzipReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("error creating gzip reader: %s", err)
	}

	unzippedBody, err := io.ReadAll(gzipReader)
	if err != nil {
		t.Fatalf("error reading gzipped body: %s", err)
	}

	var result noBodyResponse
	mustUnmarshal(t, bytes.NewBuffer(unzippedBody), &result)

	if result.Gzipped != true {
		t.Fatalf("expected resp.Gzipped == true")
	}

	if len(unzippedBody) <= zippedContentLength {
		t.Fatalf("expected compressed body")
	}
}

func TestDeflate(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/deflate")
	resp := mustDoReq(t, req)

	assertContentType(t, resp, "application/json; encoding=utf-8")
	assertHeader(t, resp, "Content-Encoding", "deflate")
	assertStatusCode(t, resp, http.StatusOK)

	contentLengthHeader := resp.Header.Get("Content-Length")
	if contentLengthHeader == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	compressedContentLength, err := strconv.Atoi(contentLengthHeader)
	if err != nil {
		t.Fatal(err)
	}

	reader, err := zlib.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}

	var result noBodyResponse
	mustUnmarshal(t, bytes.NewBuffer(body), &result)

	if result.Deflated != true {
		t.Fatalf("expected resp.Deflated == true")
	}

	if len(body) <= compressedContentLength {
		t.Fatalf("expected compressed body")
	}
}

func TestStream(t *testing.T) {
	t.Parallel()

	okTests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			defer resp.Body.Close()

			// Expect empty content-length due to streaming response
			assertHeader(t, resp, "Content-Length", "")

			if len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
				t.Fatalf("expected Transfer-Encoding: chunked, got %#v", resp.TransferEncoding)
			}

			var sr *streamResponse

			i := 0
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				if err := json.Unmarshal(scanner.Bytes(), &sr); err != nil {
					t.Fatalf("error unmarshalling response: %s", err)
				}
				if sr.ID != i {
					t.Fatalf("bad id: %v != %v", sr.ID, i)
				}
				i++
			}
			if err := scanner.Err(); err != nil {
				t.Fatalf("error scanning streaming response: %s", err)
			}
		})
	}

	badTests := []struct {
		url  string
		code int
	}{
		{"/stream", http.StatusNotFound},
		{"/stream/foo", http.StatusBadRequest},
		{"/stream/3.1415", http.StatusBadRequest},
		{"/stream/10/foo", http.StatusNotFound},
	}

	for _, test := range badTests {
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)
		})
	}
}

func TestDelay(t *testing.T) {
	t.Parallel()

	okTests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			elapsed := time.Since(start)

			assertStatusCode(t, resp, http.StatusOK)
			assertHeader(t, resp, "Content-Type", jsonContentType)

			var result bodyResponse
			mustUnmarshal(t, resp.Body, &result)

			if elapsed < test.expectedDelay {
				t.Fatalf("expected delay of %s, got %s", test.expectedDelay, elapsed)
			}
		})
	}

	t.Run("handle cancelation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/delay/1").WithContext(ctx)
		_, err := client.Do(req)
		if !os.IsTimeout(err) {
			t.Errorf("expected timeout error, got %v", err)
		}
	})

	t.Run("cancelation causes 499", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		// use httptest.NewRecorder rather than a live httptest.NewServer
		// because only the former will let us inspect the status code.
		w := httptest.NewRecorder()
		req, _ := http.NewRequestWithContext(ctx, "GET", "/delay/1s", nil)
		app.ServeHTTP(w, req)
		if w.Code != 499 {
			t.Errorf("expected 499, got %d", w.Code)
		}
	})

	badTests := []struct {
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
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)
		})
	}
}

func TestDrip(t *testing.T) {
	t.Parallel()

	okTests := []struct {
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

		{&url.Values{"code": {"404"}}, 0, 10, http.StatusNotFound},
		{&url.Values{"code": {"599"}}, 0, 10, 599},
		{&url.Values{"code": {"567"}}, 0, 10, 567},

		{&url.Values{"duration": {"250ms"}, "delay": {"250ms"}}, 500 * time.Millisecond, 10, http.StatusOK},
		{&url.Values{"duration": {"250ms"}, "delay": {"0.25s"}}, 500 * time.Millisecond, 10, http.StatusOK},
	}
	for _, test := range okTests {
		test := test
		t.Run(fmt.Sprintf("ok/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()

			url := "/drip?" + test.params.Encode()

			start := time.Now()
			req := newTestRequest(t, "GET", url)
			resp := mustDoReq(t, req)
			body := mustReadAll(t, resp.Body) // must read body before measuring elapsed time
			elapsed := time.Since(start)

			assertStatusCode(t, resp, test.code)
			assertHeader(t, resp, "Content-Type", "application/octet-stream")
			assertHeader(t, resp, "Content-Length", strconv.Itoa(test.numbytes))

			if len(body) != test.numbytes {
				t.Fatalf("expected %d bytes, got %d", test.numbytes, len(body))
			}

			if elapsed < test.duration {
				t.Fatalf("expected minimum duration of %s, request took %s", test.duration, elapsed)
			}
		})
	}

	t.Run("HTTP 100 Continue status code supported", func(t *testing.T) {
		// The stdlib http client automagically handles 100 Continue responses
		// by continuing the request until a "final" 200 OK response is
		// received, which prevents us from confirming that a 100 Continue
		// response is sent when using the http client directly.
		//
		// So, here we instead manally write the request to the wire and read
		// the initial response, which will give us access to the 100 Continue
		// indication we need.
		t.Parallel()

		req := newTestRequest(t, "GET", "/drip?code=100")
		reqBytes, err := httputil.DumpRequestOut(req, false)
		assertNilError(t, err)

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assertNilError(t, err)
		defer conn.Close()

		n, err := conn.Write(append(reqBytes, []byte("\r\n\r\n")...))
		assertNilError(t, err)
		assertIntEqual(t, len(reqBytes)+4, n)

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		assertNilError(t, err)
		assertStatusCode(t, resp, 100)
	})

	t.Run("writes are actually incremmental", func(t *testing.T) {
		t.Parallel()

		var (
			duration  = 100 * time.Millisecond
			numBytes  = 3
			wantDelay = duration / time.Duration(numBytes)
			endpoint  = fmt.Sprintf("/drip?duration=%s&delay=%s&numbytes=%d", duration, wantDelay, numBytes)
		)
		req := newTestRequest(t, "GET", endpoint)
		resp := mustDoReq(t, req)

		// Here we read from the response one byte at a time, and ensure that
		// at least the expected delay occurs for each read.
		//
		// The request above includes an initial delay equal to the expected
		// wait between writes so that even the first iteration of this loop
		// expects to wait the same amount of time for a read.
		buf := make([]byte, 1024)
		for {
			start := time.Now()
			n, err := resp.Body.Read(buf)
			gotDelay := time.Since(start)

			if err == io.EOF {
				break
			}

			assertNilError(t, err)
			assertIntEqual(t, n, 1)
			if !reflect.DeepEqual(buf[:n], []byte{'*'}) {
				t.Fatalf("unexpected bytes read: got %v, want %v", buf, []byte{'*'})
			}

			if gotDelay < wantDelay {
				t.Fatalf("to wait at least %s between reads, waited %s", wantDelay, gotDelay)
			}
		}
	})

	t.Run("handle cancelation during initial delay", func(t *testing.T) {
		t.Parallel()

		// For this test, we expect the client to time out and cancel the
		// request after 10ms.  The handler should immediately write a 200 OK
		// status before the client timeout, preventing a client error, but it
		// will wait 500ms to write anything to the response body.
		//
		// So, we're testing that a) the client got an immediate 200 OK but
		// that b) the response body was empty.
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/drip?duration=500ms&delay=500ms")
		req = req.WithContext(ctx)

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		if !os.IsTimeout(err) {
			t.Fatalf("expected client timeout while reading body, bot %s", err)
		}
		if len(body) > 0 {
			t.Fatalf("expected client timeout before body was written, got body %q", string(body))
		}
	})

	t.Run("handle cancelation during drip", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/drip?duration=900ms&delay=100ms")
		req = req.WithContext(ctx)

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		// in this case, the timeout happens while trying to read the body
		body, err := io.ReadAll(resp.Body)
		if !os.IsTimeout(err) {
			t.Fatalf("expected timeout reading body, got %s", err)
		}

		// but we should have received a partial response
		assertBytesEqual(t, body, []byte("**"))
	})

	badTests := []struct {
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
		test := test
		t.Run(fmt.Sprintf("bad/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()

			url := "/drip?" + test.params.Encode()
			req := newTestRequest(t, "GET", url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)
		})
	}

	t.Run("ensure HEAD request works with streaming responses", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "HEAD", "/drip?duration=900ms&delay=100ms")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		body := mustReadAll(t, resp.Body)
		if bodySize := len(body); bodySize > 0 {
			t.Fatalf("expected empty body from HEAD request, got: %s", string(body))
		}
	})
}

func TestRange(t *testing.T) {
	t.Run("ok_no_range", func(t *testing.T) {
		t.Parallel()

		wantBytes := maxBodySize - 1
		url := fmt.Sprintf("/range/%d", wantBytes)
		req := newTestRequest(t, "GET", url)

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertHeader(t, resp, "ETag", fmt.Sprintf("range%d", wantBytes))
		assertHeader(t, resp, "Accept-Ranges", "bytes")
		assertHeader(t, resp, "Content-Length", strconv.Itoa(int(wantBytes)))
		assertContentType(t, resp, "text/plain; charset=utf-8")

		body := mustReadAll(t, resp.Body)
		if len(body) != int(wantBytes) {
			t.Errorf("expected content length %d, got %d", wantBytes, len(body))
		}
	})

	t.Run("ok_range", func(t *testing.T) {
		t.Parallel()

		url := "/range/100"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=10-24")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusPartialContent)
		assertHeader(t, resp, "ETag", "range100")
		assertHeader(t, resp, "Accept-Ranges", "bytes")
		assertHeader(t, resp, "Content-Length", "15")
		assertHeader(t, resp, "Content-Range", "bytes 10-24/100")
		assertBodyEquals(t, resp, "klmnopqrstuvwxy")
	})

	t.Run("ok_range_first_16_bytes", func(t *testing.T) {
		t.Parallel()

		url := "/range/1000"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=0-15")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusPartialContent)
		assertHeader(t, resp, "ETag", "range1000")
		assertHeader(t, resp, "Accept-Ranges", "bytes")
		assertHeader(t, resp, "Content-Length", "16")
		assertHeader(t, resp, "Content-Range", "bytes 0-15/1000")
		assertBodyEquals(t, resp, "abcdefghijklmnop")
	})

	t.Run("ok_range_open_ended_last_6_bytes", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=20-")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusPartialContent)
		assertHeader(t, resp, "ETag", "range26")
		assertHeader(t, resp, "Content-Length", "6")
		assertHeader(t, resp, "Content-Range", "bytes 20-25/26")
		assertBodyEquals(t, resp, "uvwxyz")
	})

	t.Run("ok_range_suffix", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=-5")

		resp := mustDoReq(t, req)
		t.Logf("headers = %v", resp.Header)
		assertStatusCode(t, resp, http.StatusPartialContent)
		assertHeader(t, resp, "ETag", "range26")
		assertHeader(t, resp, "Content-Length", "5")
		assertHeader(t, resp, "Content-Range", "bytes 21-25/26")
		assertBodyEquals(t, resp, "vwxyz")
	})

	t.Run("err_range_out_of_bounds", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=-5")

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusPartialContent)
		assertHeader(t, resp, "ETag", "range26")
		assertHeader(t, resp, "Content-Length", "5")
		assertHeader(t, resp, "Content-Range", "bytes 21-25/26")
		assertBodyEquals(t, resp, "vwxyz")
	})

	// Note: httpbin rejects these requests with invalid range headers, but the
	// go stdlib just ignores them.
	badRangeTests := []struct {
		url         string
		rangeHeader string
	}{
		{"/range/26", "bytes=10-5"},
		{"/range/26", "bytes=32-40"},
		{"/range/26", "bytes=0-40"},
	}
	for _, test := range badRangeTests {
		test := test
		t.Run(fmt.Sprintf("ok_bad_range_header/%s", test.rangeHeader), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)
			assertBodyEquals(t, resp, "abcdefghijklmnopqrstuvwxyz")
		})
	}

	badTests := []struct {
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
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)
		})
	}
}

func TestHTML(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/html")
	resp := mustDoReq(t, req)
	assertContentType(t, resp, htmlContentType)
	assertBodyContains(t, resp, `<h1>Herman Melville - Moby-Dick</h1>`)
}

func TestRobots(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/robots.txt")
	resp := mustDoReq(t, req)
	assertContentType(t, resp, "text/plain")
	assertBodyContains(t, resp, `Disallow: /deny`)
}

func TestDeny(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/deny")
	resp := mustDoReq(t, req)
	assertContentType(t, resp, "text/plain")
	assertBodyContains(t, resp, `YOU SHOULDN'T BE HERE`)
}

func TestCache(t *testing.T) {
	t.Run("ok_no_cache", func(t *testing.T) {
		t.Parallel()

		url := "/cache"
		req := newTestRequest(t, "GET", url)
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, jsonContentType)

		lastModified := resp.Header.Get("Last-Modified")
		if lastModified == "" {
			t.Fatalf("did get Last-Modified header")
		}

		assertHeader(t, resp, "ETag", sha1hash(lastModified))

		var result noBodyResponse
		mustUnmarshal(t, resp.Body, &result)
	})

	tests := []struct {
		headerKey string
		headerVal string
	}{
		{"If-None-Match", "my-custom-etag"},
		{"If-Modified-Since", "my-custom-date"},
	}
	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("ok_cache/%s", test.headerKey), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", "/cache")
			req.Header.Add(test.headerKey, test.headerVal)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusNotModified)
		})
	}
}

func TestCacheControl(t *testing.T) {
	t.Run("ok_cache_control", func(t *testing.T) {
		t.Parallel()

		url := "/cache/60"
		req := newTestRequest(t, "GET", url)
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, jsonContentType)
		assertHeader(t, resp, "Cache-Control", "public, max-age=60")
	})

	badTests := []struct {
		url            string
		expectedStatus int
	}{
		{"/cache/60/foo", http.StatusNotFound},
		{"/cache/foo", http.StatusBadRequest},
		{"/cache/3.14", http.StatusBadRequest},
	}
	for _, test := range badTests {
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestETag(t *testing.T) {
	t.Run("ok_no_headers", func(t *testing.T) {
		t.Parallel()

		url := "/etag/abc"
		req := newTestRequest(t, "GET", url)
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertHeader(t, resp, "ETag", `"abc"`)
	})

	tests := []struct {
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
		test := test
		t.Run("ok_"+test.name, func(t *testing.T) {
			t.Parallel()

			url := "/etag/" + test.etag
			req := newTestRequest(t, "GET", url)
			req.Header.Add(test.headerKey, test.headerVal)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}

	badTests := []struct {
		url            string
		expectedStatus int
	}{
		{"/etag/foo/bar", http.StatusNotFound},
	}
	for _, test := range badTests {
		test := test
		t.Run(fmt.Sprintf("bad/%s", test.url), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestBytes(t *testing.T) {
	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/1024"
		req := newTestRequest(t, "GET", url)
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, "application/octet-stream")

		body := mustReadAll(t, resp.Body)
		if len(body) != 1024 {
			t.Errorf("expected content length 1024, got %d", len(body))
		}
	})

	t.Run("ok_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/16?seed=1234567890"
		req := newTestRequest(t, "GET", url)

		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)
		assertContentType(t, resp, "application/octet-stream")

		want := "\xbf\xcd*\xfa\x15\xa2\xb3r\xc7\a\x98Z\"\x02J\x8e"
		assertBodyEquals(t, resp, want)
	})

	edgeCaseTests := []struct {
		url                   string
		expectedContentLength int
	}{
		{"/bytes/0", 0},
		{"/bytes/1", 1},
		{"/bytes/99999999", 100 * 1024},

		// negative seed allowed
		{"/bytes/16?seed=-12345", 16},
	}
	for _, test := range edgeCaseTests {
		test := test
		t.Run("edge"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)
			t.Logf("status:  %q", resp.Status)
			t.Logf("headers: %v", resp.Header)
			assertHeader(t, resp, "Content-Length", strconv.Itoa(test.expectedContentLength))

			bodyLen := len(mustReadAll(t, resp.Body))
			if bodyLen != test.expectedContentLength {
				t.Errorf("expected body of length %d, got %d", test.expectedContentLength, bodyLen)
			}
		})
	}

	badTests := []struct {
		url            string
		expectedStatus int
	}{
		{"/bytes/-1", http.StatusBadRequest},

		{"/bytes", http.StatusNotFound},
		{"/bytes/16/foo", http.StatusNotFound},

		{"/bytes/foo", http.StatusBadRequest},
		{"/bytes/3.14", http.StatusBadRequest},

		{"/bytes/16?seed=12345678901234567890", http.StatusBadRequest}, // seed too big
		{"/bytes/16?seed=foo", http.StatusBadRequest},
		{"/bytes/16?seed=3.14", http.StatusBadRequest},
	}
	for _, test := range badTests {
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestStreamBytes(t *testing.T) {
	okTests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)

			if len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
				t.Fatalf("expected Transfer-Encoding: chunked, got %#v", resp.TransferEncoding)
			}

			// Expect empty content-length due to streaming response
			assertHeader(t, resp, "Content-Length", "")

			bodySize := len(mustReadAll(t, resp.Body))
			if bodySize != test.expectedContentLength {
				t.Fatalf("expected body of length %d, got %d", test.expectedContentLength, bodySize)
			}
		})
	}

	badTests := []struct {
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
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.code)
		})
	}
}

func TestLinks(t *testing.T) {
	redirectTests := []struct {
		url              string
		expectedLocation string
	}{
		{"/links/1", "/links/1/0"},
		{"/links/100", "/links/100/0"},
	}

	for _, test := range redirectTests {
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusFound)
			assertHeader(t, resp, "Location", test.expectedLocation)
		})
	}

	errorTests := []struct {
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
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}

	linksPageTests := []struct {
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
		test := test
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)
			assertContentType(t, resp, htmlContentType)
			assertBodyEquals(t, resp, test.expectedContent)
		})
	}
}

func TestImage(t *testing.T) {
	acceptTests := []struct {
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
		test := test
		t.Run("ok/accept="+test.acceptHeader, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", "/image")
			req.Header.Set("Accept", test.acceptHeader)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
			if test.expectedContentType != "" {
				assertContentType(t, resp, test.expectedContentType)
			}
		})
	}

	imageTests := []struct {
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
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestXML(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/xml")
	resp := mustDoReq(t, req)
	assertContentType(t, resp, "application/xml")
	assertBodyContains(t, resp, `<?xml version='1.0' encoding='us-ascii'?>`)
}

func isValidUUIDv4(uuid string) error {
	if len(uuid) != 36 {
		return fmt.Errorf("uuid length: %d != 36", len(uuid))
	}
	req := regexp.MustCompile("^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[8|9|a|b][a-f0-9]{3}-[a-f0-9]{12}$")
	if !req.MatchString(uuid) {
		return errors.New("Failed to match against uuidv4 regex")
	}
	return nil
}

func TestUUID(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/uuid")
	resp := mustDoReq(t, req)
	assertStatusCode(t, resp, http.StatusOK)

	// Test response unmarshalling
	var result uuidResponse
	mustUnmarshal(t, resp.Body, &result)

	// Test if the value is an actual UUID
	if err := isValidUUIDv4(result.UUID); err != nil {
		t.Fatalf("Invalid uuid %s: %s", result.UUID, err)
	}
}

func TestBase64(t *testing.T) {
	okTests := []struct {
		requestURL string
		want       string
	}{
		{
			"/base64/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"valid_base64_encoded_string",
		},
		{
			"/base64/decode/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"valid_base64_encoded_string",
		},
		{
			"/base64/encode/valid_base64_encoded_string",
			"dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
		},
		{
			// make sure we correctly handle padding
			// https://github.com/mccutchen/go-httpbin/issues/118
			"/base64/dGVzdC1pbWFnZQ==",
			"test-image",
		},
		{
			// URL-safe base64 is used for decoding (note the - instead of + in
			// encoded input string)
			"/base64/decode/YWJjMTIzIT8kKiYoKSctPUB-",
			"abc123!?$*&()'-=@~",
		},
		{
			// URL-safe base64 is used for encoding (note the - instead of + in
			// encoded output string)
			"/base64/encode/abc123%21%3F%24%2A%26%28%29%27-%3D%40~",
			"YWJjMTIzIT8kKiYoKSctPUB-",
		},
	}

	for _, test := range okTests {
		test := test
		t.Run("ok"+test.requestURL, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.requestURL)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusOK)
			assertContentType(t, resp, "text/plain")
			assertBodyEquals(t, resp, test.want)
		})
	}

	errorTests := []struct {
		requestURL           string
		expectedBodyContains string
	}{
		{
			"/base64/invalid_base64_encoded_string",
			"decode failed",
		},
		{
			"/base64/decode/invalid_base64_encoded_string",
			"decode failed",
		},
		{
			"/base64/decode/invalid_base64_encoded_string",
			"decode failed",
		},
		{
			"/base64/decode/" + strings.Repeat("X", Base64MaxLen+1),
			"Cannot handle input",
		},
		{
			"/base64/",
			"no input data",
		},
		{
			"/base64/decode/",
			"no input data",
		},
		{
			"/base64/decode/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n/extra",
			"invalid URL",
		},
		{
			"/base64/unknown/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"invalid operation: unknown",
		},
		{
			// we only support URL-safe base64 encoded strings (note the +
			// instead of - in encoded input string)
			"/base64/decode/YWJjMTIzIT8kKiYoKSctPUB+",
			"illegal base64 data",
		},
	}

	for _, test := range errorTests {
		test := test
		t.Run("error"+test.requestURL, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.requestURL)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusBadRequest)
			assertBodyContains(t, resp, test.expectedBodyContains)
		})
	}
}

func TestDumpRequest(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/dump/request?foo=bar")
	req.Host = "test-host"
	req.Header.Set("x-test-header2", "Test-Value2")
	req.Header.Set("x-test-header1", "Test-Value1")

	resp := mustDoReq(t, req)
	assertContentType(t, resp, "text/plain; charset=utf-8")
	assertBodyEquals(t, resp, "GET /dump/request?foo=bar HTTP/1.1\r\nHost: test-host\r\nAccept-Encoding: gzip\r\nUser-Agent: Go-http-client/1.1\r\nX-Test-Header1: Test-Value1\r\nX-Test-Header2: Test-Value2\r\n\r\n")
}

func TestJSON(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/json")
	resp := mustDoReq(t, req)
	assertContentType(t, resp, jsonContentType)
	assertBodyContains(t, resp, `Wake up to WonderWidgets!`)
}

func TestBearer(t *testing.T) {
	requestURL := "/bearer"

	t.Run("valid_token", func(t *testing.T) {
		t.Parallel()

		token := "valid_token"
		req := newTestRequest(t, "GET", requestURL)
		req.Header.Set("Authorization", "Bearer "+token)
		resp := mustDoReq(t, req)

		assertStatusCode(t, resp, http.StatusOK)

		var result bearerResponse
		mustUnmarshal(t, resp.Body, &result)

		if result.Authenticated != true {
			t.Fatalf("expected response key %s=%#v, got %#v",
				"Authenticated", true, result.Authenticated)
		}
		if result.Token != token {
			t.Fatalf("expected response key %s=%#v, got %#v",
				"token", token, result.Authenticated)
		}
	})

	errorTests := []struct {
		authorizationHeader string
	}{
		{
			"",
		},
		{
			"Bearer",
		},
		{
			"Bearer x y",
		},
		{
			"bearer x",
		},
		{
			"Bearer1 x",
		},
		{
			"xBearer x",
		},
	}
	for _, test := range errorTests {
		test := test
		t.Run("error"+test.authorizationHeader, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", requestURL)
			if test.authorizationHeader != "" {
				req.Header.Set("Authorization", test.authorizationHeader)
			}
			resp := mustDoReq(t, req)
			assertHeader(t, resp, "WWW-Authenticate", "Bearer")
			assertStatusCode(t, resp, http.StatusUnauthorized)
		})
	}
}

func TestNotImplemented(t *testing.T) {
	tests := []struct {
		url string
	}{
		{"/brotli"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := mustDoReq(t, req)
			assertStatusCode(t, resp, http.StatusNotImplemented)
		})
	}
}

func TestHostname(t *testing.T) {
	t.Run("default hostname", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/hostname")
		resp := mustDoReq(t, req)
		assertStatusCode(t, resp, http.StatusOK)

		var result hostnameResponse
		mustUnmarshal(t, resp.Body, &result)
		if result.Hostname != DefaultHostname {
			t.Errorf("expected hostname %q, got %q", DefaultHostname, result.Hostname)
		}
	})

	t.Run("real hostname", func(t *testing.T) {
		t.Parallel()

		realHostname := "real-hostname"
		app := New(WithHostname(realHostname))
		srv, client := newTestServer(app)
		defer srv.Close()

		req, err := http.NewRequest("GET", srv.URL+"/hostname", nil)
		assertNilError(t, err)

		resp, err := client.Do(req)
		assertNilError(t, err)
		assertStatusCode(t, resp, http.StatusOK)

		var result hostnameResponse
		mustUnmarshal(t, resp.Body, &result)
		if result.Hostname != realHostname {
			t.Errorf("expected hostname %q, got %q", realHostname, result.Hostname)
		}
	})
}

func newTestServer(handler http.Handler) (*httptest.Server, *http.Client) {
	srv := httptest.NewServer(handler)
	client := srv.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return srv, client
}

func newTestRequest(t *testing.T, verb, path string) *http.Request {
	t.Helper()
	return newTestRequestWithBody(t, verb, path, nil)
}

func newTestRequestWithBody(t *testing.T, verb, path string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(verb, srv.URL+path, body)
	if err != nil {
		t.Fatalf("failed to create request: %s", err)
	}
	return req
}

func mustDoReq(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("error making HTTP request: %s %s: %s", req.Method, req.URL, err)
	}
	t.Logf("test request: %s %s => %s (%s)", req.Method, req.URL, resp.Status, time.Since(start))
	return resp
}

func mustReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("error reading: %s", err)
	}
	if rc, ok := r.(io.ReadCloser); ok {
		rc.Close()
	}
	return string(body)
}

func mustUnmarshal(t *testing.T, r io.Reader, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func assertStatusCode(t *testing.T, resp *http.Response, code int) {
	t.Helper()
	if resp.StatusCode != code {
		t.Fatalf("expected status code %d, got %d", code, resp.StatusCode)
	}
}

// assertHeader asserts that a header key has a specific value in a
// response.
func assertHeader(t *testing.T, resp *http.Response, key, want string) {
	t.Helper()
	got := resp.Header.Get(key)
	if want != got {
		t.Fatalf("expected header %s=%#v, got %#v", key, want, got)
	}
}

func assertContentType(t *testing.T, resp *http.Response, contentType string) {
	t.Helper()
	assertHeader(t, resp, "Content-Type", contentType)
}

func assertBodyContains(t *testing.T, resp *http.Response, needle string) {
	t.Helper()
	body := mustReadAll(t, resp.Body)
	if !strings.Contains(body, needle) {
		t.Fatalf("expected string %q in body %q", needle, body)
	}
}

func assertBodyEquals(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	got := mustReadAll(t, resp.Body)
	if want != got {
		t.Fatalf("expected body = %q, got %q", want, got)
	}
}
