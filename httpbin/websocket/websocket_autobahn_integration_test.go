package websocket_test

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"testing"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

const autobahnImage = "crossbario/autobahn-testsuite:0.8.2"

var autobahnTestCases = []string{
	"1.*",
	"2.*",
	"3.*",
	"4.*",
	"5.*",
	"6.*",
	"7.*",
}

var autobahnExcludedTestCases = []string{
	// These cases all seem to rely on the server accepting fragmented text
	// frames with invalid utf8 payloads, but the spec seems to indicate that
	// every text fragment must be valid utf8 on its own.
	"6.2.3",
	"6.2.4",
	"6.4.2",
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
		"outdir":        "/testdir/report",
		"cases":         autobahnTestCases,
		"exclude-cases": autobahnExcludedTestCases,
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

	failed := false
	for _, results := range report {
		for caseName, result := range results {
			t.Run("autobahn/"+caseName, func(t *testing.T) {
				if result.Behavior == "FAILED" {
					t.Errorf("test failed")
					t.Logf("report: %s", path.Join(testDir, "report", result.ReportFile))
					failed = true
				}
				if result.BehaviorClose == "FAILED" {
					t.Errorf("test failed on close")
					t.Logf("report: %s", path.Join(testDir, "report", result.ReportFile))
					failed = true
				}
			})
		}
	}

	t.Logf("autobahn test report: %s", path.Join(testDir, "report/index.html"))
	if failed {
		runCmd(t, exec.Command("open", path.Join(testDir, "report/index.html")))
	}
}

func runCmd(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	assert.NilError(t, cmd.Run())
}

type autobahnReportIndex map[string]map[string]autobahnReportResult

type autobahnReportResult struct {
	Behavior      string `json:"behavior"`
	BehaviorClose string `json:"behaviorClose"`
	ReportFile    string `json:"reportfile"`
}
