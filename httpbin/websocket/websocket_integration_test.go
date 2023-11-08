package websocket_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

const autobahnImage = "crossbario/autobahn-testsuite:0.8.2"

func TestHandshake(t *testing.T) {
	app := httpbin.New()
	srv := httptest.NewServer(app)
	defer srv.Close()

	testCases := map[string]struct {
		reqHeaders      map[string]string
		wantStatus      int
		wantRespHeaders map[string]string
	}{
		"valid handshake": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantRespHeaders: map[string]string{
				"Connection":           "upgrade",
				"Upgrade":              "websocket",
				"Sec-Websocket-Accept": "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"valid handshake, header values case insensitive": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Upgrade":               "WebSocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantRespHeaders: map[string]string{
				"Connection":           "upgrade",
				"Upgrade":              "websocket",
				"Sec-Websocket-Accept": "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"missing Connection header": {
			reqHeaders: map[string]string{
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect Connection header": {
			reqHeaders: map[string]string{
				"Connection":            "close",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"missing Upgrade header": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect Upgrade header": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Upgrade":               "http/2",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"missing version": {
			reqHeaders: map[string]string{
				"Connection":        "upgrade",
				"Upgrade":           "websocket",
				"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect version": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "12",
			},
			wantStatus: http.StatusBadRequest,
		},
		"missing Sec-WebSocket-Key": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
	}
	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/ws", nil)
			for k, v := range tc.reqHeaders {
				req.Header.Set(k, v)
			}

			resp, err := http.DefaultClient.Do(req)
			assert.NilError(t, err)

			assert.StatusCode(t, resp, tc.wantStatus)
			for k, v := range tc.wantRespHeaders {
				assert.Equal(t, resp.Header.Get(k), v, "incorrect value for %q response header", k)
			}
		})
	}
}

func TestWebsocketServer(t *testing.T) {
	t.Parallel()

	app := httpbin.New()
	srv := httptest.NewServer(app)
	defer srv.Close()

	testDir, err := os.MkdirTemp("", "go-httpbin-autobahn-test")
	assert.NilError(t, err)
	// defer os.RemoveAll(testDir)
	t.Logf("test dir: %s", testDir)

	u, _ := url.Parse(srv.URL)
	targetURL := "ws://host.docker.internal:" + u.Port() + "/ws"

	autobahnCfg := map[string]any{
		"servers": []map[string]string{
			{
				"agent": "go-httpbin",
				"url":   targetURL,
			},
		},
		"outdir": "/testdir/report",
		"cases": []string{
			"1.*",
			"2.*",
			"3.*",
			"4.*",
			"5.*",
			"6.*",
			"7.*",
		},
		"excluded-cases": []string{},
	}

	autobahnCfgFile, err := os.Create(path.Join(testDir, "autobahn.json"))
	assert.NilError(t, err)
	defer autobahnCfgFile.Close()
	enc := json.NewEncoder(autobahnCfgFile)
	enc.SetIndent("", "  ")
	assert.NilError(t, enc.Encode(autobahnCfg))

	pullCmd := exec.Command("docker", "pull", autobahnImage)
	runCmd(t, pullCmd)

	testCmd := exec.Command(
		"docker",
		"run",
		"--net=host",
		"--rm",
		"-v", testDir+":/testdir:rw",
		autobahnImage,
		"wstest", "-m", "fuzzingclient", "--spec", "/testdir/autobahn.json",
	)
	runCmd(t, testCmd)

	f, err := os.Open(path.Join(testDir, "report", "index.json"))
	assert.NilError(t, err)
	defer f.Close()

	var report autobahnReportIndex
	assert.NilError(t, json.NewDecoder(f).Decode(&report))

	for _, results := range report {
		for caseName, result := range results {
			t.Run("autobahn/"+caseName, func(t *testing.T) {
				if result.Behavior == "FAILED" {
					t.Errorf("behavior test failed")
					t.Logf("report: %s", path.Join(testDir, "report", result.ReportFile))
				}
				if result.BehaviorClose == "FAILED" {
					t.Errorf("behavior test failed due to improper close")
					t.Logf("report: %s", path.Join(testDir, "report", result.ReportFile))
				}
			})
		}
	}

	t.Logf("autobahn test report: %s", path.Join(testDir, "report/index.html"))
}

func runCmd(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Logf("=== command: %s ===", cmd.String())
	cmd.Stdout = &testLogWriter{t}
	cmd.Stderr = &testLogWriter{t}
	assert.NilError(t, cmd.Run())
}

type testLogWriter struct {
	t *testing.T
}

func (w *testLogWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(p, []byte("\n")) {
		w.t.Log(string(line))
	}
	return len(p), nil
}

type autobahnReportIndex map[string]map[string]autobahnReportResult

type autobahnReportResult struct {
	Behavior      string `json:"behavior"`
	BehaviorClose string `json:"behaviorClose"`
	ReportFile    string `json:"reportfile"`
}
