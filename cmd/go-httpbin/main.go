// Package main implements the go-httpbin command line tool.
package main

import (
	"os"

	"github.com/mccutchen/go-httpbin/v2/httpbin/cmd"
)

// Populated at build time
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(cmd.Main(cmd.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    buildDate,
	}))

}
