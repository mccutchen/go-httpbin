package httpbin

import (
	"fmt"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	var okTests = []struct {
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
		t.Run(fmt.Sprintf("ok/%s", test.input), func(t *testing.T) {
			result, err := parseDuration(test.input)
			if err != nil {
				t.Fatalf("unexpected error parsing duration %v: %s", test.input, err)
			}
			if result != test.expected {
				t.Fatalf("expected %s, got %s", test.expected, result)
			}
		})
	}

	var badTests = []struct {
		input string
	}{
		{"foo"},
		{"100foo"},
		{"1/1"},
		{"1.5.foo"},
		{"0xFF"},
	}
	for _, test := range badTests {
		t.Run(fmt.Sprintf("bad/%s", test.input), func(t *testing.T) {
			_, err := parseDuration(test.input)
			if err == nil {
				t.Fatalf("expected error parsing %v", test.input)
			}
		})
	}
}
