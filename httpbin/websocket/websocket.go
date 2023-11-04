package websocket

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
)

// See RFC 6455 section 1.3: Opening Handshake
// https://www.rfc-editor.org/rfc/rfc6455#section-1.3
const magicGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const requiredVersion = "13"

type OpCode uint8

const (
	OpCodeContinuation OpCode = 0x0
	OpCodeText         OpCode = 0x1
	OpCodeBinary       OpCode = 0x2
	OpCodeClose        OpCode = 0x8
	OpCodePing         OpCode = 0x9
	OpCodePong         OpCode = 0xA
)

func Prepare(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("Connection") != "Upgrade" {
		return fmt.Errorf("missing required `Connection: Upgrade` header")
	}
	if r.Header.Get("Upgrade") != "websocket" {
		return fmt.Errorf("missing required `Upgrade: websocket` header")
	}
	if v := r.Header.Get("Sec-Websocket-Version"); v != requiredVersion {
		return fmt.Errorf("only websocket version %q is supported, got %q", requiredVersion, v)
	}

	clientKey := r.Header.Get("Sec-Websocket-Key")
	if clientKey == "" {
		return fmt.Errorf("missing required `Sec-Websocket-Key` header")
	}

	w.Header().Set("Connection", "upgrade")
	w.Header().Set("Upgrade", "websocket")
	w.Header().Set("Sec-Websocket-Accept", acceptKey(clientKey))
	w.WriteHeader(http.StatusSwitchingProtocols)
	return nil
}

func Serve(ctx context.Context, buf *bufio.ReadWriter) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			b, err := buf.ReadByte()
			if err != nil {
				return fmt.Errorf("failed to read fin bit: %v", err)
			}
			log.Printf("XXX read byte %v %x %b", b, b, b)

			fin := b&0b10000000 != 0
			log.Printf("XXX is FIN? %v", fin)

			opcode := OpCode(b & 0b00001111)

			mask := b &
		}
	}
}

func acceptKey(clientKey string) string {
	h := sha1.New()
	io.WriteString(h, clientKey+magicGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
