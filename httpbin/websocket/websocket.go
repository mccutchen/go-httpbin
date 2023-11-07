package websocket

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

const requiredVersion = "13"

const (
	maxControlFrameSize = 125
	maxFragmentSize     = 1024 * 1024
	maxMessageSize      = 1024 * 1024 * 2
)

type OpCode uint8

const (
	OpCodeContinuation OpCode = 0x0
	OpCodeText         OpCode = 0x1
	OpCodeBinary       OpCode = 0x2
	OpCodeClose        OpCode = 0x8
	OpCodePing         OpCode = 0x9
	OpCodePong         OpCode = 0xA
)

type StatusCode uint16

const (
	StatusNormalClosure      StatusCode = 1000
	StatusGoingAway          StatusCode = 1001
	StatusProtocolError      StatusCode = 1002
	StatusUnsupported        StatusCode = 1003
	StatusNoStatusRcvd       StatusCode = 1005
	StatusAbnormalClose      StatusCode = 1006
	StatusUnsupportedPayload StatusCode = 1007
	StatusPolicyViolation    StatusCode = 1008
	StatusTooLarge           StatusCode = 1009
	StatusTlsHandshake       StatusCode = 1015
	StatusServerError        StatusCode = 1011
)

type Frame struct {
	Fin     bool
	OpCode  OpCode
	Payload []byte
}

// Handshake validates the request and performs the WebSocket handshake.
func Handshake(w http.ResponseWriter, r *http.Request) error {
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

type Message struct {
	Binary bool
	Data   []byte
}

type Handler func(ctx context.Context, msg *Message) (*Message, error)

// Serve handles a websocket connection after the handshake has been completed.
func Serve(ctx context.Context, buf *bufio.ReadWriter, handler Handler) error {
	var (
		msgReady         = false
		needContinuation = false
		msg              *Message
	)

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

			if err := validateFrame(frame); err != nil {
				return err
			}

			switch frame.OpCode {
			case OpCodeBinary, OpCodeText:
				if needContinuation {
					return closeWithError(buf, StatusProtocolError, errors.New("expected continuation frame"))
				}
				msg = &Message{
					Binary: frame.OpCode == OpCodeBinary,
					Data:   frame.Payload,
				}
				msgReady = frame.Fin
				needContinuation = !frame.Fin
			case OpCodeContinuation:
				if !needContinuation {
					return closeWithError(buf, StatusProtocolError, errors.New("unexpected continuation frame"))
				}
				msgReady = frame.Fin
				needContinuation = !frame.Fin
				msg.Data = append(msg.Data, frame.Payload...)
				if len(msg.Data) > maxMessageSize {
					return closeWithError(buf, StatusTooLarge, fmt.Errorf("message size %d exceeds maximum of %d bytes", len(msg.Data), maxMessageSize))
				}
			case OpCodeClose:
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
				return nil
			case OpCodePing:
				frame.OpCode = OpCodePong
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
			case OpCodePong:
				// no-op
			default:
				return fmt.Errorf("unsupported opcode: %v", frame.OpCode)
			}
		}

		if msgReady {
			resp, err := handler(ctx, msg)
			if err != nil {
				return err
			}
			log.Printf("XXX got resp: %+v", resp)
			if resp == nil {
				return nil
			}
			for _, respFrame := range frameMessage(resp) {
				if err := writeFrame(buf, respFrame); err != nil {
					return err
				}
			}
			msg = nil
			msgReady = false
			needContinuation = false
		}
	}
}

func nextFrame(buf *bufio.ReadWriter) (*Frame, error) {
	b1, err := buf.ReadByte()
	if err != nil {
		return nil, err
	}

	fin := b1&0b10000000 != 0
	opcode := OpCode(b1 & 0b00001111)

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
	case b2-128 == 126:
		// Payload length is represented in the next 2 bytes (16-bit unsigned integer)
		lenBytes := make([]byte, 2)
		if _, err := io.ReadFull(buf, lenBytes); err != nil {
			return nil, err
		}
		payloadLength = uint64(binary.BigEndian.Uint16(lenBytes))
	case b2-128 == 127:
		// Payload length is represented in the next 8 bytes (64-bit unsigned integer)
		lenBytes := make([]byte, 8)
		if _, err := io.ReadFull(buf, lenBytes); err != nil {
			return nil, err
		}
		payloadLength = binary.BigEndian.Uint64(lenBytes)
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

	// FIN and OPCODE
	var fin uint8 = 0
	if frame.Fin {
		fin = 1 << 7
	}
	header = append(header, byte(fin|uint8(frame.OpCode)))

	// payload length
	payloadLen := int64(len(frame.Payload))
	switch {
	case payloadLen <= 125:
		header = append(header, byte(payloadLen))
	case payloadLen <= 0xFFFF:
		header = append(header, 126)
		header = binary.BigEndian.AppendUint16(header, uint16(payloadLen))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(payloadLen))
	}

	// frame is header + payload
	return append(header, frame.Payload...)
}

func writeFrame(buf *bufio.ReadWriter, frame *Frame) error {
	log.Printf("XXX writing frame: %+v", frame)
	if _, err := buf.Write(encodeFrame(frame)); err != nil {
		return err
	}
	return buf.Flush()
}

// frameMessage splits a message into N frames with payloads of at most
// fragmentSize bytes.
func frameMessage(msg *Message) []*Frame {
	var result []*Frame

	fin := false
	opcode := OpCodeText
	if msg.Binary {
		opcode = OpCodeBinary
	}

	offset := 0
	dataLen := len(msg.Data)
	for {
		if offset > 0 {
			opcode = OpCodeContinuation
		}
		end := offset + maxFragmentSize
		if end >= dataLen {
			fin = true
			end = dataLen
		}
		log.Printf("ZZZ fragment: %d:%d of %d", offset, end, dataLen)
		result = append(result, &Frame{
			Fin:     fin,
			OpCode:  opcode,
			Payload: msg.Data[offset:end],
		})
		if fin {
			break
		}
	}
	return result
}

func closeWithError(buf *bufio.ReadWriter, code StatusCode, err error) error {
	var payload []byte
	payload = binary.BigEndian.AppendUint16(payload, uint16(code))
	if err != nil {
		payload = append(payload, []byte(err.Error())...)
	}
	return writeFrame(buf, &Frame{
		Fin:     true,
		OpCode:  OpCodeClose,
		Payload: payload,
	})
}

func acceptKey(clientKey string) string {
	// Magic value comes from RFC 6455 section 1.3: Opening Handshake
	// https://www.rfc-editor.org/rfc/rfc6455#section-1.3
	h := sha1.New()
	io.WriteString(h, clientKey+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func validateFrame(frame *Frame) error {
	switch frame.OpCode {
	case OpCodeContinuation, OpCodeText, OpCodeBinary:
		if len(frame.Payload) > maxFragmentSize {
			return fmt.Errorf("frame payload size %d exceeds maximum of %d bytes", len(frame.Payload), maxFragmentSize)
		}
	case OpCodeClose, OpCodePing, OpCodePong:
		if len(frame.Payload) > maxControlFrameSize {
			return fmt.Errorf("frame payload size %d exceeds maximum of %d bytes", len(frame.Payload), maxControlFrameSize)
		}
		if !frame.Fin {
			return fmt.Errorf("control frame %v must not be fragmented", frame.OpCode)
		}
	}
	return nil
}
