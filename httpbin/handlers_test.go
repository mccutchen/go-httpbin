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
	"html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/must"
)

// appTestInfo carries the setup necessary for each unit test below, forming
// the basis for a mini test "framework" used across the test suite. It
// comprises
type appTestInfo struct {
	// App is the [HTTPBin] instance under test, configured by [createApp].
	App *HTTPBin
	// Srv is an [httptest.Server] running that instance.
	Srv *httptest.Server
	// Client is an [http.Client] configured to connect to the test server.
	Client *http.Client
}

// URL generates the full URL for the given path and optional query params,
// pointing at the current test server.
func (appT *appTestInfo) URL(path string, params ...url.Values) string {
	u := appT.Srv.URL + path
	for i, p := range params {
		if i == 0 && p != nil { // ignore nil params always passed through by some helpers (e.g. doGetRequest)
			u += "?"
		}
		u += p.Encode()
	}
	return u
}

// setupTestApp creates an [HTTPBin] instance with the given opts, starts a
// new [httptest.Server], and configures a client for that server. The
// returned struct encompasses all three.
//
// The server will be closed automatically when the test ends.
func setupTestApp(t *testing.T, opts ...OptionFunc) *appTestInfo {
	app := createApp(opts...)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Timeout = 5 * time.Second
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &appTestInfo{
		App:    app,
		Srv:    srv,
		Client: client,
	}
}

// createApp creates an [HTTPBin] instance with default configuration, which
// can be overridden by the given opts.
func createApp(opts ...OptionFunc) *HTTPBin {
	defaults := append([]OptionFunc{},
		WithDefaultParams(DefaultParams{
			DripDelay:    0,
			DripDuration: 100 * time.Millisecond,
			DripNumBytes: 10,
			SSECount:     10,
			SSEDelay:     0,
			SSEDuration:  100 * time.Millisecond,
		}),
		WithMaxBodySize(1024),
		WithMaxDuration(1*time.Second),
		WithObserver(StdLogObserver(slog.New(slog.NewTextHandler(io.Discard, nil)))),
	)
	return New(append(defaults, opts...)...)
}

func TestMain(m *testing.M) {
	// enable additional safety checks
	testMode = true
	os.Exit(m.Run())
}

func TestIndex(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", "/test-prefix"} {
		t.Run("ok"+prefix, func(t *testing.T) {
			t.Parallel()
			app := setupTestApp(t, WithPrefix(prefix))
			req := newTestRequest(t, "GET", app.URL(prefix+"/"), nil)
			resp := mustDoRequest(t, app, req)
			assert.ContentType(t, resp, htmlContentType)
			assert.Header(t, resp, "Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' camo.githubusercontent.com")
			body := must.ReadAll(t, resp.Body)
			assert.Contains(t, body, "go-httpbin", "body")
			assert.Contains(t, body, prefix+"/get", "body")
		})

		t.Run("not found"+prefix, func(t *testing.T) {
			t.Parallel()
			app := setupTestApp(t, WithPrefix(prefix))
			req := newTestRequest(t, "GET", app.URL(prefix+"/foo"), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusNotFound)
			assert.ContentType(t, resp, textContentType)
		})
	}
}

func TestEnv(t *testing.T) {
	t.Parallel()
	t.Run("default environment", func(t *testing.T) {
		t.Parallel()
		app := setupTestApp(t)
		req := newTestRequest(t, "GET", app.URL("/env"), nil)
		resp := mustDoRequest(t, app, req)
		result := mustParseResponse[envResponse](t, resp)
		assert.Equal(t, len(result.Env), 0, "environment variables unexpected")
	})
}

func TestFormsPost(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/forms/post"), nil)
	resp := mustDoRequest(t, app, req)

	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `<form method="post" action="/post">`)
}

func TestUTF8(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/encoding/utf8"), nil)
	resp := mustDoRequest(t, app, req)

	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `Hello world, Καλημέρα κόσμε, コンニチハ`)
}

func TestGet(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t, WithExcludeHeaders("x-ignore-*,x-info-this-key"))

	doGetRequest := func(t *testing.T, path string, params url.Values, headers http.Header) noBodyResponse {
		t.Helper()
		req := newTestRequest(t, "GET", app.URL(path, params), nil)
		req.Header.Set("User-Agent", "test")
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp := mustDoRequest(t, app, req)
		return mustParseResponse[noBodyResponse](t, resp)
	}

	t.Run("basic", func(t *testing.T) {
		t.Parallel()

		result := doGetRequest(t, "/get", nil, nil)
		assert.Equal(t, result.Method, "GET", "method mismatch")
		assert.Equal(t, result.Args.Encode(), "", "expected empty args")
		assert.Equal(t, result.URL, app.URL("/get"), "url mismatch")

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
		assert.Equal(t, result.Headers.Get("X-Ignore-Foo"), "", "unexpected header")
		assert.Equal(t, result.Headers.Get("x-info-this-key"), "", "unexpected header")
		assert.Equal(t, result.Headers.Get("X-Info-Foo"), "bar", "incorrect header")
	})

	t.Run("only_allows_gets", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "POST", app.URL("/get"), nil)
		resp := mustDoRequest(t, app, req)

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
	t.Parallel()
	app := setupTestApp(t)
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
		t.Run(fmt.Sprintf("%s %s", tc.verb, tc.path), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, tc.verb, app.URL(tc.path), nil)
			resp := mustDoRequest(t, app, req)
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
	t.Parallel()
	app := setupTestApp(t)

	t.Run("no_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", app.URL("/get"), nil)
		resp := mustDoRequest(t, app, req)
		assert.Header(t, resp, "Access-Control-Allow-Origin", "*")
	})

	t.Run("with_request_origin", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", app.URL("/get"), nil)
		req.Header.Set("Origin", "origin")
		resp := mustDoRequest(t, app, req)
		assert.Header(t, resp, "Access-Control-Allow-Origin", "origin")
	})

	t.Run("options_request", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "OPTIONS", app.URL("/get"), nil)
		resp := mustDoRequest(t, app, req)
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

	t.Run("allow_headers", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "OPTIONS", app.URL("/get"), nil)
		req.Header.Set("Access-Control-Request-Headers", "X-Test-Header")
		resp := mustDoRequest(t, app, req)
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
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// this test does not use a real server, because we need to control
			// the RemoteAddr field on the request object to make the test
			// deterministic.
			app := createApp()
			w := httptest.NewRecorder()

			req, _ := http.NewRequest("GET", "/ip", nil)
			req.RemoteAddr = tc.remoteAddr
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			app.ServeHTTP(w, req)
			assert.Equal(t, w.Code, http.StatusOK, "wrong status code")
			assert.Equal(t, w.Header().Get("Content-Type"), jsonContentType, "wrong content type")
			result := must.Unmarshal[ipResponse](t, w.Body)
			assert.Equal(t, result.Origin, tc.wantOrigin, "incorrect origin")
		})
	}

	t.Run("via real connection", func(t *testing.T) {
		// (*Request).RemoteAddr includes the local port for real incoming TCP
		// connections but not for direct ServeHTTP calls as the used in the
		// httptest.NewRecorder tests above, so we need to use a real server
		// to verify handling of both cases.
		t.Parallel()

		app := setupTestApp(t)
		resp, err := app.Client.Get(app.Srv.URL + "/ip")
		assert.NilError(t, err)
		defer resp.Body.Close()

		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, jsonContentType)

		result := must.Unmarshal[ipResponse](t, resp.Body)
		assert.Equal(t, result.Origin, "127.0.0.1", "incorrect origin")
	})
}

func TestUserAgent(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/user-agent"), nil)
	req.Header.Set("User-Agent", "test")

	resp := mustDoRequest(t, app, req)
	result := mustParseResponse[userAgentResponse](t, resp)
	assert.Equal(t, "test", result.UserAgent, "incorrect user agent")
}

func TestHeaders(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/headers"), nil)
	req.Host = "test-host"
	req.Header.Set("User-Agent", "test")
	req.Header.Set("Foo-Header", "foo")
	req.Header.Add("Bar-Header", "bar1")
	req.Header.Add("Bar-Header", "bar2")

	resp := mustDoRequest(t, app, req)
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
	t.Parallel()
	testRequestWithBody(t, setupTestApp(t), "POST", "/post")
}

func TestPut(t *testing.T) {
	t.Parallel()
	testRequestWithBody(t, setupTestApp(t), "PUT", "/put")
}

func TestDelete(t *testing.T) {
	t.Parallel()
	testRequestWithBody(t, setupTestApp(t), "DELETE", "/delete")
}

func TestPatch(t *testing.T) {
	t.Parallel()
	testRequestWithBody(t, setupTestApp(t), "PATCH", "/patch")
}

func TestAnything(t *testing.T) {
	t.Parallel()

	var (
		app   = setupTestApp(t)
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
			testRequestWithBody(t, app, verb, path)
		}
	}

	t.Run("HEAD", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "HEAD", app.URL("/anything"), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodyEquals(t, resp, "")
		assert.Header(t, resp, "Content-Length", "") // responses to HEAD requests should not have a Content-Length header
	})
}

func testRequestWithBody(t *testing.T, app *appTestInfo, verb, path string) {
	t.Run("BinaryBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyBinaryBody(t, app, verb, path)
	})
	t.Run("BodyTooBig", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyBodyTooBig(t, app, verb, path)
	})
	t.Run("EmptyBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyEmptyBody(t, app, verb, path)
	})
	t.Run("Expect100Continue", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyExpect100Continue(t, app, verb, path)
	})
	t.Run("FormEncodedBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyFormEncodedBody(t, app, verb, path)
	})
	t.Run("FormEncodedBodyNoContentType", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyFormEncodedBodyNoContentType(t, app, verb, path)
	})
	t.Run("HTML", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyHTML(t, app, verb, path)
	})
	t.Run("InvalidFormEncodedBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyInvalidFormEncodedBody(t, app, verb, path)
	})
	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyInvalidJSON(t, app, verb, path)
	})
	t.Run("InvalidMultiPartBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyInvalidMultiPartBody(t, app, verb, path)
	})
	t.Run("JSON", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyJSON(t, app, verb, path)
	})
	t.Run("MultiPartBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyMultiPartBody(t, app, verb, path)
	})
	t.Run("MultiPartBodyFiles", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyMultiPartBodyFiles(t, app, verb, path)
	})
	t.Run("QueryParams", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyQueryParams(t, app, verb, path)
	})
	t.Run("QueryParamsAndBody", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyQueryParamsAndBody(t, app, verb, path)
	})
	t.Run("TransferEncoding", func(t *testing.T) {
		t.Parallel()
		testRequestWithBodyTransferEncoding(t, app, verb, path)
	})
}

func testRequestWithBodyBinaryBody(t *testing.T, app *appTestInfo, verb string, path string) {
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
		t.Run("content type/"+test.contentType, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, verb, app.URL(path), bytes.NewReader([]byte(test.requestBody)))
			req.Header.Set("Content-Type", test.contentType)

			resp := mustDoRequest(t, app, req)
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

func testRequestWithBodyEmptyBody(t *testing.T, app *appTestInfo, verb string, path string) {
	tests := []struct {
		contentType string
	}{
		{""},
		{"application/json; charset=utf-8"},
		{"application/x-www-form-urlencoded"},
		{"multipart/form-data; foo"},
	}
	for _, test := range tests {
		t.Run("content type/"+test.contentType, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, verb, app.URL(path), nil)
			req.Header.Set("Content-Type", test.contentType)

			resp := mustDoRequest(t, app, req)
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

func testRequestWithBodyFormEncodedBody(t *testing.T, app *appTestInfo, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequest(t, verb, app.URL(path), strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp := mustDoRequest(t, app, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.DeepEqual(t, result.Form, params, "form data mismatch")
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.JSON, nil, "expected nil json")
}

func testRequestWithBodyHTML(t *testing.T, app *appTestInfo, verb, path string) {
	data := "<html><body><h1>hello world</h1></body></html>"

	req := newTestRequest(t, verb, app.URL(path), strings.NewReader(data))
	req.Header.Set("Content-Type", htmlContentType)

	resp := mustDoRequest(t, app, req)
	assert.StatusCode(t, resp, http.StatusOK)
	assert.ContentType(t, resp, jsonContentType)
	assert.BodyContains(t, resp, data)
}

func testRequestWithBodyExpect100Continue(t *testing.T, app *appTestInfo, verb, path string) {
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

		conn, err := net.Dial("tcp", app.Srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		body := []byte("test body")

		req := newTestRequest(t, verb, app.URL(path), bytes.NewReader(body))
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

		conn, err := net.Dial("tcp", app.Srv.Listener.Addr().String())
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
		// The Go stdlib's Expect:100-continue handling requires either a)
		// non-zero Content-Length header or b) Transfer-Encoding:chunked
		// header to be present.  Otherwise, the Expect header is ignored and
		// the request is processed normally.
		t.Parallel()

		conn, err := net.Dial("tcp", app.Srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		req := newTestRequest(t, verb, app.URL(path), nil)
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

func testRequestWithBodyFormEncodedBodyNoContentType(t *testing.T, app *appTestInfo, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequest(t, verb, app.URL(path), strings.NewReader(params.Encode()))
	resp := mustDoRequest(t, app, req)
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

func testRequestWithBodyMultiPartBody(t *testing.T, app *appTestInfo, verb, path string) {
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

	req := newTestRequest(t, verb, app.URL(path), bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp := mustDoRequest(t, app, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Args, nilValues, "expected empty args")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, params, "form values mismatch")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")
}

func testRequestWithBodyMultiPartBodyFiles(t *testing.T, app *appTestInfo, verb, path string) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Add a file to the multipart request
	part, _ := mw.CreateFormFile("fieldname", "filename")
	part.Write([]byte("hello world"))
	mw.Close()

	req := newTestRequest(t, verb, app.URL(path), bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp := mustDoRequest(t, app, req)
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

func testRequestWithBodyInvalidFormEncodedBody(t *testing.T, app *appTestInfo, verb, path string) {
	req := newTestRequest(t, verb, app.URL(path), strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := mustDoRequest(t, app, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyInvalidMultiPartBody(t *testing.T, app *appTestInfo, verb, path string) {
	req := newTestRequest(t, verb, app.URL(path), strings.NewReader("%ZZ"))
	req.Header.Set("Content-Type", "multipart/form-data; etc")
	resp := mustDoRequest(t, app, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyJSON(t *testing.T, app *appTestInfo, verb, path string) {
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

	req := newTestRequest(t, verb, app.URL(path), bytes.NewReader(inputBody))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp := mustDoRequest(t, app, req)
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

func testRequestWithBodyInvalidJSON(t *testing.T, app *appTestInfo, verb, path string) {
	req := newTestRequest(t, verb, app.URL(path), strings.NewReader("foo"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp := mustDoRequest(t, app, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyBodyTooBig(t *testing.T, app *appTestInfo, verb, path string) {
	body := make([]byte, app.App.MaxBodySize+1)
	req := newTestRequest(t, verb, app.URL(path), bytes.NewReader(body))
	resp := mustDoRequest(t, app, req)
	assert.StatusCode(t, resp, http.StatusBadRequest)
}

func testRequestWithBodyQueryParams(t *testing.T, app *appTestInfo, verb, path string) {
	params := url.Values{}
	params.Set("foo", "foo")
	params.Add("bar", "bar1")
	params.Add("bar", "bar2")

	req := newTestRequest(t, verb, app.URL(path, params), nil)
	resp := mustDoRequest(t, app, req)
	result := mustParseResponse[bodyResponse](t, resp)

	assert.DeepEqual(t, result.Args, params, "args mismatch")

	// extra validation
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.DeepEqual(t, result.Files, nilValues, "expected empty files")
	assert.DeepEqual(t, result.Form, nilValues, "form values mismatch")
	assert.DeepEqual(t, result.JSON, nil, "expected nil JSON")
}

func testRequestWithBodyQueryParamsAndBody(t *testing.T, app *appTestInfo, verb, path string) {
	args := url.Values{}
	args.Set("query1", "foo")
	args.Add("query2", "bar1")
	args.Add("query2", "bar2")

	form := url.Values{}
	form.Set("form1", "foo")
	form.Add("form2", "bar1")
	form.Add("form2", "bar2")

	req := newTestRequest(t, verb, app.URL(path, args), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp := mustDoRequest(t, app, req)

	result := mustParseResponse[bodyResponse](t, resp)
	assert.Equal(t, result.Method, verb, "method mismatch")
	assert.Equal(t, result.Args.Encode(), args.Encode(), "args mismatch")
	assert.Equal(t, result.Form.Encode(), form.Encode(), "form mismatch")
}

func testRequestWithBodyTransferEncoding(t *testing.T, app *appTestInfo, verb, path string) {
	testCases := []struct {
		given string
		want  string
	}{
		{"", ""},
		{"identity", ""},
		{"chunked", "chunked"},
	}
	for _, tc := range testCases {
		t.Run("transfer-encoding/"+tc.given, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, verb, app.URL(path), bytes.NewReader([]byte("{}")))
			if tc.given != "" {
				req.TransferEncoding = []string{tc.given}
			}

			resp := mustDoRequest(t, app, req)
			result := mustParseResponse[bodyResponse](t, resp)
			got := result.Headers.Get("Transfer-Encoding")
			assert.Equal(t, got, tc.want, "Transfer-Encoding header mismatch")
		})
	}
}

// TODO: implement and test more complex /status endpoint
func TestStatus(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
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
		t.Run(fmt.Sprintf("ok/status/%d", test.code), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(fmt.Sprintf("/status/%d", test.code)), nil)
			resp := mustDoRequest(t, app, req)
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
		{"/status/", http.StatusNotFound},
		{"/status/200/foo", http.StatusNotFound},
		{"/status/3.14", http.StatusBadRequest},
		{"/status/foo", http.StatusBadRequest},
		{"/status/600", http.StatusBadRequest},
		{"/status/1024", http.StatusBadRequest},
	}

	for _, test := range errorTests {
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
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

		conn, err := net.Dial("tcp", app.Srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		req := newTestRequest(t, "GET", app.URL("/status/100"), nil)
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
			req := newTestRequest(t, "GET", app.URL("/status/200:0.7,429:0.2,503:0.1"), nil)
			resp := mustDoRequest(t, app, req)
			if resp.StatusCode != 200 && resp.StatusCode != 429 && resp.StatusCode != 503 {
				t.Fatalf("expected status code 200, 429, or 503, got %d", resp.StatusCode)
			}
		})

		t.Run("bad weight", func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/status/200:foo,500:1"), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})

		t.Run("bad choice", func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/status/200:1,foo:1"), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})
	})
}

func TestUnstable(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", app.URL("/unstable"), nil)
		resp := mustDoRequest(t, app, req)
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
		t.Run("ok_"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.status)
		})
	}

	edgeCaseTests := []string{
		// strange but valid seed
		"/unstable?seed=-12345",
	}
	for _, test := range edgeCaseTests {
		t.Run("bad"+test, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test), nil)
			resp := mustDoRequest(t, app, req)
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
		t.Run("bad"+test, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusBadRequest)
		})
	}
}

func TestResponseHeaders(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		wantHeaders := url.Values{
			"Foo": {"foo"},
			"Bar": {"bar1", "bar2"},
		}

		req := newTestRequest(t, "GET", app.URL("/response-headers", wantHeaders), nil)
		resp := mustDoRequest(t, app, req)
		result := mustParseResponse[http.Header](t, resp)

		for k, expectedValues := range wantHeaders {
			// expected headers should be present in the HTTP response itself
			respValues := resp.Header[k]
			assert.DeepEqual(t, respValues, expectedValues, "HTTP response headers mismatch")

			// they should also be reflected in the decoded JSON resposne
			resultValues := result[k]
			assert.DeepEqual(t, resultValues, expectedValues, "JSON response headers mismatch")
		}

		// if no content-type is specified in the request params, the response
		// defaults to JSON.
		//
		// Note that if this changes, we need to ensure we maintain safety
		// around escapig HTML in the response (see the subtest below)
		assert.Header(t, resp, "Content-Type", jsonContentType)
	})

	t.Run("override content-type", func(t *testing.T) {
		t.Parallel()

		contentType := "text/test"

		params := url.Values{}
		params.Set("Content-Type", contentType)

		req := newTestRequest(t, "GET", app.URL("/response-headers", params), nil)
		resp := mustDoRequest(t, app, req)

		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, contentType)
	})

	t.Run("escaping HTML content", func(t *testing.T) {
		dangerousString := "<img/src/onerror=alert('xss')>"

		for _, tc := range []struct {
			contentType  string
			shouldEscape bool
		}{
			// a tiny number of content types are considered safe and do not
			// require escaping (see isDangerousContentType)
			{"application/json; charset=utf8", false},
			{"text/plain", false},
			{"application/octet-string", false},

			// if no content-type is provided, we default to JSON, which is
			// safe
			{"", false},

			// everything else requires escaping
			{"application/xml", true},
			{"image/png", true},
			{"text/html; charset=utf8", true},
			{"text/html", true},
		} {
			t.Run(tc.contentType, func(t *testing.T) {
				t.Parallel()

				params := url.Values{}
				if tc.contentType != "" {
					params.Set("Content-Type", tc.contentType)
				}
				// need to ensure dangerous strings are escaped as both keys
				// and values
				params.Set("xss", dangerousString)
				params.Set(dangerousString, "xss")

				req := newTestRequest(t, "GET", app.URL("/response-headers", params), nil)
				resp := mustDoRequest(t, app, req)

				assert.StatusCode(t, resp, http.StatusOK)
				if tc.contentType != "" {
					assert.ContentType(t, resp, tc.contentType)
				} else {
					assert.ContentType(t, resp, jsonContentType)
				}

				gotParams := must.Unmarshal[url.Values](t, resp.Body)
				for key, wantVals := range params {
					if tc.shouldEscape {
						key = html.EscapeString(key)
					}
					gotVals := gotParams[key]
					assert.Equal(t, len(gotVals), len(wantVals), "unexpected number of values for key %q (escaped=%v)", key, tc.shouldEscape)
					for i, wantVal := range wantVals {
						gotVal := gotVals[i]
						if tc.shouldEscape {
							assert.Equal(t, gotVal, html.EscapeString(wantVal), "expected HTML-escaped value")
						} else {
							assert.Equal(t, gotVal, wantVal, "expected unescaped value")
						}
					}
				}
			})
		}
	})

	t.Run("dangerously not escaping responses", func(t *testing.T) {
		t.Parallel()

		app := setupTestApp(t, WithUnsafeAllowDangerousResponses())

		dangerousString := "<img/src/onerror=alert('xss')>"

		params := url.Values{}
		params.Set("Content-Type", "text/html")
		params.Set("xss", dangerousString)

		req := newTestRequest(t, "GET", app.URL("/response-headers", params), nil)
		resp := mustDoRequest(t, app, req)

		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, "text/html")

		// dangerous string is not escaped
		assert.BodyContains(t, resp, dangerousString)
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

	for _, prefix := range []string{"", "/test-prefix"} {
		app := setupTestApp(t, WithPrefix(prefix))
		for _, test := range tests {
			reqPath := fmt.Sprintf(test.requestURL, prefix)
			t.Run("ok"+reqPath, func(t *testing.T) {
				t.Parallel()

				req := newTestRequest(t, "GET", app.URL(reqPath), nil)
				req.Host = "host"
				resp := mustDoRequest(t, app, req)

				assert.StatusCode(t, resp, http.StatusFound)
				assert.Header(t, resp, "Location", fmt.Sprintf(test.expectedLocation, prefix))
			})
		}
	}

	errorTests := []struct {
		requestURL     string
		expectedStatus int
	}{
		{"%s/redirect", http.StatusNotFound},
		{"%s/redirect/", http.StatusNotFound},
		{"%s/redirect/-1", http.StatusBadRequest},
		{"%s/redirect/3.14", http.StatusBadRequest},
		{"%s/redirect/foo", http.StatusBadRequest},
		{"%s/redirect/10/foo", http.StatusNotFound},

		{"%s/relative-redirect", http.StatusNotFound},
		{"%s/relative-redirect/", http.StatusNotFound},
		{"%s/relative-redirect/-1", http.StatusBadRequest},
		{"%s/relative-redirect/3.14", http.StatusBadRequest},
		{"%s/relative-redirect/foo", http.StatusBadRequest},
		{"%s/relative-redirect/10/foo", http.StatusNotFound},

		{"%s/absolute-redirect", http.StatusNotFound},
		{"%s/absolute-redirect/", http.StatusNotFound},
		{"%s/absolute-redirect/-1", http.StatusBadRequest},
		{"%s/absolute-redirect/3.14", http.StatusBadRequest},
		{"%s/absolute-redirect/foo", http.StatusBadRequest},
		{"%s/absolute-redirect/10/foo", http.StatusNotFound},
	}

	for _, prefix := range []string{"", "/test-prefix"} {
		app := setupTestApp(t, WithPrefix(prefix))
		for _, test := range errorTests {
			reqPath := fmt.Sprintf(test.requestURL, prefix)
			t.Run("error"+reqPath, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", app.URL(reqPath), nil)
				resp := mustDoRequest(t, app, req)
				assert.StatusCode(t, resp, test.expectedStatus)
			})
		}
	}
}

func TestRedirectTo(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t, WithAllowedRedirectDomains([]string{
		"httpbingo.org",
		"example.org",
		"www.example.com",
	}))

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
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
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
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
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

		// See https://github.com/mccutchen/go-httpbin/issues/173
		{"/redirect-to?url=//evil.com", http.StatusForbidden}, // missing scheme to attempt to bypass allowlist
	}
	for _, test := range allowListTests {
		t.Run("allowlist"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatus)
			if test.expectedStatus >= 400 {
				assert.BodyEquals(t, resp, app.App.forbiddenRedirectError)
			}
		})
	}
}

func TestCookies(t *testing.T) {
	for _, prefix := range []string{"", "/test-prefix"} {
		app := setupTestApp(t, WithPrefix(prefix))

		t.Run("get"+prefix, func(t *testing.T) {
			testCases := map[string]struct {
				cookies cookiesResponse
			}{
				"ok/no cookies": {
					cookies: cookiesResponse{Cookies: map[string]string{}},
				},
				"ok/one cookie": {
					cookies: cookiesResponse{
						Cookies: map[string]string{
							"k1": "v1",
						},
					},
				},
				"ok/many cookies": {
					cookies: cookiesResponse{
						Cookies: map[string]string{
							"k1": "v1",
							"k2": "v2",
							"k3": "v3",
						},
					},
				},
			}

			for name, tc := range testCases {
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					req := newTestRequest(t, "GET", app.URL(prefix+"/cookies"), nil)
					for k, v := range tc.cookies.Cookies {
						req.AddCookie(&http.Cookie{
							Name:  k,
							Value: v,
						})
					}

					resp := mustDoRequest(t, app, req)
					result := mustParseResponse[cookiesResponse](t, resp)
					assert.DeepEqual(t, result, tc.cookies, "cookies mismatch")
				})
			}
		})

		t.Run("set"+prefix, func(t *testing.T) {
			t.Parallel()

			cookies := cookiesResponse{
				Cookies: map[string]string{
					"k1": "v1",
					"k2": "v2",
				},
			}
			params := url.Values{}
			for k, v := range cookies.Cookies {
				params.Set(k, v)
			}

			req := newTestRequest(t, "GET", app.URL(prefix+"/cookies/set", params), nil)
			resp := mustDoRequest(t, app, req)

			assert.StatusCode(t, resp, http.StatusFound)
			assert.Header(t, resp, "Location", prefix+"/cookies")

			for _, c := range resp.Cookies() {
				v, ok := cookies.Cookies[c.Name]
				if !ok {
					t.Fatalf("got unexpected cookie %s=%s", c.Name, c.Value)
				}
				assert.Equal(t, v, c.Value, "value mismatch for cookie %q", c.Name)
			}
		})

		t.Run("delete"+prefix, func(t *testing.T) {
			t.Parallel()

			cookies := cookiesResponse{
				Cookies: map[string]string{
					"k1": "v1",
					"k2": "v2",
				},
			}

			toDelete := "k2"
			params := url.Values{}
			params.Set(toDelete, "")

			req := newTestRequest(t, "GET", app.URL(prefix+"/cookies/delete", params), nil)
			for k, v := range cookies.Cookies {
				req.AddCookie(&http.Cookie{
					Name:  k,
					Value: v,
				})
			}

			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusFound)
			assert.Header(t, resp, "Location", prefix+"/cookies")

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
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, app.URL("/basic-auth/user/pass"), nil)
				req.SetBasicAuth("user", "pass")

				resp := mustDoRequest(t, app, req)
				result := mustParseResponse[authResponse](t, resp)
				expectedResult := authResponse{
					Authenticated: true,
					Authorized:    true,
					User:          "user",
				}
				assert.DeepEqual(t, result, expectedResult, "expected authorized user")
			})
		}
	})

	t.Run("error/no auth", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, app.URL("/basic-auth/user/pass"), nil)
				resp := mustDoRequest(t, app, req)
				assert.StatusCode(t, resp, http.StatusUnauthorized)
				assert.ContentType(t, resp, jsonContentType)
				assert.Header(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

				result := must.Unmarshal[authResponse](t, resp.Body)
				expectedResult := authResponse{
					Authenticated: false,
					Authorized:    false,
					User:          "",
				}
				assert.DeepEqual(t, result, expectedResult, "expected unauthorized user")
			})
		}
	})

	t.Run("error/bad auth", func(t *testing.T) {
		for _, method := range []string{"GET", "POST", "PUT", "DELETE", "PATCH"} {
			t.Run(method, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, method, app.URL("/basic-auth/user/pass"), nil)
				req.SetBasicAuth("bad", "auth")

				resp := mustDoRequest(t, app, req)
				assert.StatusCode(t, resp, http.StatusUnauthorized)
				assert.ContentType(t, resp, jsonContentType)
				assert.Header(t, resp, "WWW-Authenticate", `Basic realm="Fake Realm"`)

				result := must.Unmarshal[authResponse](t, resp.Body)
				expectedResult := authResponse{
					Authenticated: false,
					Authorized:    false,
					User:          "bad",
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
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			req.SetBasicAuth("foo", "bar")
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.status)
		})
	}
}

func TestHiddenBasicAuth(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, "GET", app.URL("/hidden-basic-auth/user/pass"), nil)
		req.SetBasicAuth("user", "pass")

		resp := mustDoRequest(t, app, req)
		result := mustParseResponse[authResponse](t, resp)
		expectedResult := authResponse{
			Authenticated: true,
			Authorized:    true,
			User:          "user",
		}
		assert.DeepEqual(t, result, expectedResult, "expected authorized user")
	})

	t.Run("error/no auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", app.URL("/hidden-basic-auth/user/pass"), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusNotFound)
		assert.Header(t, resp, "WWW-Authenticate", "")
	})

	t.Run("error/bad auth", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "GET", app.URL("/hidden-basic-auth/user/pass"), nil)
		req.SetBasicAuth("bad", "auth")
		resp := mustDoRequest(t, app, req)
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
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			req.SetBasicAuth("foo", "bar")
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.status)
		})
	}
}

func TestDigestAuth(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

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
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
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

		req := newTestRequest(t, "GET", app.URL("/digest-auth/auth/user/pass/MD5"), nil)
		req.Header.Set("Authorization", authorization)

		resp := mustDoRequest(t, app, req)
		result := mustParseResponse[authResponse](t, resp)
		expectedResult := authResponse{
			Authenticated: true,
			Authorized:    true,
			User:          "user",
		}
		assert.DeepEqual(t, result, expectedResult, "expected authorized user")
	})
}

func TestGzip(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/gzip"), nil)
	req.Header.Set("Accept-Encoding", "none") // disable automagic gzip decompression in default http client

	resp := mustDoRequest(t, app, req)
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

	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/deflate"), nil)
	resp := mustDoRequest(t, app, req)

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

	app := setupTestApp(t)

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
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)

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
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestTrailers(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)

	testCases := []struct {
		url          string
		wantStatus   int
		wantTrailers http.Header
	}{
		{
			"/trailers",
			http.StatusOK,
			nil,
		},
		{
			"/trailers?test-trailer-1=v1&Test-Trailer-2=v2",
			http.StatusOK,
			// note that response headers are canonicalized
			http.Header{"Test-Trailer-1": {"v1"}, "Test-Trailer-2": {"v2"}},
		},
		{
			"/trailers?test-trailer-1&Authorization=Bearer",
			http.StatusBadRequest,
			nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL(tc.url), nil)
			resp := mustDoRequest(t, app, req)

			assert.StatusCode(t, resp, tc.wantStatus)
			if tc.wantStatus != http.StatusOK {
				return
			}

			// trailers only sent w/ chunked transfer encoding
			assert.DeepEqual(t, resp.TransferEncoding, []string{"chunked"}, "expected Transfer-Encoding: chunked")

			// must read entire body to get trailers
			body := must.ReadAll(t, resp.Body)

			// don't really care about the contents, as long as body can be
			// unmarshaled into the correct type
			must.Unmarshal[bodyResponse](t, strings.NewReader(body))

			assert.DeepEqual(t, resp.Trailer, tc.wantTrailers, "trailers mismatch")
		})
	}
}

func TestDelay(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)

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
		{"/delay/1", app.App.MaxDuration},
	}
	for _, test := range okTests {
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			elapsed := time.Since(start)

			_ = mustParseResponse[bodyResponse](t, resp)

			if elapsed < test.expectedDelay {
				t.Fatalf("expected delay of %s, got %s", test.expectedDelay, elapsed)
			}

			timings := decodeServerTimings(resp.Header.Get("Server-Timing"))
			assert.DeepEqual(t, timings, map[string]serverTiming{
				"initial_delay": {"initial_delay", test.expectedDelay, "initial delay"},
			}, "incorrect Server-Timing header value")
		})
	}

	t.Run("handle cancelation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", app.URL("/delay/1"), nil).WithContext(ctx)
		_, err := app.Client.Do(req)
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
		app := createApp()
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
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestDrip(t *testing.T) {
	t.Parallel()

	app := setupTestApp(t)

	okTests := []struct {
		params   url.Values
		duration time.Duration
		numbytes int
		code     int
	}{
		// there are useful defaults for all values
		{url.Values{}, 0, 10, http.StatusOK},

		// go-style durations are accepted
		{url.Values{"duration": {"5ms"}}, 5 * time.Millisecond, 10, http.StatusOK},
		{url.Values{"duration": {"0h"}}, 0, 10, http.StatusOK},
		{url.Values{"delay": {"5ms"}}, 5 * time.Millisecond, 10, http.StatusOK},
		{url.Values{"delay": {"0h"}}, 0, 10, http.StatusOK},

		// or floating point seconds
		{url.Values{"duration": {"0.25"}}, 250 * time.Millisecond, 10, http.StatusOK},
		{url.Values{"duration": {"0"}}, 0, 10, http.StatusOK},
		{url.Values{"duration": {"1"}}, 1 * time.Second, 10, http.StatusOK},
		{url.Values{"delay": {"0.25"}}, 250 * time.Millisecond, 10, http.StatusOK},
		{url.Values{"delay": {"0"}}, 0, 10, http.StatusOK},

		{url.Values{"numbytes": {"1"}}, 0, 1, http.StatusOK},
		{url.Values{"numbytes": {"101"}}, 0, 101, http.StatusOK},
		{url.Values{"numbytes": {fmt.Sprintf("%d", app.App.MaxBodySize)}}, 0, int(app.App.MaxBodySize), http.StatusOK},

		{url.Values{"code": {"404"}}, 0, 10, http.StatusNotFound},
		{url.Values{"code": {"599"}}, 0, 10, 599},
		{url.Values{"code": {"567"}}, 0, 10, 567},

		{url.Values{"duration": {"250ms"}, "delay": {"250ms"}}, 500 * time.Millisecond, 10, http.StatusOK},
		{url.Values{"duration": {"250ms"}, "delay": {"0.25s"}}, 500 * time.Millisecond, 10, http.StatusOK},
	}
	for _, test := range okTests {
		t.Run(fmt.Sprintf("ok/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			req := newTestRequest(t, "GET", app.URL("/drip", test.params), nil)
			resp := mustDoRequest(t, app, req)
			assert.BodySize(t, resp, test.numbytes) // must read body before measuring elapsed time
			elapsed := time.Since(start)

			assert.StatusCode(t, resp, test.code)
			assert.ContentType(t, resp, textContentType)
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

		req := newTestRequest(t, "GET", app.URL("/drip?code=100"), nil)
		reqBytes, err := httputil.DumpRequestOut(req, false)
		assert.NilError(t, err)

		conn, err := net.Dial("tcp", app.Srv.Listener.Addr().String())
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
		req := newTestRequest(t, "GET", app.URL(endpoint), nil)
		resp := mustDoRequest(t, app, req)

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
				assert.RoughlyEqual(t, gotPause, wantPauseBetweenWrites, 3*time.Millisecond)
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

		req := newTestRequest(t, "GET", app.URL("/drip?duration=500ms&delay=500ms"), nil).WithContext(ctx)
		if _, err := app.Client.Do(req); !os.IsTimeout(err) {
			t.Fatalf("expected timeout error, got %s", err)
		}
	})

	t.Run("handle cancelation during drip", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", app.URL("/drip?duration=900ms&delay=100ms"), nil).WithContext(ctx)
		resp := mustDoRequest(t, app, req)

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
		params url.Values
		code   int
	}{
		{url.Values{"duration": {"1m"}}, http.StatusBadRequest},
		{url.Values{"duration": {"-1ms"}}, http.StatusBadRequest},
		{url.Values{"duration": {"1001"}}, http.StatusBadRequest},
		{url.Values{"duration": {"-1"}}, http.StatusBadRequest},
		{url.Values{"duration": {"foo"}}, http.StatusBadRequest},

		{url.Values{"delay": {"1m"}}, http.StatusBadRequest},
		{url.Values{"delay": {"-1ms"}}, http.StatusBadRequest},
		{url.Values{"delay": {"1001"}}, http.StatusBadRequest},
		{url.Values{"delay": {"-1"}}, http.StatusBadRequest},
		{url.Values{"delay": {"foo"}}, http.StatusBadRequest},

		{url.Values{"numbytes": {"foo"}}, http.StatusBadRequest},
		{url.Values{"numbytes": {"0"}}, http.StatusBadRequest},
		{url.Values{"numbytes": {"-1"}}, http.StatusBadRequest},
		{url.Values{"numbytes": {"0xff"}}, http.StatusBadRequest},
		{url.Values{"numbytes": {fmt.Sprintf("%d", app.App.MaxBodySize+1)}}, http.StatusBadRequest},

		{url.Values{"code": {"foo"}}, http.StatusBadRequest},
		{url.Values{"code": {"-1"}}, http.StatusBadRequest},
		{url.Values{"code": {"25"}}, http.StatusBadRequest},
		{url.Values{"code": {"600"}}, http.StatusBadRequest},

		// request would take too long
		{url.Values{"duration": {"750ms"}, "delay": {"500ms"}}, http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run(fmt.Sprintf("bad/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/drip", test.params), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.code)
		})
	}

	t.Run("ensure HEAD request works with streaming responses", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, "HEAD", app.URL("/drip?duration=900ms&delay=100ms"), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodySize(t, resp, 0)
	})

	t.Run("Server-Timings header", func(t *testing.T) {
		t.Parallel()

		var (
			duration = 100 * time.Millisecond
			delay    = 50 * time.Millisecond
			numBytes = 10
		)

		url := fmt.Sprintf("/drip?duration=%s&delay=%s&numbytes=%d", duration, delay, numBytes)
		req := newTestRequest(t, "GET", app.URL(url), nil)
		resp := mustDoRequest(t, app, req)

		assert.StatusCode(t, resp, http.StatusOK)

		timings := decodeServerTimings(resp.Header.Get("Server-Timing"))

		// compute expected pause between writes to match server logic and
		// handle lossy floating point truncation in the serialized header
		// value
		computedPause := duration / time.Duration(numBytes-1)
		wantPause, _ := time.ParseDuration(fmt.Sprintf("%.2fms", computedPause.Seconds()*1e3))

		assert.DeepEqual(t, timings, map[string]serverTiming{
			"total_duration":  {"total_duration", delay + duration, "total request duration"},
			"initial_delay":   {"initial_delay", delay, "initial delay"},
			"pause_per_write": {"pause_per_write", wantPause, "computed pause between writes"},
			"write_duration":  {"write_duration", duration, "duration of writes after initial delay"},
		}, "incorrect Server-Timing header value")
	})
}

func TestRange(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_no_range", func(t *testing.T) {
		t.Parallel()

		wantBytes := app.App.MaxBodySize - 1
		url := fmt.Sprintf("/range/%d", wantBytes)
		req := newTestRequest(t, "GET", app.URL(url), nil)

		resp := mustDoRequest(t, app, req)
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
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=10-24")

		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range100")
		assert.Header(t, resp, "Accept-Ranges", "bytes")
		assert.Header(t, resp, "Content-Length", "15")
		assert.Header(t, resp, "Content-Range", "bytes 10-24/100")
		assert.Header(t, resp, "Content-Type", textContentType)
		assert.BodyEquals(t, resp, "klmnopqrstuvwxy")
	})

	t.Run("ok_range_first_16_bytes", func(t *testing.T) {
		t.Parallel()

		url := "/range/1000"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=0-15")

		resp := mustDoRequest(t, app, req)
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
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=20-")

		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range26")
		assert.Header(t, resp, "Content-Length", "6")
		assert.Header(t, resp, "Content-Range", "bytes 20-25/26")
		assert.BodyEquals(t, resp, "uvwxyz")
	})

	t.Run("ok_range_suffix", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=-5")

		resp := mustDoRequest(t, app, req)
		t.Logf("headers = %v", resp.Header)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range26")
		assert.Header(t, resp, "Content-Length", "5")
		assert.Header(t, resp, "Content-Range", "bytes 21-25/26")
		assert.BodyEquals(t, resp, "vwxyz")
	})

	t.Run("ok_range_with_duration", func(t *testing.T) {
		t.Parallel()

		url := "/range/100?duration=100ms"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=10-24")

		start := time.Now()
		resp := mustDoRequest(t, app, req)
		elapsed := time.Since(start)

		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range100")
		assert.Header(t, resp, "Accept-Ranges", "bytes")
		assert.Header(t, resp, "Content-Length", "15")
		assert.Header(t, resp, "Content-Range", "bytes 10-24/100")
		assert.Header(t, resp, "Content-Type", textContentType)
		assert.BodyEquals(t, resp, "klmnopqrstuvwxy")
		assert.DurationRange(t, elapsed, 100*time.Millisecond, 150*time.Millisecond)
	})

	t.Run("ok_multiple_ranges", func(t *testing.T) {
		t.Parallel()

		url := "/range/100"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=10-24, 50-64")

		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusPartialContent)
		assert.Header(t, resp, "ETag", "range100")
		assert.Header(t, resp, "Accept-Ranges", "bytes")

		mediatype, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		assert.NilError(t, err)
		assert.Equal(t, mediatype, "multipart/byteranges", "incorrect content type")

		expectRanges := []struct {
			contentRange string
			body         string
		}{
			{"bytes 10-24/100", "klmnopqrstuvwxy"},
			{"bytes 50-64/100", "yzabcdefghijklm"},
		}
		mpr := multipart.NewReader(resp.Body, params["boundary"])
		for i := 0; ; i++ {
			p, err := mpr.NextPart()
			if err == io.EOF {
				break
			}
			assert.NilError(t, err)

			ct := p.Header.Get("Content-Type")
			assert.Equal(t, ct, textContentType, "incorrect content type")

			cr := p.Header.Get("Content-Range")
			assert.Equal(t, cr, expectRanges[i].contentRange, "incorrect Content-Range header")

			part := must.ReadAll(t, p)
			assert.Equal(t, string(part), expectRanges[i].body, "incorrect range part")
		}
	})

	t.Run("err_range_out_of_bounds", func(t *testing.T) {
		t.Parallel()

		url := "/range/26"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Add("Range", "bytes=-5")

		resp := mustDoRequest(t, app, req)
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
		t.Run(fmt.Sprintf("ok_bad_range_header/%s", test.rangeHeader), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusOK)
			assert.BodyEquals(t, resp, "abcdefghijklmnopqrstuvwxyz")
		})
	}

	badTests := []struct {
		url  string
		code int
	}{
		{"/range/1/foo", http.StatusNotFound},

		{"/range/", http.StatusNotFound},
		{"/range/foo", http.StatusBadRequest},
		{"/range/1.5", http.StatusBadRequest},
		{"/range/-1", http.StatusBadRequest},

		// invalid durations
		{"/range/100?duration=-1", http.StatusBadRequest},
		{"/range/100?duration=XYZ", http.StatusBadRequest},
		{"/range/100?duration=2h", http.StatusBadRequest},
	}

	for _, test := range badTests {
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestHTML(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/html"), nil)
	resp := mustDoRequest(t, app, req)
	assert.ContentType(t, resp, htmlContentType)
	assert.BodyContains(t, resp, `<h1>Herman Melville - Moby-Dick</h1>`)
}

func TestRobots(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/robots.txt"), nil)
	resp := mustDoRequest(t, app, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyContains(t, resp, `Disallow: /deny`)
}

func TestDeny(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/deny"), nil)
	resp := mustDoRequest(t, app, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyContains(t, resp, `YOU SHOULDN'T BE HERE`)
}

func TestCache(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_no_cache", func(t *testing.T) {
		t.Parallel()

		url := "/cache"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		resp := mustDoRequest(t, app, req)

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
		t.Run(fmt.Sprintf("ok_cache/%s", test.headerKey), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/cache"), nil)
			req.Header.Add(test.headerKey, test.headerVal)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusNotModified)
		})
	}
}

func TestCacheControl(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_cache_control", func(t *testing.T) {
		t.Parallel()

		url := "/cache/60"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		resp := mustDoRequest(t, app, req)
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
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestETag(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_no_headers", func(t *testing.T) {
		t.Parallel()

		url := "/etag/abc"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		resp := mustDoRequest(t, app, req)
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
		t.Run("ok_"+test.name, func(t *testing.T) {
			t.Parallel()
			url := "/etag/" + test.etag
			req := newTestRequest(t, "GET", app.URL(url), nil)
			req.Header.Add(test.headerKey, test.headerVal)
			resp := mustDoRequest(t, app, req)
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
		t.Run(fmt.Sprintf("bad/%s", test.url), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestBytes(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	t.Run("ok_no_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/1024"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.ContentType(t, resp, binaryContentType)
		assert.BodySize(t, resp, 1024)
	})

	t.Run("ok_seed", func(t *testing.T) {
		t.Parallel()

		url := "/bytes/16?seed=1234567890"
		req := newTestRequest(t, "GET", app.URL(url), nil)

		resp := mustDoRequest(t, app, req)
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

		// negative seed allowed
		{"/bytes/16?seed=-12345", 16},
	}
	for _, test := range edgeCaseTests {
		t.Run("edge"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)

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
		{"/bytes/99999999", http.StatusBadRequest}, // exceeds MaxBodySize

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
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestStreamBytes(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

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
		t.Run("ok"+test.url, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)

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
		t.Run("bad"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.code)
		})
	}
}

func TestLinks(t *testing.T) {
	for _, prefix := range []string{"", "/test-prefix"} {
		app := setupTestApp(t, WithPrefix(prefix))

		redirectTests := []struct {
			url              string
			expectedLocation string
		}{
			{"/links/1", "/links/1/0"},
			{"/links/100", "/links/100/0"},
		}

		for _, test := range redirectTests {
			t.Run("ok"+prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", app.URL(prefix+test.url), nil)
				resp := mustDoRequest(t, app, req)
				assert.StatusCode(t, resp, http.StatusFound)
				assert.Header(t, resp, "Location", prefix+test.expectedLocation)
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
			t.Run("error"+prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", app.URL(prefix+test.url), nil)
				resp := mustDoRequest(t, app, req)
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
			t.Run("ok"+prefix+test.url, func(t *testing.T) {
				t.Parallel()
				req := newTestRequest(t, "GET", app.URL(prefix+test.url), nil)
				resp := mustDoRequest(t, app, req)
				assert.StatusCode(t, resp, http.StatusOK)
				assert.ContentType(t, resp, htmlContentType)
				expectedContent := fmt.Sprintf(test.expectedContent, prefix)
				assert.BodyEquals(t, resp, expectedContent)
			})
		}
	}
}

func TestImage(t *testing.T) {
	t.Parallel()
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

	app := setupTestApp(t)

	for _, test := range acceptTests {
		t.Run("ok/accept="+test.acceptHeader, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/image"), nil)
			req.Header.Set("Accept", test.acceptHeader)
			resp := mustDoRequest(t, app, req)
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
		t.Run("error"+test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatus)
		})
	}
}

func TestXML(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/xml"), nil)
	resp := mustDoRequest(t, app, req)
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
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/uuid"), nil)
	resp := mustDoRequest(t, app, req)
	result := mustParseResponse[uuidResponse](t, resp)
	testValidUUIDv4(t, result.UUID)
}

func TestBase64(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	okTests := []struct {
		requestURL      string
		want            string
		wantContentType string
	}{
		{
			"/base64/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"valid_base64_encoded_string",
			textContentType,
		},
		{
			"/base64/decode/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			"valid_base64_encoded_string",
			textContentType,
		},
		{
			"/base64/encode/valid_base64_encoded_string",
			"dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			textContentType,
		},
		{
			// make sure we correctly handle padding
			// https://github.com/mccutchen/go-httpbin/issues/118
			"/base64/dGVzdC1pbWFnZQ==",
			"test-image",
			textContentType,
		},
		{
			// URL-safe base64 is used for decoding (note the - instead of + in
			// encoded input string)
			"/base64/decode/YWJjMTIzIT8kKiYoKSctPUB-",
			"abc123!?$*&()'-=@~",
			textContentType,
		},
		{
			// Std base64 is also supported for decoding (+ instead of - in
			// encoded input string). See also:
			// https://github.com/mccutchen/go-httpbin/issues/152
			"/base64/decode/8J+Ziywg8J+MjSEK4oCm",
			"🙋, 🌍!\n…",
			textContentType,
		},
		{
			// URL-safe base64 is used for encoding (note the - instead of + in
			// encoded output string)
			"/base64/encode/abc123%21%3F%24%2A%26%28%29%27-%3D%40~",
			"YWJjMTIzIT8kKiYoKSctPUB-",
			textContentType,
		},
		{
			// Custom content type
			"/base64/eyJzZXJ2ZXIiOiAiZ28taHR0cGJpbiJ9Cg==?content-type=application/json",
			`{"server": "go-httpbin"}` + "\n",
			"application/json",
		},
		{
			// XSS prevention w/ dangerous content type
			"/base64/PGltZy9zcmMvb25lcnJvcj1hbGVydCgneHNzJyk+?content-type=text/html",
			html.EscapeString("<img/src/onerror=alert('xss')>"),
			"text/html",
		},
	}

	for _, test := range okTests {
		t.Run("ok"+test.requestURL, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.requestURL), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusOK)
			assert.ContentType(t, resp, test.wantContentType)
			assert.BodyEquals(t, resp, test.want)
		})
	}

	errorTests := []struct {
		requestURL           string
		expectedStatusCode   int
		expectedBodyContains string
	}{
		{
			"/base64/invalid_base64_encoded_string",
			http.StatusBadRequest,
			"decode failed",
		},
		{
			"/base64/decode/invalid_base64_encoded_string",
			http.StatusBadRequest,
			"decode failed",
		},
		{
			"/base64/decode/invalid_base64_encoded_string",
			http.StatusBadRequest,
			"decode failed",
		},
		{
			"/base64/decode/" + strings.Repeat("X", int(app.App.MaxBodySize)+1),
			http.StatusBadRequest,
			"input data exceeds max length",
		},
		{
			"/base64/unknown/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n",
			http.StatusBadRequest,
			"invalid operation: unknown",
		},
		{
			"/base64/",
			http.StatusNotFound,
			"not found",
		},
		{
			"/base64/decode/",
			http.StatusNotFound,
			"not found",
		},
		{
			"/base64/decode/dmFsaWRfYmFzZTY0X2VuY29kZWRfc3RyaW5n/extra",
			http.StatusNotFound,
			"not found",
		},
	}

	for _, test := range errorTests {
		t.Run("error"+test.requestURL, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.requestURL), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, test.expectedStatusCode)
			assert.BodyContains(t, resp, test.expectedBodyContains)
		})
	}
}

func TestDumpRequest(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	req := newTestRequest(t, "GET", app.URL("/dump/request?foo=bar"), nil)
	req.Host = "test-host"
	req.Header.Set("x-test-header2", "Test-Value2")
	req.Header.Set("x-test-header1", "Test-Value1")

	resp := mustDoRequest(t, app, req)
	assert.ContentType(t, resp, textContentType)
	assert.BodyEquals(t, resp, "GET /dump/request?foo=bar HTTP/1.1\r\nHost: test-host\r\nAccept-Encoding: gzip\r\nUser-Agent: Go-http-client/1.1\r\nX-Test-Header1: Test-Value1\r\nX-Test-Header2: Test-Value2\r\n\r\n")
}

func TestJSON(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	req := newTestRequest(t, "GET", app.URL("/json"), nil)
	resp := mustDoRequest(t, app, req)
	assert.ContentType(t, resp, jsonContentType)
	assert.BodyContains(t, resp, `Wake up to WonderWidgets!`)
}

func TestBearer(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)
	url := "/bearer"

	t.Run("valid_token", func(t *testing.T) {
		t.Parallel()

		token := "valid_token"
		req := newTestRequest(t, "GET", app.URL(url), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		resp := mustDoRequest(t, app, req)
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
		t.Run("error"+test.authorizationHeader, func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL(url), nil)
			if test.authorizationHeader != "" {
				req.Header.Set("Authorization", test.authorizationHeader)
			}
			resp := mustDoRequest(t, app, req)
			assert.Header(t, resp, "WWW-Authenticate", "Bearer")
			assert.StatusCode(t, resp, http.StatusUnauthorized)
		})
	}
}

func TestNotImplemented(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

	tests := []struct {
		url string
	}{
		{"/brotli"},
	}
	for _, test := range tests {
		t.Run(test.url, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL(test.url), nil)
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, http.StatusNotImplemented)
		})
	}
}

func TestHostname(t *testing.T) {
	t.Run("default hostname", func(t *testing.T) {
		t.Parallel()
		app := setupTestApp(t)
		req := newTestRequest(t, "GET", app.URL("/hostname"), nil)
		resp := mustDoRequest(t, app, req)
		result := mustParseResponse[hostnameResponse](t, resp)
		assert.Equal(t, result.Hostname, DefaultHostname, "hostname mismatch")
	})

	t.Run("real hostname", func(t *testing.T) {
		t.Parallel()

		realHostname := "real-hostname"
		app := setupTestApp(t, WithHostname(realHostname))

		req := newTestRequest(t, "GET", app.URL("/hostname"), nil)
		resp := mustDoRequest(t, app, req)

		result := mustParseResponse[hostnameResponse](t, resp)
		assert.Equal(t, result.Hostname, realHostname, "hostname mismatch")
	})
}

func TestSSE(t *testing.T) {
	t.Parallel()
	app := setupTestApp(t)

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
		params   url.Values
		duration time.Duration
		count    int
	}{
		// there are useful defaults for all values
		{url.Values{}, 0, 10},

		// go-style durations are accepted
		{url.Values{"duration": {"5ms"}}, 5 * time.Millisecond, 10},
		{url.Values{"duration": {"10ns"}}, 0, 10},
		{url.Values{"delay": {"5ms"}}, 5 * time.Millisecond, 10},
		{url.Values{"delay": {"0h"}}, 0, 10},

		// or floating point seconds
		{url.Values{"duration": {"0.25"}}, 250 * time.Millisecond, 10},
		{url.Values{"duration": {"1"}}, 1 * time.Second, 10},
		{url.Values{"delay": {"0.25"}}, 250 * time.Millisecond, 10},
		{url.Values{"delay": {"0"}}, 0, 10},

		{url.Values{"count": {"1"}}, 0, 1},
		{url.Values{"count": {"011"}}, 0, 11},
		{url.Values{"count": {fmt.Sprintf("%d", app.App.maxSSECount)}}, 0, int(app.App.maxSSECount)},

		{url.Values{"duration": {"250ms"}, "delay": {"250ms"}}, 500 * time.Millisecond, 10},
		{url.Values{"duration": {"250ms"}, "delay": {"0.25s"}}, 500 * time.Millisecond, 10},
	}
	for _, test := range okTests {
		t.Run(fmt.Sprintf("ok/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()

			req := newTestRequest(t, "GET", app.URL("/sse", test.params), nil)
			start := time.Now()
			resp := mustDoRequest(t, app, req)
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
		params url.Values
		code   int
	}{
		{url.Values{"duration": {"0"}}, http.StatusBadRequest},
		{url.Values{"duration": {"0s"}}, http.StatusBadRequest},
		{url.Values{"duration": {"1m"}}, http.StatusBadRequest},
		{url.Values{"duration": {"-1ms"}}, http.StatusBadRequest},
		{url.Values{"duration": {"1001"}}, http.StatusBadRequest},
		{url.Values{"duration": {"-1"}}, http.StatusBadRequest},
		{url.Values{"duration": {"foo"}}, http.StatusBadRequest},

		{url.Values{"delay": {"1m"}}, http.StatusBadRequest},
		{url.Values{"delay": {"-1ms"}}, http.StatusBadRequest},
		{url.Values{"delay": {"1001"}}, http.StatusBadRequest},
		{url.Values{"delay": {"-1"}}, http.StatusBadRequest},
		{url.Values{"delay": {"foo"}}, http.StatusBadRequest},

		{url.Values{"count": {"foo"}}, http.StatusBadRequest},
		{url.Values{"count": {"0"}}, http.StatusBadRequest},
		{url.Values{"count": {"-1"}}, http.StatusBadRequest},
		{url.Values{"count": {"0xff"}}, http.StatusBadRequest},
		{url.Values{"count": {fmt.Sprintf("%d", app.App.maxSSECount+1)}}, http.StatusBadRequest},

		// request would take too long
		{url.Values{"duration": {"750ms"}, "delay": {"500ms"}}, http.StatusBadRequest},
	}
	for _, test := range badTests {
		t.Run(fmt.Sprintf("bad/%s", test.params.Encode()), func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, "GET", app.URL("/sse", test.params), nil)
			resp := mustDoRequest(t, app, req)
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

		req := newTestRequest(t, "GET", app.URL(endpoint), nil)
		resp := mustDoRequest(t, app, req)
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
				assert.RoughlyEqual(t, gotPause, wantPauseBetweenWrites, 3*time.Millisecond)
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

		req := newTestRequest(t, "GET", app.URL("/sse?duration=500ms&delay=500ms"), nil).WithContext(ctx)
		if _, err := app.Client.Do(req); !os.IsTimeout(err) {
			t.Fatalf("expected timeout error, got %s", err)
		}
	})

	t.Run("handle cancelation during stream", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		req := newTestRequest(t, "GET", app.URL("/sse?duration=900ms&delay=0&count=2"), nil).WithContext(ctx)
		resp := mustDoRequest(t, app, req)

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
		req := newTestRequest(t, "HEAD", app.URL("/sse?duration=900ms&delay=100ms"), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusOK)
		assert.BodySize(t, resp, 0)
	})

	t.Run("Server-Timings trailers", func(t *testing.T) {
		t.Parallel()

		var (
			duration = 250 * time.Millisecond
			delay    = 100 * time.Millisecond
			count    = 10
			params   = url.Values{
				"duration": {duration.String()},
				"delay":    {delay.String()},
				"count":    {strconv.Itoa(count)},
			}
		)

		req := newTestRequest(t, "GET", app.URL("/sse", params), nil)
		resp := mustDoRequest(t, app, req)

		// need to fully consume body for Server-Timing trailers to arrive
		must.ReadAll(t, resp.Body)

		rawTimings := resp.Trailer.Get("Server-Timing")
		t.Logf("raw Server-Timing header value: %q", rawTimings)

		timings := decodeServerTimings(rawTimings)

		// Ensure total server time makes sense based on duration and delay
		total := timings["total_duration"]
		assert.DurationRange(t, total.dur, duration+delay, duration+delay+25*time.Millisecond)

		// Ensure computed pause time makes sense based on duration, delay, and
		// numbytes (should be exact, but we're re-parsing a truncated float in
		// the header value)
		pause := timings["pause_per_write"]
		assert.RoughlyEqual(t, pause.dur, duration/time.Duration(count-1), 1*time.Millisecond)

		// remaining timings should exactly match request parameters, no need
		// to adjust for per-run variations
		wantTimings := map[string]serverTiming{
			"write_duration": {"write_duration", duration, "duration of writes after initial delay"},
			"initial_delay":  {"initial_delay", delay, "initial delay"},
		}
		for k, want := range wantTimings {
			got := timings[k]
			assert.DeepEqual(t, got, want, "incorrect timing for key %q", k)
		}
	})
}

func TestUpload(t *testing.T) {
	app := setupTestApp(t)
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
	for _, verb := range []string{"POST", "PUT", "PATCH"} {
		for _, test := range tests {
			t.Run("content type/"+test.contentType, func(t *testing.T) {
				t.Parallel()

				req := newTestRequest(t, verb, app.URL("/upload"), bytes.NewReader([]byte(test.requestBody)))
				req.Header.Set("Content-Type", test.contentType)
				resp := mustDoRequest(t, app, req)

				result := mustParseResponse[discardedBodyResponse](t, resp)
				assert.Equal(t, result.Method, verb, "method mismatch")
				assert.DeepEqual(t, result.BytesReceived, int64(len(test.requestBody)), "BytesReceived should match requestedBody size")
			})
		}

	}
}

func TestWebSocketEcho(t *testing.T) {
	// ========================================================================
	// Note: Here we only test input validation for the websocket endpoint.
	//
	// See websocket/*_test.go for in-depth integration tests of the actual
	// websocket implementation.
	// ========================================================================

	t.Parallel()
	app := setupTestApp(t)

	handshakeHeaders := map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
		"Sec-WebSocket-Version": "13",
	}

	t.Run("handshake ok", func(t *testing.T) {
		t.Parallel()

		req := newTestRequest(t, http.MethodGet, app.URL("/websocket/echo"), nil)
		for k, v := range handshakeHeaders {
			req.Header.Set(k, v)
		}

		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusSwitchingProtocols)
	})

	t.Run("handshake failed", func(t *testing.T) {
		t.Parallel()
		req := newTestRequest(t, http.MethodGet, app.URL("/websocket/echo"), nil)
		resp := mustDoRequest(t, app, req)
		assert.StatusCode(t, resp, http.StatusBadRequest)
	})

	paramTests := []struct {
		query      string
		wantStatus int
	}{
		// ok
		{"max_fragment_size=1&max_message_size=2", http.StatusSwitchingProtocols},
		{fmt.Sprintf("max_fragment_size=%d&max_message_size=%d", app.App.MaxBodySize, app.App.MaxBodySize), http.StatusSwitchingProtocols},

		// bad max_framgent_size
		{"max_fragment_size=-1&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=0&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=3&max_message_size=2", http.StatusBadRequest},
		{"max_fragment_size=foo&max_message_size=2", http.StatusBadRequest},
		{fmt.Sprintf("max_fragment_size=%d&max_message_size=2", app.App.MaxBodySize+1), http.StatusBadRequest},

		// bad max_message_size
		{"max_fragment_size=1&max_message_size=0", http.StatusBadRequest},
		{"max_fragment_size=1&max_message_size=-1", http.StatusBadRequest},
		{"max_fragment_size=1&max_message_size=bar", http.StatusBadRequest},
		{fmt.Sprintf("max_fragment_size=1&max_message_size=%d", app.App.MaxBodySize+1), http.StatusBadRequest},
	}
	for _, tc := range paramTests {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			req := newTestRequest(t, http.MethodGet, app.URL("/websocket/echo?"+tc.query), nil)
			for k, v := range handshakeHeaders {
				req.Header.Set(k, v)
			}
			resp := mustDoRequest(t, app, req)
			assert.StatusCode(t, resp, tc.wantStatus)
		})
	}
}

func newTestRequest(t *testing.T, verb, endpoint string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(verb, endpoint, body)
	assert.NilError(t, err)
	return req
}

func mustDoRequest(t *testing.T, app *appTestInfo, req *http.Request) *http.Response {
	t.Helper()
	resp, err := app.Client.Do(req)
	assert.NilError(t, err)
	t.Cleanup(func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	})
	return resp
}

func mustParseResponse[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	assert.StatusCode(t, resp, http.StatusOK)
	assert.ContentType(t, resp, jsonContentType)
	return must.Unmarshal[T](t, resp.Body)
}
