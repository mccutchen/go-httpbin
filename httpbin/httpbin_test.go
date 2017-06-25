package httpbin

import (
	"testing"
	"time"
)

func TestNewHTTPBin__Defaults(t *testing.T) {
	h := NewHTTPBin()
	if h.options.MaxMemory != DefaultMaxMemory {
		t.Fatalf("expected default MaxMemory == %d, got %#v", DefaultMaxMemory, h.options.MaxMemory)
	}
	if h.options.MaxDuration != DefaultMaxDuration {
		t.Fatalf("expected default MaxDuration == %s, got %#v", DefaultMaxDuration, h.options.MaxDuration)
	}
}

func TestNewHTTPBinWithOptions__Defaults(t *testing.T) {
	o := &Options{
		MaxDuration: 1 * time.Second,
		MaxMemory:   1024,
	}
	h := NewHTTPBinWithOptions(o)
	if h.options.MaxMemory != o.MaxMemory {
		t.Fatalf("expected MaxMemory == %d, got %#v", o.MaxMemory, h.options.MaxMemory)
	}
	if h.options.MaxDuration != o.MaxDuration {
		t.Fatalf("expected MaxDuration == %s, got %#v", o.MaxDuration, h.options.MaxDuration)
	}
}
