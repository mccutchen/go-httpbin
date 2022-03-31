package httpbin

import (
	"embed"
	"path"
)

//go:embed static/*
var staticAssets embed.FS

// staticAsset loads an embedded static asset by name.
func staticAsset(name string) ([]byte, error) {
	return staticAssets.ReadFile(path.Join("static", name))
}

// mustStaticAsset loads an embedded static asset by name, panicking on error.
func mustStaticAsset(name string) []byte {
	b, err := staticAsset(name)
	if err != nil {
		panic(err)
	}
	return b
}
