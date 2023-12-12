package httpbin

import (
	"bytes"
	"embed"
	"path"
	"text/template"
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

func (h *HTTPBin) mustRenderTemplate(name string) []byte {
	t, err := template.New(name).Parse(string(mustStaticAsset(name)))
	if err != nil {
		panic(err)
	}
	var buf bytes.Buffer
	ctx := struct{ Prefix string }{Prefix: h.prefix}
	if err := t.Execute(&buf, ctx); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
