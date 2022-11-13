package httpbin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
)

func TestSlowResponse(t *testing.T) {
	app := httpbin.New()
	testServer := httptest.NewServer(app)
	defer testServer.Close()

	client := http.Client{
		Timeout: time.Duration(1 * time.Second),
	}

	_, err := client.Get(testServer.URL + "/delay/10")
	if !os.IsTimeout(err) {
		t.Fatalf("expected timeout error, got %s", err)
	}
}
