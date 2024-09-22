package httpbin

import (
	"fmt"
	"runtime"
)

var (
	// Values set with ldflags -X at build time
	buildDate      = "" // ISO-8601 format
	buildGitCommit = ""
	buildGitTag    = ""
)

type VersionInfo struct {
	BuildDate  string `json:"buildDate"`
	GitCommit  string `json:"gitCommit"`
	GitVersion string `json:"gitVersion"`
	GoVersion  string `json:"goVersion"`
	Platform   string `json:"platform"`
}

func getVersionInfo() VersionInfo {
	return VersionInfo{
		BuildDate:  buildDate,
		GitCommit:  buildGitCommit,
		GitVersion: buildGitTag,
		GoVersion:  runtime.Version(),
		Platform:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}
