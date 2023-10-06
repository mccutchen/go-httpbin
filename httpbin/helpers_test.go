package httpbin

import (
	"crypto/tls"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func mustParse(s string) *url.URL {
	u, e := url.Parse(s)
	if e != nil {
		panic(e)
	}
	return u
}

func TestGetURL(t *testing.T) {
	baseURL := mustParse("http://example.com/something?foo=bar")
	tests := []struct {
		name     string
		input    *http.Request
		expected *url.URL
	}{
		{
			"basic test",
			&http.Request{
				URL:    baseURL,
				Header: http.Header{},
			},
			mustParse("http://example.com/something?foo=bar"),
		},
		{
			"if TLS is not nil, scheme is https",
			&http.Request{
				URL:    baseURL,
				TLS:    &tls.ConnectionState{},
				Header: http.Header{},
			},
			mustParse("https://example.com/something?foo=bar"),
		},
		{
			"if X-Forwarded-Proto is present, scheme is that value",
			&http.Request{
				URL:    baseURL,
				Header: http.Header{"X-Forwarded-Proto": {"https"}},
			},
			mustParse("https://example.com/something?foo=bar"),
		},
		{
			"if X-Forwarded-Proto is present, scheme is that value (2)",
			&http.Request{
				URL:    baseURL,
				Header: http.Header{"X-Forwarded-Proto": {"bananas"}},
			},
			mustParse("bananas://example.com/something?foo=bar"),
		},
		{
			"if X-Forwarded-Ssl is 'on', scheme is https",
			&http.Request{
				URL:    baseURL,
				Header: http.Header{"X-Forwarded-Ssl": {"on"}},
			},
			mustParse("https://example.com/something?foo=bar"),
		},
		{
			"if request URL host is empty, host is request.host",
			&http.Request{
				URL:  mustParse("http:///just/a/path"),
				Host: "zombo.com",
			},
			mustParse("http://zombo.com/just/a/path"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			res := getURL(test.input)
			assert.Equal(t, res.String(), test.expected.String(), "URL mismatch")
		})
	}
}

func TestParseDuration(t *testing.T) {
	okTests := []struct {
		input    string
		expected time.Duration
	}{
		// go-style durations
		{"1s", time.Second},
		{"500ms", 500 * time.Millisecond},
		{"1.5h", 90 * time.Minute},
		{"-10m", -10 * time.Minute},

		// or floating point seconds
		{"1", time.Second},
		{"0.25", 250 * time.Millisecond},
		{"-25", -25 * time.Second},
		{"-2.5", -2500 * time.Millisecond},
	}
	for _, test := range okTests {
		test := test
		t.Run(fmt.Sprintf("ok/%s", test.input), func(t *testing.T) {
			t.Parallel()
			result, err := parseDuration(test.input)
			assert.NilError(t, err)
			assert.Equal(t, result, test.expected, "incorrect duration")
		})
	}

	badTests := []struct {
		input string
	}{
		{"foo"},
		{"100foo"},
		{"1/1"},
		{"1.5.foo"},
		{"0xFF"},
	}
	for _, test := range badTests {
		test := test
		t.Run(fmt.Sprintf("bad/%s", test.input), func(t *testing.T) {
			t.Parallel()
			_, err := parseDuration(test.input)
			if err == nil {
				t.Fatalf("expected error parsing %v", test.input)
			}
		})
	}
}

func TestSyntheticByteStream(t *testing.T) {
	t.Parallel()
	factory := func(offset int64) byte {
		return byte(offset)
	}

	t.Run("read", func(t *testing.T) {
		t.Parallel()
		s := newSyntheticByteStream(10, factory)

		// read first half
		p := make([]byte, 5)
		count, err := s.Read(p)
		assert.NilError(t, err)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{0, 1, 2, 3, 4}, "incorrect bytes read")

		// read second half
		p = make([]byte, 5)
		count, err = s.Read(p)
		assert.Error(t, err, io.EOF)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{5, 6, 7, 8, 9}, "incorrect bytes read")

		// can't read any more
		p = make([]byte, 5)
		count, err = s.Read(p)
		assert.Error(t, err, io.EOF)
		assert.Equal(t, count, 0, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{0, 0, 0, 0, 0}, "incorrect bytes read")
	})

	t.Run("read into too-large buffer", func(t *testing.T) {
		t.Parallel()
		s := newSyntheticByteStream(5, factory)
		p := make([]byte, 10)
		count, err := s.Read(p)
		assert.Error(t, err, io.EOF)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{0, 1, 2, 3, 4, 0, 0, 0, 0, 0}, "incorrect bytes read")
	})

	t.Run("seek", func(t *testing.T) {
		t.Parallel()
		s := newSyntheticByteStream(100, factory)

		p := make([]byte, 5)
		s.Seek(10, io.SeekStart)
		count, err := s.Read(p)
		assert.NilError(t, err)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{10, 11, 12, 13, 14}, "incorrect bytes read")

		s.Seek(10, io.SeekCurrent)
		count, err = s.Read(p)
		assert.NilError(t, err)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{25, 26, 27, 28, 29}, "incorrect bytes read")

		s.Seek(10, io.SeekEnd)
		count, err = s.Read(p)
		assert.NilError(t, err)
		assert.Equal(t, count, 5, "incorrect number of bytes read")
		assert.DeepEqual(t, p, []byte{90, 91, 92, 93, 94}, "incorrect bytes read")

		_, err = s.Seek(10, 666)
		assert.Equal(t, err.Error(), "Seek: invalid whence", "incorrect error for invalid whence")

		_, err = s.Seek(-10, io.SeekStart)
		assert.Equal(t, err.Error(), "Seek: invalid offset", "incorrect error for invalid offset")
	})
}

func TestGetClientIP(t *testing.T) {
	t.Parallel()

	makeHeaders := func(m map[string]string) http.Header {
		h := make(http.Header, len(m))
		for k, v := range m {
			h.Set(k, v)
		}
		return h
	}

	testCases := map[string]struct {
		given *http.Request
		want  string
	}{
		"custom platform headers take precedence": {
			given: &http.Request{
				Header: makeHeaders(map[string]string{
					"Fly-Client-IP":   "9.9.9.9",
					"X-Forwarded-For": "1.1.1.1,2.2.2.2,3.3.3.3",
				}),
				RemoteAddr: "0.0.0.0",
			},
			want: "9.9.9.9",
		},
		"custom cloudflare header take precedence": {
			given: &http.Request{
				Header: makeHeaders(map[string]string{
					"CF-Connecting-IP": "9.9.9.9",
					"X-Forwarded-For":  "1.1.1.1,2.2.2.2,3.3.3.3",
				}),
				RemoteAddr: "0.0.0.0",
			},
			want: "9.9.9.9",
		},
		"x-forwarded-for is parsed": {
			given: &http.Request{
				Header: makeHeaders(map[string]string{
					"X-Forwarded-For": "1.1.1.1,2.2.2.2,3.3.3.3",
				}),
				RemoteAddr: "0.0.0.0",
			},
			want: "1.1.1.1",
		},
		"remoteaddr is fallback": {
			given: &http.Request{
				RemoteAddr: "0.0.0.0",
			},
			want: "0.0.0.0",
		},
	}
	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, getClientIP(tc.given), tc.want, "incorrect client ip")
		})
	}
}

func TestParseFileDoesntExist(t *testing.T) {
	// set up a headers map where the filename doesn't exist, to test `f.Open`
	// throwing an error
	headers := map[string][]*multipart.FileHeader{
		"fieldname": {
			{
				Filename: "bananas",
			},
		},
	}

	// expect a patherror
	_, err := parseFiles(headers)
	if _, ok := err.(*fs.PathError); !ok {
		t.Fatalf("Open(nonexist): error is %T, want *PathError", err)
	}
}

func TestWildcardHelpers(t *testing.T) {
	tests := []struct {
		pattern  string
		name     string
		input    string
		expected bool
	}{
		{
			"info-*",
			"basic test",
			"info-foo",
			true,
		},
		{
			"info-*",
			"basic test case insensitive",
			"INFO-bar",
			true,
		},
		{
			"info-*-foo",
			"a single wildcard in the middle of the string",
			"INFO-bar-foo",
			true,
		},
		{
			"info-*-foo",
			"a single wildcard in the middle of the string",
			"INFO-bar-baz",
			false,
		},
		{
			"info-*-foo-*-bar",
			"multiple wildcards in the string",
			"info-aaa-foo--bar",
			true,
		},
		{
			"info-*-foo-*-bar",
			"multiple wildcards in the string",
			"info-aaa-foo-a-bar",
			true,
		},
		{
			"info-*-foo-*-bar",
			"multiple wildcards in the string",
			"info-aaa-foo--bar123",
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpRegexStr := wildCardToRegexp(test.pattern)
			regex := regexp.MustCompile("(?i)" + "(" + tmpRegexStr + ")")
			matched := regex.Match([]byte(test.input))
			assert.Equal(t, matched, test.expected, "incorrect match")
		})
	}
}

func TestCreateFullExcludeRegex(t *testing.T) {
	// tolerate unused comma
	excludeHeaders := "x-ignore-*,x-info-this-key,,"
	regex := createFullExcludeRegex(excludeHeaders)
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			"basic test",
			"x-ignore-foo",
			true,
		},
		{
			"basic test case insensitive",
			"X-IGNORE-bar",
			true,
		},
		{
			"basic test 3",
			"x-info-this-key",
			true,
		},
		{
			"basic test 4",
			"foo-bar",
			false,
		},
		{
			"basic test 5",
			"x-info-this-key-foo",
			false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			matched := regex.Match([]byte(test.input))
			assert.Equal(t, matched, test.expected, "incorrect match")
		})
	}

	nilReturn := createFullExcludeRegex("")
	assert.Equal(t, nilReturn, nil, "incorrect match")
}
