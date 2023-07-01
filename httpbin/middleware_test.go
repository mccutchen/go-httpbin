package httpbin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func TestTestMode(t *testing.T) {
	// This test ensures that we use testMode in our test suite, and ensures
	// that it is working as expected.
	assert.Equal(t, testMode, true, "expected testMode to be turned on in test suite")

	// We want to ensure that, in testMode, a handler calling WriteHeader twice
	// will cause a panic. This happens most often when we forget to return
	// early after writing an error response, and has helped identify and fix
	// some subtly broken error handling.
	observer := func(r Result) {}
	handler := observe(observer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.WriteHeader(http.StatusOK)
	}))

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected to catch panic")
		}
		err, ok := r.(error)
		assert.Equal(t, ok, true, "expected panic to be an error")
		assert.Equal(t, err.Error(), "HTTP status already set to 400, cannot set to 200", "incorrectp panic error message")
	}()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(w, r)
}
