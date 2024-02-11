package httpbin

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
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

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/must"
)

const (
	maxBodySize int64         = 1024
	maxDuration time.Duration = 1 * time.Second
	testPrefix                = "/a-prefix"
)

type environment struct {
	prefix string
	srv    *httptest.Server
	client *http.Client
}

// "Global" test app, server, & client to be reused across test cases.
// Initialized in TestMain.
var (
	app        *HTTPBin
	srv        *httptest.Server
	client     *http.Client
	envs       []*environment
	defaultEnv *environment
)

func createApp(opts ...OptionFunc) *HTTPBin {
	return New(append(append(make([]OptionFunc, 0, 6+len(opts)),
		WithAllowedRedirectDomains([]string{
			"httpbingo.org",
			"example.org",
			"www.example.com",
		}),
		WithDefaultParams(DefaultParams{
			DripDelay:    0,
			DripDuration: 100 * time.Millisecond,
			DripNumBytes: 10,
			SSECount:     10,
			SSEDelay:     0,
			SSEDuration:  100 * time.Millisecond,
		}),
		WithMaxBodySize(maxBodySize),
		WithMaxDuration(maxDuration),
		WithObserver(StdLogObserver(log.New(io.Discard, "", 0))),
		WithExcludeHeaders("x-ignore-*,x-info-this-key")),
		opts...)...)
}

func TestMain(m *testing.M) {
	// enable additional safety checks
	testMode = true

	var env *environment
	app = createApp()
	env = newTestEnvironment(app)
	defer env.srv.Close()
	srv = env.srv
	client = env.client
	envs = append(envs, env)
	defaultEnv = env

	env = newTestEnvironment(createApp(WithPrefix(testPrefix)))
	defer env.srv.Close()
	envs = append(envs, env)

	os.Exit(m.Run())
}

func TestIndex(t *testing.T) {
	for _, env := range envs {
		env := env
		t.Run("ok"+env.prefix, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", env.prefix+"/", env)
			resp := must.DoReq(t, env.client, req)

			assert.ContentType(t, resp, htmlContentType)
			assert.Header(t, resp, "Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
			body := must.ReadAll(t, resp.Body)
			assert.Contains(t, body, "go-httpbin", "body")
			assert.Contains(t, body, env.prefix+"/get", "body")
		})

		t.Run("not found"+env.prefix, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", env.prefix+"/foo", env)
			resp := must.DoReq(t, env.client, req)
			assert.StatusCode(t, resp, http.StatusNotFound)
			assert.ContentType(t, resp, jsonContentType)
			got := must.Unmarshal[errorRespnose](t, resp.Body)
			want := errorRespnose{
				StatusCode: http.StatusNotFound,
				Error:      "Not Found",
			}
			assert.DeepEqual(t, got, want, "incorrect error response")
		})
	}
}

func TestFormsPost(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/forms/post")
	resp := must.DoReq(t, client, req)

	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `<form method="post" action="/post">`)
}

func TestUTF8(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/encoding/utf8")
	resp := must.DoReq(t, client, req)

	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `Hello world, Καλημέρα κόσμε, コンニチハ`)
}

func TestGet(t *testing.T) {
	doGetRequest := func(t *testing.T, path string, params url.Values, headers http.Header) noBodyResponse {
		t.Helper()

		if params != nil {
			path = fmt.Sprintf("%s?%s", path, params.Encode())
		}
		req := newTestRequest(t, "GET", path)
		req.Header.Set("User-Agent", "test")
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}

		resp := must.DoReq(t, client, req)
		return mustParseResponse[noBodyResponse](t, resp)
	}

	t.Run("basic", func(t *testing.T) {
		t.Parallel()

		result := doGetRequest(t, "/get", nil, nil)
		assert.Equal(t, result.Method, "GET", "method mismatch")
		assert.Equal(t, result.Args.Encode(), "", "expected empty args")
		assert.Equal(t, result.URL, srv.URL+"/get", "url mismatch")

		if !strings.HasPrefix(result.Origin, "127.0.0.1") {
			t.Fatalf("expected 127.0.0.1 origin, got %q", result.Origin)
		}

		wantHeaders := map[string]string{
			"Content-Type": "",
			"User-Agent":   "test",
		}
		for key, val := range wantHeaders {
			assert.Equal(t, result.Headers.Get(key), val, "header mismatch for key %q", key)
		}
	})

	t.Run("with_query_params", func(t *testing.T) {
		t.Parallel()

		params := url.Values{}
		params.Set("foo", "foo")
		params.Add("bar", "bar1")
		params.Add("bar", "bar2")

		result := doGetRequest(t, "/get", params, nil)
		assert.Equal(t, result.Args.Encode(), params.Encode(), "args mismatch")
		assert.Equal(t, result.Method, "GET", "method mismatch")
	})

	t.Run("will ignore specific headers", func(t *testing.T) {
		t.Parallel()

		params := url.Values{}
		params.Set("foo", "foo")
		params.Add("bar", "bar1")
		params.Add("bar", "bar2")

		header := http.Header{}

		header.Set("X-Ignore-Foo", "foo")
		header.Set("X-Info-Foo", "bar")
		header.Set("x-info-this-key", "baz")

		result := doGetRequest(t, "/get", params, header)
		assert.Equal(t, result.Args.Encode(), params.Encode(), "args mismatch")
		assert.Equal(t, result.Method, "GET", "method mismatch")
		assertHeaderEqual(t, &result.Headers, "X-Ignore-Foo", "")
		assertHeaderEqual(t, &result.Headers, "x-info-this-key", "")
		assertHeaderEqual(t, &result.Headers, "X-Info-Foo", "bar")
	})

	t.Run("only_allows_gets", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "POST", "/get")
		resp := must.DoReq(t, client, req)

		assert.StatusCode(t, resp, http.StatusMethodNotAllowed)
		assert.ContentType(t, resp, textContentType)
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
			headers := http.Header{}
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, tc.wantCode)

			// we only do further validation when we get an OK response
			if tc.wantCode != http.StatusOK {
				return
			}

			assert.StatusCode(t, resp, http.StatusOK)
			assert.BodyEquals(t, resp, "")
			assert.Header(t, resp, "Content-Length", "") // content-length should be empty
		})
	}
}

func TestCORS(t *testing.T) {
	t.Run("CORS/no_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/get")
		resp := must.DoReq(t, client, req)
		assert.Header(t, resp, "Access-Control-Allow-Origin", "*")
	})

	t.Run("CORS/with_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/get")
		req.Header.Set("Origin", "origin")
		resp := must.DoReq(t, client, req)
		assert.Header(t, resp, "Access-Control-Allow-Origin", "origin")
	})

	t.Run("CORS/options_request", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "OPTIONS", "/get")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, 200)

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
			assert.Header(t, resp, test.key, test.expected)
		}
	})

	t.Run("CORS/allow_headers", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "OPTIONS", "/get")
		req.Header.Set("Access-Control-Request-Headers", "X-Test-Header")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, 200)

		headerTests := []struct {
			key      string
			expected string
		}{
			{"Access-Control-Allow-Headers", "X-Test-Header"},
		}
		for _, test := range headerTests {
			assert.Header(t, resp, test.key, test.expected)
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

			result := must.Unmarshal[ipResponse](t, w.Body)
			assert.Equal(t, result.Origin, tc.wantOrigin, "incorrect origin")
		})
	}
}

func TestUserAgent(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/user-agent")
	req.Header.Set("User-Agent", "test")

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[userAgentResponse](t, resp)
	assert.Equal(t, "test", result.UserAgent, "incorrect user agent")
}

func TestHeaders(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/headers")
	req.Host = "test-host"
	req.Header.Set("User-Agent", "test")
	req.Header.Set("Foo-Header", "foo")
	req.Header.Add("Bar-Header", "bar1")
	req.Header.Add("Bar-Header", "bar2")

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[headersResponse](t, resp)

	// Host header requires special treatment, because it's a field on the
	// http.Request struct itself, not part of its headers map
	host := result.Headers.Get("Host")
	assert.Equal(t, req.Host, host, "missing or incorrect Host header")

	for k, expectedValues := range req.Header {
		values := result.Headers.Values(k)
		assert.DeepEqual(t, expectedValues, values, "missing or incorrect header for key %q", k)
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
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodyEquals(t, resp, "")
		assert.Header(t, resp, "Content-Length", "") // responses to HEAD requests should not have a Content-Length header
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
		testRequestWithBodyExpect100Continue,
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

			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			result := mustParseResponse[bodyResponse](t, resp)
			assert.Equal(t, result.Method, verb, "method mismatch")
			assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
			assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
			assert.DeepEqual(t, result.Form, nilValues, "expected empty form")
			assert.DeepEqual(t, result.JSON, nil, "expected nil json")

			expected := "data:" + test.contentType + ";base64," + base64.StdEncoding.EncodeToString([]byte(test.requestBody))
			assert.Equal(t, result.Data, expected, "expected binary encoded response data")
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

			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			result := mustParseResponse[bodyResponse](t, resp)
			assert.Equal(t, result.Data, "", "expected empty response data")
			assert.Equal(t, result.Method, verb, "method mismatch")
			assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
			assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
			assert.DeepEqual(t, result.Form, nilValues, "expected empty form")
			assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")
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

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.DeepEqual(t, result.Form, params, "form data mismatch")
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.JSON, nil, "expected nil json")
}

func testRequestWithBodyHTML(t *testing.T, verb, path string) {
	data := "<html><body><h1>hello world</h1></body></html>"

	req := newTestRequestWithBody(t, verb, path, strings.NewReader(data))
	req.Header.Set("Content-Type", htmlContentType)

	resp := must.DoReq(t, client, req)
	assert.StatusCode(t, resp, http.StatusOK)
	assert.ContentType(t, resp, jsonContentType)
	assert.BodyContains(t, resp, data)
}

func testRequestWithBodyExpect100Continue(t *testing.T, verb, path string) {
	// The stdlib http client automagically handles 100 Continue responses
	// by continuing the request until a "final" 200 OK response is
	// received, which prevents us from confirming that a 100 Continue
	// response is sent when using the http client directly.
	//
	// So, here we instead manally write the request to the wire in two
	// steps, confirming that we receive a 100 Continue response before
	// sending the body and getting the normal expected response.

	t.Run("non-zero content-length okay", func(t *testing.T) {
		t.Parallel()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		body := []byte("test body")

		req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body))
		req.Header.Set("Expect", "100-continue")
		req.Header.Set("Content-Type", "text/plain")

		reqBytes, _ := httputil.DumpRequestOut(req, false)
		t.Logf("raw request:\n%q", reqBytes)

		if !strings.Contains(string(reqBytes), "Content-Length: 9") {
			t.Fatalf("expected request to contain Content-Length header")
		}

		// first, we write the request line and headers -- but NOT the body --
		// which should cause the server to respond with a 100 Continue
		// response.
		{
			n, err := conn.Write(reqBytes)
			assert.NilError(t, err)
			assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

			resp, err := http.ReadResponse(bufio.NewReader(conn), req)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusContinue)
		}

		// Once we've gotten the 100 Continue response, we write the body. After
		// that, we should get a normal 200 OK response along with the expected
		// result.
		{
			n, err := conn.Write(body)
			assert.NilError(t, err)
			assert.Equal(t, n, len(body), "incorrect number of bytes written")

			resp, err := http.ReadResponse(bufio.NewReader(conn), req)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusOK)

			got := must.Unmarshal[bodyResponse](t, resp.Body)
			assert.Equal(t, got.Data, string(body), "incorrect body")
		}
	})

	t.Run("transfer-encoding:chunked okay", func(t *testing.T) {
		t.Parallel()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		body := []byte("test body")

		reqParts := []string{
			fmt.Sprintf("%s %s HTTP/1.1", verb, path),
			"Host: test",
			"Content-Type: text/plain",
			"Expect: 100-continue",
			"Transfer-Encoding: chunked",
		}
		reqBytes := []byte(strings.Join(reqParts, "\r\n") + "\r\n\r\n")
		t.Logf("raw request:\n%q", reqBytes)

		// first, we write the request line and headers -- but NOT the body --
		// which should cause the server to respond with a 100 Continue
		// response.
		{
			n, err := conn.Write(reqBytes)
			assert.NilError(t, err)
			assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusContinue)
		}

		// Once we've gotten the 100 Continue response, we write the body. After
		// that, we should get a normal 200 OK response along with the expected
		// result.
		{
			// write chunk size
			_, err := conn.Write([]byte("9\r\n"))
			assert.NilError(t, err)

			// write chunk data
			n, err := conn.Write(append(body, "\r\n"...))
			assert.NilError(t, err)
			assert.Equal(t, n, len(body)+2, "incorrect number of bytes written")

			// write empty terminating chunk
			_, err = conn.Write([]byte("0\r\n\r\n"))
			assert.NilError(t, err)

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusOK)

			got := must.Unmarshal[bodyResponse](t, resp.Body)
			assert.Equal(t, got.Data, string(body), "incorrect body")
		}
	})

	t.Run("zero content-length ignored", func(t *testing.T) {
		// The Go stdlib's Expect:100-continue handling requires either a a)
		// non-zero Content-Length header or b) Transfer-Encoding:chunked
		// header to be present.  Otherwise, the Expect header is ignored and
		// the request is processed normally.
		t.Parallel()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		req := newTestRequest(t, verb, path)
		req.Header.Set("Expect", "100-continue")

		reqBytes, _ := httputil.DumpRequestOut(req, false)
		t.Logf("raw request:\n%q", reqBytes)

		// For GET and DELETE requests, it appears the Go stdlib does not
		// include a Content-Length:0 header, so we ensure that the header is
		// either missing or has a value of 0.
		switch verb {
		case "GET", "DELETE":
			if strings.Contains(string(reqBytes), "Content-Length:") {
				t.Fatalf("expected no Content-Length header for %s request", verb)
			}
		default:
			if !strings.Contains(string(reqBytes), "Content-Length: 0") {
				t.Fatalf("expected Content-Length:0 header for %s request", verb)
			}
		}

		n, err := conn.Write(reqBytes)
		assert.NilError(t, err)
		assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		assert.NilError(t, err)

		// Note: the server should NOT send a 100 Continue response here,
		// because we send a request without a Content-Length header or with a
		// Content-Length: 0 header.
		assert.StatusCode(t, resp, http.StatusOK)

		got := must.Unmarshal[bodyResponse](t, resp.Body)
		assert.Equal(t, got.Data, "", "incorrect body")
	})
}

func testRequestWithBodyFormEncodedBodyNoContentType(t *testing.T, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequestWithBody(t, verb, path, strings.NewReader(params.Encode()))
	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, nilValues, "expected empty form")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")

	// Because we did not set an content type, httpbin will return the base64 encoded data.
	expectedBody := "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString([]byte(params.Encode()))
	assert.Equal(t, result.Data, expectedBody, "response data mismatch")
}

func testRequestWithBodyMultiPartBody(t *testing.T, verb, path string) {
	params := url.Values{
		"foo": {"foo"},
		"bar": {"bar1", "bar2"},
	}

	// Prepare a form that you will submit to that URL.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	for k, vs := range params {
		for _, v := range vs {
			fw, err := mw.CreateFormField(k)
			assert.NilError(t, err)
			_, err = fw.Write([]byte(v))
			assert.NilError(t, err)
		}
	}
	mw.Close()

	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, params, "form values mismatch")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")
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

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Form, nilValues, "expected empty form")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")

	// verify that the file we added is present in the `files` attribute of the
	// response, with the field as key and content as value
	wantFiles := url.Values{
		"fieldname": {"hello world"},
	}
	assert.DeepEqual(t, result.Files, wantFiles, "files mismatch")
}

func testRequestWithBodyInvalidFormEncodedBody(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := must.DoReq(t, client, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyInvalidMultiPartBody(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "multipart/form-data; etc")
	resp := must.DoReq(t, client, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
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

	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.Equal(t, result.Data, string(inputBody), "response data mismatch")
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, nilValues, "form values mismatch")

	// Need to re-marshall just the JSON field from the response in order to
	// re-unmarshall it into our expected type
	roundTrippedInputBytes, err := json.Marshal(result.JSON)
	assert.NilError(t, err)

	roundTrippedInput := must.Unmarshal[testInput](t, bytes.NewReader(roundTrippedInputBytes))
	assert.DeepEqual(t, roundTrippedInput, input, "round-tripped JSON mismatch")
}

func testRequestWithBodyInvalidJSON(t *testing.T, verb, path string) {
	req := newTestRequestWithBody(t, verb, path, strings.NewReader("foo"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp := must.DoReq(t, client, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyBodyTooBig(t *testing.T, verb, path string) {
	body := make([]byte, maxBodySize+1)
	req := newTestRequestWithBody(t, verb, path, bytes.NewReader(body))
	resp := must.DoReq(t, client, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyQueryParams(t *testing.T, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequest(t, verb, fmt.Sprintf("%s?%s", path, params.Encode()))
	resp := must.DoReq(t, client, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.DeepEqual(t, result.Args, params, "args mismatch")

	// extra validation
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, nilValues, "form values mismatch")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")
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
	resp := must.DoReq(t, client, req)

	result := mustParseResponse[bodyResponse](t, resp)
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.Equal(t, result.Args.Encode(), args.Encode(), "args mismatch")
	assert.Equal(t, result.Form.Encode(), form.Encode(), "form mismatch")
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

			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			result := mustParseResponse[bodyResponse](t, resp)
			got := result.Headers.Get("Transfer-Encoding")
			assert.Equal(t, got, tc.want, "Transfer-Encoding header mismatch")
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
		// 100 is tested as a special case below
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
		{500, nil, ""}, // maximum allowed status code
		{599, nil, ""}, // maximum allowed status code
	}

	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("ok/status/%d", test.code), func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest("GET", srv.URL+fmt.Sprintf("/status/%d", test.code), nil)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
			assert.BodyEquals(t, resp, test.body)
			for key, val := range test.headers {
				assert.Header(t, resp, key, val)
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
		{"/status/600", http.StatusBadRequest},
		{"/status/1024", http.StatusBadRequest},
	}

	for _, test := range errorTests {
		test := test
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.status)
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

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		req := newTestRequest(t, "GET", "/status/100")
		reqBytes, err := httputil.DumpRequestOut(req, false)
		assert.NilError(t, err)

		n, err := conn.Write(reqBytes)
		assert.NilError(t, err)
		assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		assert.NilError(t, err)
		assert.StatusCode(t, resp, http.StatusContinue)
	})

	t.Run("multiple choice", func(t *testing.T) {
		t.Parallel()

		t.Run("ok", func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest("GET", srv.URL+"/status/200:0.7,429:0.2,503:0.1", nil)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			if resp.StatusCode != 200 && resp.StatusCode != 429 && resp.StatusCode != 503 {
				t.Fatalf("expected status code 200, 429, or 503, got %d", resp.StatusCode)
			}
		})

		t.Run("bad weight", func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest("GET", srv.URL+"/status/200:foo,500:1", nil)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})

		t.Run("bad choice", func(t *testing.T) {
			t.Parallel()
			req, _ := http.NewRequest("GET", srv.URL+"/status/200:1,foo:1", nil)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})
	})
}

func TestUnstable(t *testing.T) {
	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/unstable")
		resp := must.DoReq(t, client, req)
		if resp.StatusCode != 200 && resp.StatusCode != 500 {
			t.Fatalf("expected status code 200 or 500, got %d", resp.StatusCode)
		}
	})

	tests := []struct {
		url    string
		status int
	}{
		// rand.NewSource(1234567890).Float64() => 0.08
		{"/unstable?seed=1234567890", 500},
		{"/unstable?seed=1234567890&failure_rate=0.07", 200},
	}
	for _, test := range tests {
		test := test
		t.Run("ok_"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.status)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})
	}
}

func TestResponseHeaders(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		wantHeaders := url.Values{
			"Foo": {"foo"},
			"Bar": {"bar1", "bar2"},
		}

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/response-headers?%s", srv.URL, wantHeaders.Encode()), nil)
		resp := must.DoReq(t, client, req)
		result := mustParseResponse[http.Header](t, resp)

		for k, expectedValues := range wantHeaders {
			// expected headers should be present in the HTTP response itself
			respValues := resp.Header[k]
			assert.DeepEqual(t, respValues, expectedValues, "HTTP response headers mismatch")

			// they should also be reflected in the decoded JSON resposne
			resultValues := result[k]
			assert.DeepEqual(t, resultValues, expectedValues, "JSON response headers mismatch")
		}
	})

	t.Run("override content-type", func(t *testing.T) {
		t.Parallel()

		contentType := "text/test"

		params := url.Values{}
		params.Set("Content-Type", contentType)

		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/response-headers?%s", srv.URL, params.Encode()), nil)
		resp := must.DoReq(t, client, req)

		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, contentType)
	})
}

func TestRedirects(t *testing.T) {
	tests := []struct {
		requestURL       string
		expectedLocation string
	}{
		// append %.s to expected string if result does not contain prefix:
		// https://stackoverflow.com/a/41209086
		{"%s/redirect/1", "%s/get"},
		{"%s/redirect/2", "%s/relative-redirect/1"},
		{"%s/redirect/100", "%s/relative-redirect/99"},

		{"%s/redirect/1?absolute=true", "http://host/get%.s"},
		{"%s/redirect/2?absolute=TRUE", "http://host/absolute-redirect/1%.s"},
		{"%s/redirect/100?absolute=True", "http://host/absolute-redirect/99%.s"},

		{"%s/redirect/100?absolute=t", "%s/relative-redirect/99"},
		{"%s/redirect/100?absolute=1", "%s/relative-redirect/99"},
		{"%s/redirect/100?absolute=yes", "%s/relative-redirect/99"},

		{"%s/relative-redirect/1", "%s/get"},
		{"%s/relative-redirect/2", "%s/relative-redirect/1"},
		{"%s/relative-redirect/100", "%s/relative-redirect/99"},

		{"%s/absolute-redirect/1", "http://host/get%.s"},
		{"%s/absolute-redirect/2", "http://host/absolute-redirect/1%.s"},
		{"%s/absolute-redirect/100", "http://host/absolute-redirect/99%.s"},
	}

	for _, env := range envs {
		for _, test := range tests {
			env := env
			test := test
			requestURL := fmt.Sprintf(test.requestURL, env.prefix)
			t.Run("ok"+requestURL, func(t *testing.T) {
				t.Parallel()

				req := newTestRequest(t, "GET", requestURL, env)
				req.Host = "host"
				resp := must.DoReq(t, env.client, req)
				defer consumeAndCloseBody(resp)

				assert.StatusCode(t, resp, http.StatusFound)
				assert.Header(t, resp, "Location", fmt.Sprintf(test.expectedLocation, env.prefix))
			})
		}
	}

	errorTests := []struct {
		requestURL     string
		expectedStatus int
	}{
		{"%s/redirect", http.StatusNotFound},
		{"%s/redirect/", http.StatusBadRequest},
		{"%s/redirect/-1", http.StatusBadRequest},
		{"%s/redirect/3.14", http.StatusBadRequest},
		{"%s/redirect/foo", http.StatusBadRequest},
		{"%s/redirect/10/foo", http.StatusNotFound},

		{"%s/relative-redirect", http.StatusNotFound},
		{"%s/relative-redirect/", http.StatusBadRequest},
		{"%s/relative-redirect/-1", http.StatusBadRequest},
		{"%s/relative-redirect/3.14", http.StatusBadRequest},
		{"%s/relative-redirect/foo", http.StatusBadRequest},
		{"%s/relative-redirect/10/foo", http.StatusNotFound},

		{"%s/absolute-redirect", http.StatusNotFound},
		{"%s/absolute-redirect/", http.StatusBadRequest},
		{"%s/absolute-redirect/-1", http.StatusBadRequest},
		{"%s/absolute-redirect/3.14", http.StatusBadRequest},
		{"%s/absolute-redirect/foo", http.StatusBadRequest},
		{"%s/absolute-redirect/10/foo", http.StatusNotFound},
	}

	for _, env := range envs {
		for _, test := range errorTests {
			env := env
			test := test
			requestURL := fmt.Sprintf(test.requestURL, env.prefix)
			t.Run("error"+requestURL, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", requestURL, env)
				resp := must.DoReq(t, env.client, req)
				defer consumeAndCloseBody(resp)
				assert.StatusCode(t, resp, test.expectedStatus)
			})
		}
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
			assert.Header(t, resp, "Location", test.expectedLocation)
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
		{"/redirect-to?url=foo&status_code=foo", http.StatusBadRequest},                       // invalid status code
		{"/redirect-to?url=http%3A%2F%2Ffoo%25%25bar&status_code=418", http.StatusBadRequest}, // invalid URL
	}
	for _, test := range badTests {
		test := test
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.url)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}

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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
			if test.expectedStatus >= 400 {
				assert.BodyEquals(t, resp, app.forbiddenRedirectError)
			}
		})
	}
}

func TestCookies(t *testing.T) {
	for _, env := range envs {
		env := env
		t.Run("get"+env.prefix, func(t *testing.T) {
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

					req := newTestRequest(t, "GET", env.prefix+"/cookies", env)
					for k, v := range tc.cookies {
						req.AddCookie(&http.Cookie{
							Name:  k,
							Value: v,
						})
					}

					resp := must.DoReq(t, env.client, req)
					defer consumeAndCloseBody(resp)

					result := mustParseResponse[cookiesResponse](t, resp)
					assert.DeepEqual(t, result, tc.cookies, "cookies mismatch")
				})
			}
		})

		t.Run("set"+env.prefix, func(t *testing.T) {
			t.Parallel()

			cookies := cookiesResponse{
				"k1": "v1",
				"k2": "v2",
			}
			params := &url.Values{}
			for k, v := range cookies {
				params.Set(k, v)
			}

			req := newTestRequest(t, "GET", env.prefix+"/cookies/set?"+params.Encode(), env)
			resp := must.DoReq(t, client, req)

			assert.StatusCode(t, resp, http.StatusFound)
			assert.Header(t, resp, "Location", env.prefix+"/cookies")

			for _, c := range resp.Cookies() {
				v, ok := cookies[c.Name]
				if !ok {
					t.Fatalf("got unexpected cookie %s=%s", c.Name, c.Value)
				}
				assert.Equal(t, v, c.Value, "value mismatch for cookie %q", c.Name)
			}
		})

		t.Run("delete"+env.prefix, func(t *testing.T) {
			t.Parallel()

			cookies := cookiesResponse{
				"k1": "v1",
				"k2": "v2",
			}

			toDelete := "k2"
			params := &url.Values{}
			params.Set(toDelete, "")

			req := newTestRequest(t, "GET", env.prefix+"/cookies/delete?"+params.Encode(), env)
			for k, v := range cookies {
				req.AddCookie(&http.Cookie{
					Name:  k,
					Value: v,
				})
			}

			resp := must.DoReq(t, env.client, req)
			assert.StatusCode(t, resp, http.StatusFound)
			assert.Header(t, resp, "Location", env.prefix+"/cookies")

			for _, c := range resp.Cookies() {
				if c.Name == toDelete {
					if time.Since(c.Expires) < (24*365-1)*time.Hour {
						t.Fatalf("expected cookie %s to be deleted; got %#v", toDelete, c)
					}
				}
			}
		})
	}
}

func TestBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			method := method
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, "/basic-auth/user/pass")
				req.SetBasicAuth("user", "pass")

				resp := must.DoReq(t, client, req)
				result := mustParseResponse[authResponse](t, resp)
				expectedResult := authResponse{
					Authorized: true,
					User:       "user",
				}
				assert.DeepEqual(t, result, expectedResult, "expected authorized user")
			})
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			method := method
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, "/basic-auth/user/pass")
				resp := must.DoReq(t, client, req)
				assert.StatusCode(t, resp, http.StatusUnauthorized)
				assert.ContentType(t, resp, jsonContentType)
				assert.Header(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

				result := must.Unmarshal[authResponse](t, resp.Body)
				expectedResult := authResponse{
					Authorized: false,
					User:       "",
				}
				assert.DeepEqual(t, result, expectedResult, "expected unauthorized user")
			})
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			method := method
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, "/basic-auth/user/pass")
				req.SetBasicAuth("bad", "auth")

				resp := must.DoReq(t, client, req)
				assert.StatusCode(t, resp, http.StatusUnauthorized)
				assert.ContentType(t, resp, jsonContentType)
				assert.Header(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

				result := must.Unmarshal[authResponse](t, resp.Body)
				expectedResult := authResponse{
					Authorized: false,
					User:       "bad",
				}
				assert.DeepEqual(t, result, expectedResult, "expected unauthorized user")
			})
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.status)
		})
	}
}

func TestHiddenBasicAuth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		req.SetBasicAuth("user", "pass")

		resp := must.DoReq(t, client, req)
		result := mustParseResponse[authResponse](t, resp)
		expectedResult := authResponse{
			Authorized: true,
			User:       "user",
		}
		assert.DeepEqual(t, result, expectedResult, "expected authorized user")
	})

	t.Run("error/no auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusNotFound)
		assert.Header(t, resp, "WWW-Authenticate", "")
	})

	t.Run("error/bad auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/hidden-basic-auth/user/pass")
		req.SetBasicAuth("bad", "auth")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusNotFound)
		assert.Header(t, resp, "WWW-Authenticate", "")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.status)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.status)
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

		resp := must.DoReq(t, client, req)
		result := mustParseResponse[authResponse](t, resp)
		expectedResult := authResponse{
			Authorized: true,
			User:       "user",
		}
		assert.DeepEqual(t, result, expectedResult, "expected authorized user")
	})
}

func TestGzip(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/gzip")
	req.Header.Set("Accept-Encoding", "none") // disable automagic gzip decompression in default http client

	resp := must.DoReq(t, client, req)
	assert.Header(t, resp, "Content-Encoding", "gzip")
	assert.ContentType(t, resp, jsonContentType)
	assert.StatusCode(t, resp, http.StatusOK)

	zippedContentLengthStr := resp.Header.Get("Content-Length")
	if zippedContentLengthStr == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	zippedContentLength, err := strconv.Atoi(zippedContentLengthStr)
	assert.NilError(t, err)

	gzipReader, err := gzip.NewReader(resp.Body)
	assert.NilError(t, err)

	unzippedBody, err := io.ReadAll(gzipReader)
	assert.NilError(t, err)

	result := must.Unmarshal[noBodyResponse](t, bytes.NewBuffer(unzippedBody))
	assert.Equal(t, result.Gzipped, true, "expected resp.Gzipped == true")

	if len(unzippedBody) <= zippedContentLength {
		t.Fatalf("expected compressed body")
	}
}

func TestDeflate(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/deflate")
	resp := must.DoReq(t, client, req)

	assert.ContentType(t, resp, jsonContentType)
	assert.Header(t, resp, "Content-Encoding", "deflate")
	assert.StatusCode(t, resp, http.StatusOK)

	contentLengthHeader := resp.Header.Get("Content-Length")
	if contentLengthHeader == "" {
		t.Fatalf("missing Content-Length header in response")
	}

	compressedContentLength, err := strconv.Atoi(contentLengthHeader)
	assert.NilError(t, err)

	reader, err := zlib.NewReader(resp.Body)
	assert.NilError(t, err)

	body, err := io.ReadAll(reader)
	assert.NilError(t, err)

	result := must.Unmarshal[noBodyResponse](t, bytes.NewBuffer(body))
	assert.Equal(t, result.Deflated, true, "expected result.Deflated == true")

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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			// Expect empty content-length due to streaming response
			assert.Header(t, resp, "Content-Length", "")
			assert.DeepEqual(t, resp.TransferEncoding, []string{"chunked"}, "expected Transfer-Encoding: chunked")

			i := 0
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				sr := must.Unmarshal[streamResponse](t, bytes.NewReader(scanner.Bytes()))
				assert.Equal(t, sr.ID, i, "bad id")
				i++
			}
			assert.NilError(t, scanner.Err())
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
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
			resp := must.DoReq(t, client, req)
			elapsed := time.Since(start)

			defer consumeAndCloseBody(resp)
			_ = mustParseResponse[bodyResponse](t, resp)

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
		assert.Equal(t, w.Code, 499, "incorrect status code")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.BodySize(t, resp, test.numbytes) // must read body before measuring elapsed time
			elapsed := time.Since(start)

			assert.StatusCode(t, resp, test.code)
			assert.ContentType(t, resp, binaryContentType)
			assert.Header(t, resp, "Content-Length", strconv.Itoa(test.numbytes))
			if elapsed < test.duration {
				t.Fatalf("expected minimum duration of %s, request took %s", test.duration, elapsed)
			}

			// Note: while the /drip endpoint seems like an ideal use case for
			// using chunked transfer encoding to stream data to the client, it
			// is actually intended to simulate a slow connection between
			// server and client, so it is important to ensure that it writes a
			// "regular," un-chunked response.
			assert.DeepEqual(t, resp.TransferEncoding, nil, "unexpected Transfer-Encoding header")
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
		assert.NilError(t, err)

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		n, err := conn.Write(reqBytes)
		assert.NilError(t, err)
		assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		assert.NilError(t, err)
		assert.StatusCode(t, resp, 100)
	})

	t.Run("writes are actually incremmental", func(t *testing.T) {
		t.Parallel()

		var (
			duration = 100 * time.Millisecond
			numBytes = 3
			endpoint = fmt.Sprintf("/drip?duration=%s&numbytes=%d", duration, numBytes)

			// Match server logic for calculating the delay between writes
			wantPauseBetweenWrites = duration / time.Duration(numBytes-1)
		)
		req := newTestRequest(t, "GET", endpoint)
		resp := must.DoReq(t, client, req)
		defer consumeAndCloseBody(resp)

		// Here we read from the response one byte at a time, and ensure that
		// at least the expected delay occurs for each read.
		//
		// The request above includes an initial delay equal to the expected
		// wait between writes so that even the first iteration of this loop
		// expects to wait the same amount of time for a read.
		buf := make([]byte, 1024)
		gotBody := make([]byte, 0, numBytes)
		for i := 0; ; i++ {
			start := time.Now()
			n, err := resp.Body.Read(buf)
			gotPause := time.Since(start)

			// We expect to read exactly one byte on each iteration. On the
			// last iteration, we expct to hit EOF after reading the final
			// byte, because the server does not pause after the last write.
			assert.Equal(t, n, 1, "incorrect number of bytes read")
			assert.DeepEqual(t, buf[:n], []byte{'*'}, "unexpected bytes read")
			gotBody = append(gotBody, buf[:n]...)

			if err == io.EOF {
				break
			}

			assert.NilError(t, err)

			// only ensure that we pause for the expected time between writes
			// (allowing for minor mismatch in local timers and server timers)
			// after the first byte.
			if i > 0 {
				assert.RoughDuration(t, gotPause, wantPauseBetweenWrites, 3*time.Millisecond)
			}
		}

		wantBody := bytes.Repeat([]byte{'*'}, numBytes)
		assert.DeepEqual(t, gotBody, wantBody, "incorrect body")
	})

	t.Run("handle cancelation during initial delay", func(t *testing.T) {
		t.Parallel()

		// For this test, we expect the client to time out and cancel the
		// request after 10ms.  The handler should still be in its intitial
		// delay period, so this will result in a request error since no status
		// code will be written before the cancelation.
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/drip?duration=500ms&delay=500ms").WithContext(ctx)
		if _, err := client.Do(req); !os.IsTimeout(err) {
			t.Fatalf("expected timeout error, got %s", err)
		}
	})

	t.Run("handle cancelation during drip", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/drip?duration=900ms&delay=100ms").WithContext(ctx)
		resp := must.DoReq(t, client, req)
		defer consumeAndCloseBody(resp)

		// In this test, the server should have started an OK response before
		// our client timeout cancels the request, so we should get an OK here.
		assert.StatusCode(t, resp, http.StatusOK)

		// But, we should time out while trying to read the whole response
		// body.
		body, err := io.ReadAll(resp.Body)
		if !os.IsTimeout(err) {
			t.Fatalf("expected timeout reading body, got %s", err)
		}

		// And even though the request timed out, we should get a partial
		// response.
		assert.DeepEqual(t, body, []byte("**"), "incorrect partial body")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
		})
	}

	t.Run("ensure HEAD request works with streaming responses", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "HEAD", "/drip?duration=900ms&delay=100ms")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodySize(t, resp, 0)
	})
}

func TestRange(t *testing.T) {
	t.Run("ok_no_range", func(t *testing.T) {
		t.Parallel()

		wantBytes := maxBodySize - 1
		url := fmt.Sprintf("/range/%d", wantBytes)
		req := newTestRequest(t, "GET", url)

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.Header(t, resp, "ETag", fmt.Sprintf("range%d", wantBytes))
		assert.Header(t, resp, "Accept-Ranges", "bytes")
		assert.Header(t, resp, "Content-Length", strconv.Itoa(int(wantBytes)))
		assert.ContentType(t, resp, textContentType)
		assert.BodySize(t, resp, int(wantBytes))
	})

	t.Run("ok_range", func(t *testing.T) {
		t.Parallel()

		url := "/range/100"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=10-24")

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range100")
		assert.Header(t, resp, "Accept-Ranges", "bytes")
		assert.Header(t, resp, "Content-Length", "15")
		assert.Header(t, resp, "Content-Range", "bytes 10-24/100")
		assert.BodyEquals(t, resp, "klmnopqrstuvwxy")
	})

	t.Run("ok_range_first_16_bytes", func(t *testing.T) {
		t.Parallel()

		url := "/range/1000"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=0-15")

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range1000")
		assert.Header(t, resp, "Accept-Ranges", "bytes")
		assert.Header(t, resp, "Content-Length", "16")
		assert.Header(t, resp, "Content-Range", "bytes 0-15/1000")
		assert.BodyEquals(t, resp, "abcdefghijklmnop")
	})

	t.Run("ok_range_open_ended_last_6_bytes", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=20-")

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range26")
		assert.Header(t, resp, "Content-Length", "6")
		assert.Header(t, resp, "Content-Range", "bytes 20-25/26")
		assert.BodyEquals(t, resp, "uvwxyz")
	})

	t.Run("ok_range_suffix", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=-5")

		resp := must.DoReq(t, client, req)
		t.Logf("headers = %v", resp.Header)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range26")
		assert.Header(t, resp, "Content-Length", "5")
		assert.Header(t, resp, "Content-Range", "bytes 21-25/26")
		assert.BodyEquals(t, resp, "vwxyz")
	})

	t.Run("err_range_out_of_bounds", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", url)
		req.Header.Add("Range", "bytes=-5")

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range26")
		assert.Header(t, resp, "Content-Length", "5")
		assert.Header(t, resp, "Content-Range", "bytes 21-25/26")
		assert.BodyEquals(t, resp, "vwxyz")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusOK)
			assert.BodyEquals(t, resp, "abcdefghijklmnopqrstuvwxyz")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestHTML(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/html")
	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `<h1>Herman Melville - Moby-Dick</h1>`)
}

func TestRobots(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/robots.txt")
	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyContains(t, resp, `Disallow: /deny`)
}

func TestDeny(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/deny")
	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyContains(t, resp, `YOU SHOULDN'T BE HERE`)
}

func TestCache(t *testing.T) {
	t.Run("ok_no_cache", func(t *testing.T) {
		t.Parallel()

		url := "/cache"
		req := newTestRequest(t, "GET", url)
		resp := must.DoReq(t, client, req)

		_ = mustParseResponse[noBodyResponse](t, resp)
		lastModified := resp.Header.Get("Last-Modified")
		if lastModified == "" {
			t.Fatalf("expected Last-Modified header")
		}
		assert.Header(t, resp, "ETag", sha1hash(lastModified))
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusNotModified)
		})
	}
}

func TestCacheControl(t *testing.T) {
	t.Run("ok_cache_control", func(t *testing.T) {
		t.Parallel()

		url := "/cache/60"
		req := newTestRequest(t, "GET", url)
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, jsonContentType)
		assert.Header(t, resp, "Cache-Control", "public, max-age=60")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestETag(t *testing.T) {
	t.Run("ok_no_headers", func(t *testing.T) {
		t.Parallel()

		url := "/etag/abc"
		req := newTestRequest(t, "GET", url)
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.Header(t, resp, "ETag", `"abc"`)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestBytes(t *testing.T) {
	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/1024"
		req := newTestRequest(t, "GET", url)
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, binaryContentType)
		assert.BodySize(t, resp, 1024)
	})

	t.Run("ok_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/16?seed=1234567890"
		req := newTestRequest(t, "GET", url)

		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, binaryContentType)

		want := "\xbf\xcd*\xfa\x15\xa2\xb3r\xc7\a\x98Z\"\x02J\x8e"
		assert.BodyEquals(t, resp, want)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			assert.StatusCode(t, resp, http.StatusOK)
			assert.Header(t, resp, "Content-Length", strconv.Itoa(test.expectedContentLength))
			assert.BodySize(t, resp, test.expectedContentLength)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)

			// Expect empty content-length due to streaming response
			assert.Header(t, resp, "Content-Length", "")
			assert.DeepEqual(t, resp.TransferEncoding, []string{"chunked"}, "incorrect Transfer-Encoding header")
			assert.BodySize(t, resp, test.expectedContentLength)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestLinks(t *testing.T) {
	for _, env := range envs {
		env := env

		redirectTests := []struct {
			url              string
			expectedLocation string
		}{
			{"/links/1", "/links/1/0"},
			{"/links/100", "/links/100/0"},
		}

		for _, test := range redirectTests {
			test := test
			t.Run("ok"+env.prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", env.prefix+test.url, env)
				resp := must.DoReq(t, env.client, req)
				defer consumeAndCloseBody(resp)
				assert.StatusCode(t, resp, http.StatusFound)
				assert.Header(t, resp, "Location", env.prefix+test.expectedLocation)
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
			t.Run("error"+env.prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", env.prefix+test.url, env)
				resp := must.DoReq(t, env.client, req)
				defer consumeAndCloseBody(resp)
				assert.StatusCode(t, resp, test.expectedStatus)
			})
		}

		linksPageTests := []struct {
			url             string
			expectedContent string
		}{
			{"/links/2/0", `<html><head><title>Links</title></head><body>0 <a href="%[1]s/links/2/1">1</a> </body></html>`},
			{"/links/2/1", `<html><head><title>Links</title></head><body><a href="%[1]s/links/2/0">0</a> 1 </body></html>`},

			// offsets too large and too small are ignored
			{"/links/2/2", `<html><head><title>Links</title></head><body><a href="%[1]s/links/2/0">0</a> <a href="%[1]s/links/2/1">1</a> </body></html>`},
			{"/links/2/10", `<html><head><title>Links</title></head><body><a href="%[1]s/links/2/0">0</a> <a href="%[1]s/links/2/1">1</a> </body></html>`},
			{"/links/2/-1", `<html><head><title>Links</title></head><body><a href="%[1]s/links/2/0">0</a> <a href="%[1]s/links/2/1">1</a> </body></html>`},
		}
		for _, test := range linksPageTests {
			test := test
			t.Run("ok"+env.prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", env.prefix+test.url, env)
				resp := must.DoReq(t, env.client, req)
				defer consumeAndCloseBody(resp)
				assert.StatusCode(t, resp, http.StatusOK)
				assert.ContentType(t, resp, htmlContentType)
				expectedContent := fmt.Sprintf(test.expectedContent, env.prefix)
				assert.BodyEquals(t, resp, expectedContent)
			})
		}
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
			if test.expectedContentType != "" {
				assert.ContentType(t, resp, test.expectedContentType)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestXML(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/xml")
	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, "application/xml")
	assert.BodyContains(t, resp, `<?xml version='1.0' encoding='us-ascii'?>`)
}

func testValidUUIDv4(t *testing.T, uuid string) {
	t.Helper()
	assert.Equal(t, len(uuid), 36, "incorrect uuid length")
	req := regexp.MustCompile("^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[8|9|a|b][a-f0-9]{3}-[a-f0-9]{12}$")
	if !req.MatchString(uuid) {
		t.Fatalf("invalid uuid %q", uuid)
	}
}

func TestUUID(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/uuid")
	resp := must.DoReq(t, client, req)
	result := mustParseResponse[uuidResponse](t, resp)
	testValidUUIDv4(t, result.UUID)
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
			// Std base64 is also supported for decoding (+ instead of - in
			// encoded input string). See also:
			// https://github.com/mccutchen/go-httpbin/issues/152
			"/base64/decode/8J+Ziywg8J+MjSEK4oCm",
			"🙋, 🌍!\n…",
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusOK)
			assert.ContentType(t, resp, textContentType)
			assert.BodyEquals(t, resp, test.want)
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
			"/base64/decode/" + strings.Repeat("X", int(maxBodySize)+1),
			"input data exceeds max length",
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
			"decode failed",
		},
		{
			"/base64/unknown/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"invalid operation: unknown",
		},
	}

	for _, test := range errorTests {
		test := test
		t.Run("error"+test.requestURL, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", test.requestURL)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusBadRequest)
			assert.BodyContains(t, resp, test.expectedBodyContains)
		})
	}
}

func TestDumpRequest(t *testing.T) {
	t.Parallel()

	req := newTestRequest(t, "GET", "/dump/request?foo=bar")
	req.Host = "test-host"
	req.Header.Set("x-test-header2", "Test-Value2")
	req.Header.Set("x-test-header1", "Test-Value1")

	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyEquals(t, resp, "GET /dump/request?foo=bar HTTP/1.1\r\nHost: test-host\r\nAccept-Encoding: gzip\r\nUser-Agent: Go-http-client/1.1\r\nX-Test-Header1: Test-Value1\r\nX-Test-Header2: Test-Value2\r\n\r\n")
}

func TestJSON(t *testing.T) {
	t.Parallel()
	req := newTestRequest(t, "GET", "/json")
	resp := must.DoReq(t, client, req)
	assert.ContentType(t, resp, jsonContentType)
	assert.BodyContains(t, resp, `Wake up to WonderWidgets!`)
}

func TestBearer(t *testing.T) {
	requestURL := "/bearer"

	t.Run("valid_token", func(t *testing.T) {
		t.Parallel()

		token := "valid_token"
		req := newTestRequest(t, "GET", requestURL)
		req.Header.Set("Authorization", "Bearer "+token)

		resp := must.DoReq(t, client, req)
		result := mustParseResponse[bearerResponse](t, resp)
		want := bearerResponse{
			Authenticated: true,
			Token:         token,
		}
		assert.DeepEqual(t, result, want, "auth response mismatch")
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.Header(t, resp, "WWW-Authenticate", "Bearer")
			assert.StatusCode(t, resp, http.StatusUnauthorized)
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
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, http.StatusNotImplemented)
		})
	}
}

func TestHostname(t *testing.T) {
	t.Run("default hostname", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", "/hostname")
		resp := must.DoReq(t, client, req)
		result := mustParseResponse[hostnameResponse](t, resp)
		assert.Equal(t, result.Hostname, DefaultHostname, "hostname mismatch")
	})

	t.Run("real hostname", func(t *testing.T) {
		t.Parallel()

		realHostname := "real-hostname"
		app := New(WithHostname(realHostname))
		srv, client := newTestServer(app)
		defer srv.Close()

		req, err := http.NewRequest("GET", srv.URL+"/hostname", nil)
		assert.NilError(t, err)

		resp, err := client.Do(req)
		assert.NilError(t, err)

		result := mustParseResponse[hostnameResponse](t, resp)
		assert.Equal(t, result.Hostname, realHostname, "hostname mismatch")
	})
}

func TestSSE(t *testing.T) {
	t.Parallel()

	parseServerSentEvent := func(t *testing.T, buf *bufio.Reader) (serverSentEvent, error) {
		t.Helper()

		// match "event: ping" line
		eventLine, err := buf.ReadBytes('\n')
		if err != nil {
			return serverSentEvent{}, err
		}
		_, eventType, _ := bytes.Cut(eventLine, []byte(":"))
		assert.Equal(t, string(bytes.TrimSpace(eventType)), "ping", "unexpected event type")

		// match "data: {...}" line
		dataLine, err := buf.ReadBytes('\n')
		if err != nil {
			return serverSentEvent{}, err
		}
		_, data, _ := bytes.Cut(dataLine, []byte(":"))
		var event serverSentEvent
		assert.NilError(t, json.Unmarshal(data, &event))

		// match newline after event data
		b, err := buf.ReadByte()
		if err != nil && err != io.EOF {
			assert.NilError(t, err)
		}
		if b != '\n' {
			t.Fatalf("expected newline after event data, got %q", b)
		}

		return event, nil
	}

	parseServerSentEventStream := func(t *testing.T, resp *http.Response) []serverSentEvent {
		t.Helper()
		buf := bufio.NewReader(resp.Body)
		var events []serverSentEvent
		for {
			event, err := parseServerSentEvent(t, buf)
			if err == io.EOF {
				break
			}
			assert.NilError(t, err)
			events = append(events, event)
		}
		return events
	}

	okTests := []struct {
		params   *url.Values
		duration time.Duration
		count    int
	}{
		// there are useful defaults for all values
		{&url.Values{}, 0, 10},

		// go-style durations are accepted
		{&url.Values{"duration": {"5ms"}}, 5 * time.Millisecond, 10},
		{&url.Values{"duration": {"10ns"}}, 0, 10},
		{&url.Values{"delay": {"5ms"}}, 5 * time.Millisecond, 10},
		{&url.Values{"delay": {"0h"}}, 0, 10},

		// or floating point seconds
		{&url.Values{"duration": {"0.25"}}, 250 * time.Millisecond, 10},
		{&url.Values{"duration": {"1"}}, 1 * time.Second, 10},
		{&url.Values{"delay": {"0.25"}}, 250 * time.Millisecond, 10},
		{&url.Values{"delay": {"0"}}, 0, 10},

		{&url.Values{"count": {"1"}}, 0, 1},
		{&url.Values{"count": {"011"}}, 0, 11},
		{&url.Values{"count": {fmt.Sprintf("%d", app.maxSSECount)}}, 0, int(app.maxSSECount)},

		{&url.Values{"duration": {"250ms"}, "delay": {"250ms"}}, 500 * time.Millisecond, 10},
		{&url.Values{"duration": {"250ms"}, "delay": {"0.25s"}}, 500 * time.Millisecond, 10},
	}
	for _, test := range okTests {
		test := test
		t.Run(fmt.Sprintf("ok/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()

			url := "/sse?" + test.params.Encode()

			start := time.Now()
			req := newTestRequest(t, "GET", url)
			resp := must.DoReq(t, client, req)
			assert.StatusCode(t, resp, http.StatusOK)
			events := parseServerSentEventStream(t, resp)

			if elapsed := time.Since(start); elapsed < test.duration {
				t.Fatalf("expected minimum duration of %s, request took %s", test.duration, elapsed)
			}
			assert.ContentType(t, resp, sseContentType)
			assert.DeepEqual(t, resp.TransferEncoding, []string{"chunked"}, "unexpected Transfer-Encoding header")
			assert.Equal(t, len(events), test.count, "unexpected number of events")
		})
	}

	badTests := []struct {
		params *url.Values
		code   int
	}{
		{&url.Values{"duration": {"0"}}, http.StatusBadRequest},
		{&url.Values{"duration": {"0s"}}, http.StatusBadRequest},
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

		{&url.Values{"count": {"foo"}}, http.StatusBadRequest},
		{&url.Values{"count": {"0"}}, http.StatusBadRequest},
		{&url.Values{"count": {"-1"}}, http.StatusBadRequest},
		{&url.Values{"count": {"0xff"}}, http.StatusBadRequest},
		{&url.Values{"count": {fmt.Sprintf("%d", app.maxSSECount+1)}}, http.StatusBadRequest},

		// request would take too long
		{&url.Values{"duration": {"750ms"}, "delay": {"500ms"}}, http.StatusBadRequest},
	}
	for _, test := range badTests {
		test := test
		t.Run(fmt.Sprintf("bad/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()
			url := "/sse?" + test.params.Encode()
			req := newTestRequest(t, "GET", url)
			resp := must.DoReq(t, client, req)
			defer consumeAndCloseBody(resp)
			assert.StatusCode(t, resp, test.code)
		})
	}

	t.Run("writes are actually incremmental", func(t *testing.T) {
		t.Parallel()

		var (
			duration = 100 * time.Millisecond
			count    = 3
			endpoint = fmt.Sprintf("/sse?duration=%s&count=%d", duration, count)

			// Match server logic for calculating the delay between writes
			wantPauseBetweenWrites = duration / time.Duration(count-1)
		)

		req := newTestRequest(t, "GET", endpoint)
		resp := must.DoReq(t, client, req)
		buf := bufio.NewReader(resp.Body)
		eventCount := 0

		// Here we read from the response one byte at a time, and ensure that
		// at least the expected delay occurs for each read.
		//
		// The request above includes an initial delay equal to the expected
		// wait between writes so that even the first iteration of this loop
		// expects to wait the same amount of time for a read.
		for i := 0; ; i++ {
			start := time.Now()
			event, err := parseServerSentEvent(t, buf)
			if err == io.EOF {
				break
			}
			assert.NilError(t, err)
			gotPause := time.Since(start)

			// We expect to read exactly one byte on each iteration. On the
			// last iteration, we expct to hit EOF after reading the final
			// byte, because the server does not pause after the last write.
			assert.Equal(t, event.ID, i, "unexpected SSE event ID")

			// only ensure that we pause for the expected time between writes
			// (allowing for minor mismatch in local timers and server timers)
			// after the first byte.
			if i > 0 {
				assert.RoughDuration(t, gotPause, wantPauseBetweenWrites, 3*time.Millisecond)
			}

			eventCount++
		}

		assert.Equal(t, eventCount, count, "unexpected number of events")
	})

	t.Run("handle cancelation during initial delay", func(t *testing.T) {
		t.Parallel()

		// For this test, we expect the client to time out and cancel the
		// request after 10ms.  The handler should still be in its intitial
		// delay period, so this will result in a request error since no status
		// code will be written before the cancelation.
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/sse?duration=500ms&delay=500ms").WithContext(ctx)
		if _, err := client.Do(req); !os.IsTimeout(err) {
			t.Fatalf("expected timeout error, got %s", err)
		}
	})

	t.Run("handle cancelation during stream", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", "/sse?duration=900ms&delay=0&count=2").WithContext(ctx)
		resp := must.DoReq(t, client, req)
		defer consumeAndCloseBody(resp)

		// In this test, the server should have started an OK response before
		// our client timeout cancels the request, so we should get an OK here.
		assert.StatusCode(t, resp, http.StatusOK)

		// But, we should time out while trying to read the whole response
		// body.
		body, err := io.ReadAll(resp.Body)
		if !os.IsTimeout(err) {
			t.Fatalf("expected timeout reading body, got %s", err)
		}

		// partial read should include the first whole event
		event, err := parseServerSentEvent(t, bufio.NewReader(bytes.NewReader(body)))
		assert.NilError(t, err)
		assert.Equal(t, event.ID, 0, "unexpected SSE event ID")
	})

	t.Run("ensure HEAD request works with streaming responses", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "HEAD", "/sse?duration=900ms&delay=100ms")
		resp := must.DoReq(t, client, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodySize(t, resp, 0)
	})
}

func TestWebSocketEcho(t *testing.T) {
	// ========================================================================
	// Note: Here we only test input validation for the websocket endpoint.
	//
	// See websocket/*_test.go for in-depth integration tests of the actual
	// websocket implementation.
	// ========================================================================

	handshakeHeaders := map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
		"Sec-WebSocket-Version": "13",
	}

	t.Run("handshake ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, http.MethodGet, "/websocket/echo")
		for k, v := range handshakeHeaders {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		assert.NilError(t, err)
		assert.StatusCode(t, resp, http.StatusSwitchingProtocols)
	})

	t.Run("handshake failed", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, http.MethodGet, "/websocket/echo")
		resp, err := client.Do(req)
		assert.NilError(t, err)
		assert.StatusCode(t, resp, http.StatusBadRequest)
	})

	paramTests := []struct {
		query      string
		wantStatus int
	}{
		// ok
		{"max_fragment_size=1&max_message_size=2", http.StatusSwitchingProtocols},
		{fmt.Sprintf("max_fragment_size=%d&max_message_size=%d", app.MaxBodySize, app.MaxBodySize), http.StatusSwitchingProtocols},

		// bad max_framgent_size
		{"max_fragment_size=-1&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=0&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=3&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=foo&max_message_size=2", http.StatusBadRequest},
		{fmt.Sprintf("max_fragment_size=%d&max_message_size=2", app.MaxBodySize+1), http.StatusBadRequest},

		// bad max_message_size
		{"max_fragment_size=1&max_message_size=0", http.StatusBadRequest},
		{"max_fragment_size=1&max_message_size=-1", http.StatusBadRequest},
		{"max_fragment_size=1&max_message_size=bar", http.StatusBadRequest},
		{fmt.Sprintf("max_fragment_size=1&max_message_size=%d", app.MaxBodySize+1), http.StatusBadRequest},
	}
	for _, tc := range paramTests {
		tc := tc
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, http.MethodGet, "/websocket/echo?"+tc.query)
			for k, v := range handshakeHeaders {
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, tc.wantStatus)
		})
	}
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

func newTestEnvironment(app *HTTPBin) (env *environment) {
	env = new(environment)
	env.srv, env.client = newTestServer(app)
	env.prefix = app.prefix
	return
}

func newTestRequest(t *testing.T, verb, path string, envs ...*environment) *http.Request {
	t.Helper()
	return newTestRequestWithBody(t, verb, path, nil, envs...)
}

func newTestRequestWithBody(t *testing.T, verb, path string, body io.Reader, envs ...*environment) *http.Request {
	t.Helper()

	var env *environment
	if len(envs) == 0 {
		env = defaultEnv
	} else if len(envs) == 1 {
		env = envs[0]
	} else {
		t.Fatal("Only zero or one environment are allowed")
	}
	req, err := http.NewRequest(verb, env.srv.URL+path, body)
	assert.NilError(t, err)
	return req
}

func mustParseResponse[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	assert.StatusCode(t, resp, http.StatusOK)
	assert.ContentType(t, resp, jsonContentType)
	return must.Unmarshal[T](t, resp.Body)
}

func consumeAndCloseBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func assertHeaderEqual(t *testing.T, header *http.Header, key, want string) {
	t.Helper()
	got := header.Get(key)
	if want != got {
		t.Fatalf("expected header %s=%#v, got %#v", key, want, got)
	}
}
