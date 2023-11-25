package websocket_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin/websocket"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

const autobahnImage = "crossbario/autobahn-testsuite:0.8.2"

var defaultIncludedTestCases = []string{
	"*",
}

var defaultExcludedTestCases = []string{
	// These cases all seem to rely on the server accepting fragmented text
	// frames with invalid utf8 payloads, but the spec seems to indicate that
	// every text fragment must be valid utf8 on its own.
	"6.2.3",
	"6.2.4",
	"6.4.2",

	// Compression extensions are not supported
	"12.*",
	"13.*",
}

func TestWebSocketServer(t *testing.T) {
	t.Parallel()

	if os.Getenv("AUTOBAHN_TESTS") == "" {
		t.Skipf("set AUTOBAHN_TESTS=1 to run autobahn integration tests")
	}

	includedTestCases := defaultIncludedTestCases
	excludedTestCases := defaultExcludedTestCases
	if userTestCases := os.Getenv("AUTOBAHN_CASES"); userTestCases != "" {
		t.Logf("using AUTOBAHN_CASES=%q", userTestCases)
		includedTestCases = strings.Split(userTestCases, ",")
		excludedTestCases = []string{}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws := websocket.New(w, r, websocket.Limits{
			MaxFragmentSize: 1024 * 1024 * 16,
			MaxMessageSize:  1024 * 1024 * 16,
		})
		if err := ws.Handshake(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ws.Serve(websocket.EchoHandler)
	}))
	defer srv.Close()

	testDir := newTestDir(t)
	t.Logf("test dir: %s", testDir)

	u, _ := url.Parse(srv.URL)
	targetURL := "ws://host.docker.internal:" + u.Port() + "/websocket/echo"

	autobahnCfg := map[string]any{
		"servers": []map[string]string{
			{
				"agent": "go-httpbin",
				"url":   targetURL,
			},
		},
		"outdir":        "/testdir/report",
		"cases":         includedTestCases,
		"exclude-cases": excludedTestCases,
	}

	autobahnCfgFile, err := os.Create(path.Join(testDir, "autobahn.json"))
	assert.NilError(t, err)
	assert.NilError(t, json.NewEncoder(autobahnCfgFile).Encode(autobahnCfg))
	autobahnCfgFile.Close()

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

	summary := loadSummary(t, testDir)

	for _, results := range summary {
		for caseName, result := range results {
			result := result
			t.Run("autobahn/"+caseName, func(t *testing.T) {
				if result.Behavior == "FAILED" || result.BehaviorClose == "FAILED" {
					report := loadReport(t, testDir, result.ReportFile)
					t.Errorf("description: %s", report.Description)
					t.Errorf("expectation: %s", report.Expectation)
					t.Errorf("result:      %s", report.Result)
					t.Errorf("close:       %s", report.ResultClose)
				}
			})
		}
	}

	t.Logf("autobahn test report: %s", path.Join(testDir, "report/index.html"))
	if os.Getenv("AUTOBAHN_OPEN_REPORT") != "" {
		runCmd(t, exec.Command("open", path.Join(testDir, "report/index.html")))
	}
}

func runCmd(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	t.Logf("running command: %s", cmd.String())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	assert.NilError(t, cmd.Run())
}

func newTestDir(t *testing.T) string {
	t.Helper()
	x, err := os.Getwd()
	assert.NilError(t, err)
	if !strings.HasSuffix(x, "go-httpbin") {
		t.Errorf("unexpected working directory: %s", x)
	}
	testDir, err := filepath.Abs(path.Join(".integrationtests", fmt.Sprintf("autobahn-test-%d", time.Now().Unix())))
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(testDir, 0o755))
	return testDir
}

func loadSummary(t *testing.T, testDir string) autobahnReportSummary {
	t.Helper()
	f, err := os.Open(path.Join(testDir, "report", "index.json"))
	assert.NilError(t, err)
	defer f.Close()
	var summary autobahnReportSummary
	assert.NilError(t, json.NewDecoder(f).Decode(&summary))
	return summary
}

func loadReport(t *testing.T, testDir string, reportFile string) autobahnReportResult {
	t.Helper()
	reportPath := path.Join(testDir, "report", reportFile)
	t.Logf("report path: %s", reportPath)
	f, err := os.Open(reportPath)
	assert.NilError(t, err)
	var report autobahnReportResult
	assert.NilError(t, json.NewDecoder(f).Decode(&report))
	return report
}

type autobahnReportSummary map[string]map[string]autobahnReportResult

type autobahnReportResult struct {
	Behavior      string `json:"behavior"`
	BehaviorClose string `json:"behaviorClose"`
	Description   string `json:"description"`
	Expectation   string `json:"expectation"`
	ReportFile    string `json:"reportfile"`
	Result        string `json:"result"`
	ResultClose   string `json:"resultClose"`
}
