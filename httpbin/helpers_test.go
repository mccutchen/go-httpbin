package httpbin

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func assertNil(t *testing.T, v interface{}) {
	if v != nil {
		t.Errorf("expected nil, got %#v", v)
	}
}

func assertIntEqual(t *testing.T, a, b int) {
	if a != b {
		t.Errorf("expected %v == %v", a, b)
	}
}

func assertBytesEqual(t *testing.T, a, b []byte) {
	if !reflect.DeepEqual(a, b) {
		t.Errorf("expected %v == %v", a, b)
	}
}

func assertError(t *testing.T, got, expected error) {
	if got != expected {
		t.Errorf("expected error %v, got %v", expected, got)
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
			if err != nil {
				t.Fatalf("unexpected error parsing duration %v: %s", test.input, err)
			}
			if result != test.expected {
				t.Fatalf("expected %s, got %s", test.expected, result)
			}
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
		assertNil(t, err)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{0, 1, 2, 3, 4})

		// read second half
		p = make([]byte, 5)
		count, err = s.Read(p)
		assertError(t, err, io.EOF)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{5, 6, 7, 8, 9})

		// can't read any more
		p = make([]byte, 5)
		count, err = s.Read(p)
		assertError(t, err, io.EOF)
		assertIntEqual(t, count, 0)
		assertBytesEqual(t, p, []byte{0, 0, 0, 0, 0})
	})

	t.Run("read into too-large buffer", func(t *testing.T) {
		t.Parallel()
		s := newSyntheticByteStream(5, factory)
		p := make([]byte, 10)
		count, err := s.Read(p)
		assertError(t, err, io.EOF)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{0, 1, 2, 3, 4, 0, 0, 0, 0, 0})
	})

	t.Run("seek", func(t *testing.T) {
		t.Parallel()
		s := newSyntheticByteStream(100, factory)

		p := make([]byte, 5)
		s.Seek(10, io.SeekStart)
		count, err := s.Read(p)
		assertNil(t, err)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{10, 11, 12, 13, 14})

		s.Seek(10, io.SeekCurrent)
		count, err = s.Read(p)
		assertNil(t, err)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{25, 26, 27, 28, 29})

		s.Seek(10, io.SeekEnd)
		count, err = s.Read(p)
		assertNil(t, err)
		assertIntEqual(t, count, 5)
		assertBytesEqual(t, p, []byte{90, 91, 92, 93, 94})

		// invalid whence
		_, err = s.Seek(10, 666)
		if err.Error() != "Seek: invalid whence" {
			t.Errorf("Expected \"Seek: invalid whence\", got %#v", err.Error())
		}

		// invalid offset
		_, err = s.Seek(-10, io.SeekStart)
		if err.Error() != "Seek: invalid offset" {
			t.Errorf("Expected \"Seek: invalid offset\", got %#v", err.Error())
		}
	})
}

func Test_getClientIP(t *testing.T) {
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
			if got := getClientIP(tc.given); got != tc.want {
				t.Errorf("getClientIP() = %v, want %v", got, tc.want)
			}
		})
	}
}
