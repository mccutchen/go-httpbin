package httpbin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func TestNew(t *testing.T) {
	t.Parallel()
	h := New()
	if h.MaxBodySize != DefaultMaxBodySize {
		t.Fatalf("expected default MaxBodySize == %d, got %#v", DefaultMaxBodySize, h.MaxBodySize)
	}
	if h.MaxDuration != DefaultMaxDuration {
		t.Fatalf("expected default MaxDuration == %s, got %#v", DefaultMaxDuration, h.MaxDuration)
	}
	if h.Observer != nil {
		t.Fatalf("expected default Observer == nil, got %#v", h.Observer)
	}
	assert.DeepEqual(t, h.version, versionResponse{Service: "go-httpbin"}, "incorrect default versionResponse")

}

func TestNewOptions(t *testing.T) {
	t.Parallel()
	maxDuration := 1 * time.Second
	maxBodySize := int64(1024)
	observer := func(_ Result) {}

	h := New(
		WithMaxBodySize(maxBodySize),
		WithMaxDuration(maxDuration),
		WithObserver(observer),
		WithVersion("go-httpbin", "1.2.3", "abcd1234", "1988-11-12T10:00:00Z", "go2.0.0"),
	)

	if h.MaxBodySize != maxBodySize {
		t.Fatalf("expected MaxBodySize == %d, got %#v", maxBodySize, h.MaxBodySize)
	}
	if h.MaxDuration != maxDuration {
		t.Fatalf("expected MaxDuration == %s, got %#v", maxDuration, h.MaxDuration)
	}
	if h.Observer == nil {
		t.Fatalf("expected non-nil Observer")
	}
	assert.DeepEqual(t, h.version, versionResponse{
		Service:   "go-httpbin",
		Version:   "1.2.3",
		Commit:    "abcd1234",
		BuildDate: "1988-11-12T10:00:00Z",
		GoVersion: "go2.0.0",
	}, "incorrect versionResponse")
}

func TestNewObserver(t *testing.T) {
	t.Parallel()
	expectedStatus := http.StatusTeapot

	observed := false
	observer := func(r Result) {
		observed = true
		if r.Status != expectedStatus {
			t.Fatalf("expected result status = %d, got %d", expectedStatus, r.Status)
		}
	}

	h := New(WithObserver(observer))

	r, _ := http.NewRequest("GET", fmt.Sprintf("/status/%d", expectedStatus), nil)
	w := httptest.NewRecorder()
	h.Handler().ServeHTTP(w, r)

	if observed == false {
		t.Fatalf("observer never called")
	}
}
