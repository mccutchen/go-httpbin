package websocket

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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

type Frame struct {
	Fin     uint8
	OpCode  OpCode
	Payload []byte
}

func Prepare(w http.ResponseWriter, r *http.Request) error {
	if strings.ToLower(r.Header.Get("Connection")) != "upgrade" {
		return fmt.Errorf("missing required `Connection: upgrade` header")
	}
	if strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
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
			frame, err := nextFrame(buf)
			if err != nil {
				return err
			}
			log.Printf("XXX got frame: %+v", frame)
			switch frame.OpCode {
			case OpCodeBinary, OpCodeText, OpCodeContinuation:
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
			case OpCodePing:
				frame.OpCode = OpCodePong
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
			case OpCodeClose:
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
				return nil
			default:
				return fmt.Errorf("unsupported opcode: %v", frame.OpCode)
			}
		}
	}
}

func nextFrame(buf *bufio.ReadWriter) (*Frame, error) {
	b1, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}

	fin := b1 & 0b10000000
	log.Printf("XXX FIN: %v", fin != 0)

	opcode := OpCode(b1 & 0b00001111)
	log.Printf("XXX opcode: %v", opcode)

	b2, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}

	// Per https://datatracker.ietf.org/doc/html/rfc6455#section-5.2, all
	// client frames must be masked.
	if masked := b2 & 0b10000000; masked == 0 {
		return nil, fmt.Errorf("received unmasked client frame")
	}

	var payloadLength uint64
	switch {
	case b2-128 <= 125:
		// Payload length is directly represented in the second byte
		payloadLength = uint64(b2 - 128)
		log.Printf("XXX 1 byte payload length: %v", payloadLength)
	case b2-128 == 126:
		// Payload length is represented in the next 2 bytes (16-bit unsigned integer)
		lenBytes := make([]byte, 2)
		if _, err := io.ReadFull(buf, lenBytes); err != nil {
			return nil, err
		}
		payloadLength = uint64(binary.BigEndian.Uint16(lenBytes))
		log.Printf("XXX 2 byte payload length: %v %v", payloadLength, lenBytes)
	case b2-128 == 127:
		// Payload length is represented in the next 8 bytes (64-bit unsigned integer)
		lenBytes := make([]byte, 8)
		if _, err := io.ReadFull(buf, lenBytes); err != nil {
			return nil, err
		}
		payloadLength = binary.BigEndian.Uint64(lenBytes)
		log.Printf("XXX 8 byte payload length: %v %v", payloadLength, lenBytes)
	}

	mask := make([]byte, 4)
	if _, err := io.ReadFull(buf, mask); err != nil {
		return nil, err
	}

	payload := make([]byte, payloadLength)
	if _, err := io.ReadFull(buf, payload); err != nil {
		return nil, err
	}

	for i, b := range payload {
		payload[i] = b ^ mask[i%4]
	}

	return &Frame{
		Fin:     fin,
		OpCode:  opcode,
		Payload: payload,
	}, nil
}

func encodeFrame(frame *Frame) []byte {
	var header []byte

	// Fin and OpCode
	header = append(header, byte(frame.Fin|uint8(frame.OpCode)))

	// Payload length
	payloadLen := len(frame.Payload)
	switch {
	case payloadLen <= 125:
		header = append(header, byte(payloadLen))
	case payloadLen <= 0xFFFF:
		header = append(header, 126)
		header = append(header, byte(payloadLen>>8), byte(payloadLen&0xFF))
	default:
		header = append(header, 127)
		for i := 0; i < 8; i++ {
			header = append(header, byte(payloadLen>>(56-8*i)))
		}
	}

	// log.Printf("XXX ENCODED FRAME %#v", frame)
	// log.Printf("XXX ENCODED BYTES: %v", append(header, frame.Payload...))

	// Combine header and payload to form the frame
	return append(header, frame.Payload...)
}

func writeFrame(buf *bufio.ReadWriter, frame *Frame) error {
	if _, err := buf.Write(encodeFrame(frame)); err != nil {
		return err
	}
	return buf.Flush()
}

func acceptKey(clientKey string) string {
	h := sha1.New()
	io.WriteString(h, clientKey+magicGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
