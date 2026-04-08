// Package main implements the go-httpbin command line tool.
package main

import (
	"os"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin/cmd"
)

// Build metadata, populated by the release process (see .goreleaser.yaml).
var (
	version   = "dev"
	commit    = "HEAD"
	buildDate = time.Now().String()
)

func main() {
	// TODO: incorporate into a `--version` flag.
	{
		_ = version
		_ = commit
		_ = buildDate
	}

	os.Exit(cmd.Main())
}
