// Package main implements the go-httpbin command line tool.
package main

import (
	"os"

	"github.com/mccutchen/go-httpbin/v2/httpbin/cmd"
)

func main() {
	os.Exit(cmd.Main())
}
