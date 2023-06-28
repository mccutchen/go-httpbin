package assert

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/must"
)

// Equal asserts that two values are equal.
func Equal[T comparable](t *testing.T, want, got T, msg string, arg ...any) {
	t.Helper()
	if want != got {
		if msg == "" {
			msg = "expected values to match"
		}
		msg = fmt.Sprintf(msg, arg...)
		t.Fatalf("%s:\nwant: %#v\n got: %#v", msg, want, got)
	}
}

// DeepEqual asserts that two values are deeply equal.
func DeepEqual[T any](t *testing.T, want, got T, msg string, arg ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		if msg == "" {
			msg = "expected values to match"
		}
		msg = fmt.Sprintf(msg, arg...)
		t.Fatalf("%s:\nwant: %#v\n got: %#v", msg, want, got)
	}
}

// NilError asserts that an error is nil.
func NilError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected nil error, got %s (%T)", err, err)
	}
}

// Error asserts that an error is not nil.
func Error(t *testing.T, got, expected error) {
	t.Helper()
	if got != expected {
		t.Fatalf("expected error %v, got %v", expected, got)
	}
}

// StatusCode asserts that a response has a specific status code.
func StatusCode(t *testing.T, resp *http.Response, code int) {
	t.Helper()
	if resp.StatusCode != code {
		t.Fatalf("expected status code %d, got %d", code, resp.StatusCode)
	}
}

// Header asserts that a header key has a specific value in a response.
func Header(t *testing.T, resp *http.Response, key, want string) {
	t.Helper()
	got := resp.Header.Get(key)
	if want != got {
		t.Fatalf("expected header %s=%#v, got %#v", key, want, got)
	}
}

// ContentType asserts that a response has a specific Content-Type header
// value.
func ContentType(t *testing.T, resp *http.Response, contentType string) {
	t.Helper()
	Header(t, resp, "Content-Type", contentType)
}

// BodyContains asserts that a response body contains a specific substring.
func BodyContains(t *testing.T, resp *http.Response, needle string) {
	t.Helper()
	body := must.ReadAll(t, resp.Body)
	if !strings.Contains(body, needle) {
		t.Fatalf("expected string %q in body %q", needle, body)
	}
}

// BodyEquals asserts that a response body is equal to a specific string.
func BodyEquals(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	got := must.ReadAll(t, resp.Body)
	Equal(t, got, want, "incorrect response body")
}
