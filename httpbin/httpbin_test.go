package httpbin

import (
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
}

func TestNewOptions(t *testing.T) {
	maxDuration := 1 * time.Second
	maxBodySize := int64(1024)

	h := New(
		WithMaxBodySize(maxBodySize),
		WithMaxDuration(maxDuration))

	if h.MaxBodySize != maxBodySize {
		t.Fatalf("expected MaxBodySize == %d, got %#v", maxBodySize, h.MaxBodySize)
	}
	if h.MaxDuration != maxDuration {
		t.Fatalf("expected MaxDuration == %s, got %#v", maxDuration, h.MaxDuration)
	}
}
