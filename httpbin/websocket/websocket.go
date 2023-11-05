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

type Frame struct {
	Fin     uint8
	OpCode  OpCode
	Payload []byte
}

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
			frame, err := nextFrame(buf)
			if err != nil {
				return err
			}
			log.Printf("XXX got frame: %+v", frame)
			log.Printf("XXX got frame: %v", encodeFrame(frame))
			if frame.OpCode == OpCodeText {
				log.Printf("XXX got text frame: %s", string(frame.Payload))
			}
			switch frame.OpCode {
			case OpCodePing:
				log.Printf("XXX handling ping frame")
				frame.OpCode = OpCodePong
				if _, err := buf.Write(encodeFrame(frame)); err != nil {
					return err
				}
				if err := buf.Flush(); err != nil {
					return err
				}
			case OpCodeClose:
				log.Printf("XXX handling close frame")
				return nil
			case OpCodeContinuation:
				return fmt.Errorf("continuation frames are not supported")
			case OpCodeBinary, OpCodeText:
				log.Printf("XXX echoing frame")
				if _, err := buf.Write(encodeFrame(frame)); err != nil {
					return err
				}
				if err := buf.Flush(); err != nil {
					return err
				}
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
	log.Printf("XXX read byte %v %x %b", b1, b1, b1)

	fin := b1 & 0b10000000
	log.Printf("XXX FIN: %v", fin)

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

	// Extract the payload length based on the opcode and payload length
	// information in the first byte
	var payloadLength uint64
	switch {
	case b2-128 <= 125:
		// Payload length is directly represented in the second byte
		payloadLength = uint64(b2 - 128)
	case b2-128 == 126:
		// Payload length is represented in the next 2 bytes (16-bit unsigned integer)
		b3, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b4, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		payloadLength = uint64(b3)<<8 + uint64(b4)
	case b2-128 == 127:
		// Payload length is represented in the next 8 bytes (64-bit unsigned integer)
		b3, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b4, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b5, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b6, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b7, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b8, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b9, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		b10, err := buf.ReadByte()
		if err != nil {
			return nil, err
		}
		payloadLength = uint64(b3)<<56 + uint64(b4)<<48 + uint64(b5)<<40 + uint64(b6)<<32 +
			uint64(b7)<<24 + uint64(b8)<<16 + uint64(b9)<<8 + uint64(b10)
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
	header = append(header, byte((frame.Fin<<7)|uint8(frame.OpCode)))

	// Payload length
	payloadLen := len(frame.Payload)
	if payloadLen <= 125 {
		header = append(header, byte(payloadLen))
	} else if payloadLen <= 0xFFFF {
		header = append(header, 126)
		header = append(header, byte(payloadLen>>8), byte(payloadLen&0xFF))
	} else {
		header = append(header, 127)
		for i := 0; i < 8; i++ {
			header = append(header, byte(payloadLen>>(56-8*i)))
		}
	}

	// Combine header and payload to form the frame
	return append(header, frame.Payload...)
}

func acceptKey(clientKey string) string {
	h := sha1.New()
	io.WriteString(h, clientKey+magicGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
