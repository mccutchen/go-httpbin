package httpbin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
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
}

func TestNewOptions(t *testing.T) {
	maxDuration := 1 * time.Second
	maxBodySize := int64(1024)
	observer := func(_ Result) {}

	h := New(
		WithMaxBodySize(maxBodySize),
		WithMaxDuration(maxDuration),
		WithObserver(observer),
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
}

func TestNewObserver(t *testing.T) {
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
