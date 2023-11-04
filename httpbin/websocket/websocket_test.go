package websocket

import (
	"testing"

	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func TestAcceptKey(t *testing.T) {
	clientKey := "dGhlIHNhbXBsZSBub25jZQ=="
	wantAcceptKey := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	gotAcceptKey := acceptKey(clientKey)
	assert.Equal(t, gotAcceptKey, wantAcceptKey, "incorrect accept key")
}
