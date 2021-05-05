// +build go1.16

package assets

import (
	"fmt"
	"strings"

	"github.com/mccutchen/go-httpbin/static"
)

// Asset loads and returns the asset for the given name.
// It returns an error if the asset could not be found or
// could not be loaded.
func Asset(name string) ([]byte, error) {
	canonicalName := strings.Replace(name, "\\", "/", -1)
	data, err := static.Fs.ReadFile(canonicalName)
	if err != nil {
		return nil, fmt.Errorf("Asset %s not found", name)
	}
	return data, nil
}

// MustAsset is like Asset but panics when Asset would return an error.
// It simplifies safe initialization of global variables.
func MustAsset(name string) []byte {
	a, err := Asset(name)
	if err != nil {
		panic("asset: Asset(" + name + "): " + err.Error())
	}

	return a
}
