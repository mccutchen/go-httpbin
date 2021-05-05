// +build go1.16

package static

import (
	"embed"
)

//go:embed *.*
var Fs embed.FS
