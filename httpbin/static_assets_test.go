package httpbin

import "testing"

// Silly test just to satisfy coverage
func TestMustStaticAsset(t *testing.T) {
	defer func() {
		// recover from panic if one occured. Set err to nil otherwise.
		if err := recover(); err == nil {
			t.Fatalf("expected to recover from panic, got nil")
		}
	}()
	mustStaticAsset("xxxyyyzzz")
}
