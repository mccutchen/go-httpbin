package httpbin

import (
	"bytes"
	"embed"
	"path"
	"text/template"
)

//go:embed static/*
var staticAssets embed.FS

// return full name of static assert
func staticName(name string) string {
	return path.Join("static", name)
}

// staticAsset loads an embedded static asset by name.
func staticAsset(name string) ([]byte, error) {
	return staticAssets.ReadFile(staticName(name))
}

// mustStaticAsset loads an embedded static asset by name, panicking on error.
func mustStaticAsset(name string) []byte {
	b, err := staticAsset(name)
	if err != nil {
		panic(err)
	}
	return b
}

func (h *HTTPBin) staticTemplateAssert(name string) []byte {
	t, err := template.ParseFS(staticAssets, staticName(name))
	if err != nil {
		panic(err)
	}

	var buf bytes.Buffer

	d := &struct {
		Prefix string
	}{h.prefix}

	err = t.Execute(&buf, d)
	if err != nil {
		panic(err)
	}

	return buf.Bytes()
}
