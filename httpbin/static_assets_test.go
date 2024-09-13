package httpbin

import "testing"

// Silly tests just to increase code coverage scores

func TestMustStaticAsset(t *testing.T) {
	defer func() {
		// recover from panic if one occured. Set err to nil otherwise.
		if err := recover(); err == nil {
			t.Fatalf("expected to recover from panic, got nil")
		}
	}()
	mustStaticAsset("xxxyyyzzz")
}

func TestMustRenderTemplate(t *testing.T) {
	t.Run("invalid template name", func(t *testing.T) {
		defer func() {
			// recover from panic if one occured. Set err to nil otherwise.
			if err := recover(); err == nil {
				t.Fatalf("expected to recover from panic, got nil")
			}
		}()
		mustRenderTemplate("xxxyyyzzz", nil)
	})

	t.Run("invalid template data", func(t *testing.T) {
		defer func() {
			// recover from panic if one occured. Set err to nil otherwise.
			if err := recover(); err == nil {
				t.Fatalf("expected to recover from panic, got nil")
			}
		}()
		mustRenderTemplate("index.html.tmpl", nil)
	})
}
