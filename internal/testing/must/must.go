package must

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

func DoReq(t *testing.T, client *http.Client, req *http.Request) *http.Response {
	t.Helper()
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("error making HTTP request: %s %s: %s", req.Method, req.URL, err)
	}
	t.Logf("HTTP request: %s %s => %s (%s)", req.Method, req.URL, resp.Status, time.Since(start))
	return resp
}

func ReadAll(t *testing.T, r io.Reader) string {
	t.Helper()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("error reading: %s", err)
	}
	if rc, ok := r.(io.ReadCloser); ok {
		rc.Close()
	}
	return string(body)
}

func Unmarshal[T any](t *testing.T, r io.Reader) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(r).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
