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
	"net/http"
	"strings"
	"unicode/utf8"
)

const requiredVersion = "13"

type Opcode uint8

const (
	OpcodeContinuation Opcode = 0x0
	OpcodeText         Opcode = 0x1
	OpcodeBinary       Opcode = 0x2
	OpcodeClose        Opcode = 0x8
	OpcodePing         Opcode = 0x9
	OpcodePong         Opcode = 0xA
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

// Frame is a websocket frame.
type Frame struct {
	Fin     bool
	RSV1    bool
	RSV3    bool
	RSV2    bool
	Opcode  Opcode
	Payload []byte
}

// Message is a message from the client, which may be constructed from one or
// more individual frames.
type Message struct {
	Binary  bool
	Payload []byte
}

// Handler handles a single websocket message. If the returned message is
// non-nil, it will be sent to the client. If an error is returned, the
// connection will be closed.
type Handler func(ctx context.Context, msg *Message) (*Message, error)

// Limits defines the limits imposed on a websocket connections.
type Limits struct {
	MaxFragmentSize int
	MaxMessageSize  int
}

// WebSocket is a websocket connection.
type WebSocket struct {
	maxFragmentSize int
	maxMessageSize  int
	handshook       bool
}

// New creates a new websocket.
func New(limits Limits) *WebSocket {
	return &WebSocket{
		maxFragmentSize: limits.MaxFragmentSize,
		maxMessageSize:  limits.MaxMessageSize,
	}
}

// Handshake validates the request and performs the WebSocket handshake.
func (s *WebSocket) Handshake(w http.ResponseWriter, r *http.Request) error {
	if s.handshook {
		panic("websocket: handshake: handshake already completed")
	}

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

	s.handshook = true
	return nil
}

// Serve handles a websocket connection after the handshake has been completed.
func (s *WebSocket) Serve(w http.ResponseWriter, r *http.Request, handler Handler) {
	if !s.handshook {
		panic("websocket: serve: handshake not completed")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		panic("websocket: serve: server does not support hijacking")
	}

	conn, buf, err := hj.Hijack()
	if err != nil {
		panic(fmt.Errorf("websocket: serve: hijack failed: %s", err))
	}
	defer conn.Close()

	// errors intentionally ignored here. it's serverLoop's responsibility to
	// properly close the websocket connection with a useful error message, and
	// any unexpected error returned from serverLoop is not actionable.
	_ = s.serveLoop(r.Context(), buf, handler)
}

func (s *WebSocket) serveLoop(ctx context.Context, buf *bufio.ReadWriter, handler Handler) error {
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
				return writeCloseFrame(buf, StatusServerError, err)
			}

			if err := validateFrame(frame, s.maxFragmentSize); err != nil {
				return writeCloseFrame(buf, StatusProtocolError, err)
			}

			switch frame.Opcode {
			case OpcodeBinary, OpcodeText:
				if needContinuation {
					return writeCloseFrame(buf, StatusProtocolError, errors.New("expected continuation frame"))
				}
				if frame.Opcode == OpcodeText && !utf8.Valid(frame.Payload) {
					return writeCloseFrame(buf, StatusUnsupportedPayload, errors.New("invalid UTF-8"))
				}
				msg = &Message{
					Binary:  frame.Opcode == OpcodeBinary,
					Payload: frame.Payload,
				}
				msgReady = frame.Fin
				needContinuation = !frame.Fin
			case OpcodeContinuation:
				if !needContinuation {
					return writeCloseFrame(buf, StatusProtocolError, errors.New("unexpected continuation frame"))
				}
				if !msg.Binary && !utf8.Valid(frame.Payload) {
					return writeCloseFrame(buf, StatusUnsupportedPayload, errors.New("invalid UTF-8"))
				}
				msgReady = frame.Fin
				needContinuation = !frame.Fin
				msg.Payload = append(msg.Payload, frame.Payload...)
				if len(msg.Payload) > s.maxMessageSize {
					return writeCloseFrame(buf, StatusTooLarge, fmt.Errorf("message size %d exceeds maximum of %d bytes", len(msg.Payload), s.maxMessageSize))
				}
			case OpcodeClose:
				return writeCloseFrame(buf, StatusNormalClosure, nil)
			case OpcodePing:
				frame.Opcode = OpcodePong
				if err := writeFrame(buf, frame); err != nil {
					return err
				}
			case OpcodePong:
				// no-op
			default:
				return writeCloseFrame(buf, StatusProtocolError, fmt.Errorf("unsupported opcode: %v", frame.Opcode))
			}
		}

		if msgReady {
			resp, err := handler(ctx, msg)
			if err != nil {
				return writeCloseFrame(buf, StatusServerError, err)
			}
			if resp == nil {
				continue
			}
			for _, respFrame := range frameResponse(resp, s.maxFragmentSize) {
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

	var (
		fin    = b1&0b10000000 != 0
		rsv1   = b1&0b01000000 != 0
		rsv2   = b1&0b00100000 != 0
		rsv3   = b1&0b00010000 != 0
		opcode = Opcode(b1 & 0b00001111)
	)

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
		RSV1:    rsv1,
		RSV2:    rsv2,
		RSV3:    rsv3,
		Opcode:  opcode,
		Payload: payload,
	}, nil
}

func encodeFrame(frame *Frame) []byte {
	var header []byte

	// FIN, RSV1-3, OPCODE
	var b1 byte
	if frame.Fin {
		b1 |= 0b10000000
	}
	if frame.RSV1 {
		b1 |= 0b01000000
	}
	if frame.RSV2 {
		b1 |= 0b00100000
	}
	if frame.RSV3 {
		b1 |= 0b00010000
	}
	b1 |= uint8(frame.Opcode) & 0b00001111
	header = append(header, b1)

	// payload length
	payloadLen := int64(len(frame.Payload))
	switch {
	case payloadLen <= 125:
		header = append(header, byte(payloadLen))
	case payloadLen <= 65535:
		header = append(header, 126)
		header = binary.BigEndian.AppendUint16(header, uint16(payloadLen))
	default:
		header = append(header, 127)
		header = binary.BigEndian.AppendUint64(header, uint64(payloadLen))
	}

	return append(header, frame.Payload...)
}

func writeFrame(buf *bufio.ReadWriter, frame *Frame) error {
	if _, err := buf.Write(encodeFrame(frame)); err != nil {
		return err
	}
	return buf.Flush()
}

// frameResponse splits a message into N frames with payloads of at most
// fragmentSize bytes.
func frameResponse(msg *Message, fragmentSize int) []*Frame {
	var result []*Frame

	fin := false
	opcode := OpcodeText
	if msg.Binary {
		opcode = OpcodeBinary
	}

	offset := 0
	dataLen := len(msg.Payload)
	for {
		if offset > 0 {
			opcode = OpcodeContinuation
		}
		end := offset + fragmentSize
		if end >= dataLen {
			fin = true
			end = dataLen
		}
		result = append(result, &Frame{
			Fin:     fin,
			Opcode:  opcode,
			Payload: msg.Payload[offset:end],
		})
		if fin {
			break
		}
	}
	return result
}

// writeCloseFrame writes a close frame to the wire, with an optional error
// message.
func writeCloseFrame(buf *bufio.ReadWriter, code StatusCode, err error) error {
	var payload []byte
	payload = binary.BigEndian.AppendUint16(payload, uint16(code))
	if err != nil {
		payload = append(payload, []byte(err.Error())...)
	}
	return writeFrame(buf, &Frame{
		Fin:     true,
		Opcode:  OpcodeClose,
		Payload: payload,
	})
}

var reservedStatusCodes = map[uint16]bool{
	// Explicitly reserved by RFC section 7.4.1 Defined Status Codes:
	// https://datatracker.ietf.org/doc/html/rfc6455#section-7.4.1
	1004: true,
	1005: true,
	1006: true,
	1015: true,
	// Apparently reserved, according to the autobahn testsuite's fuzzingclient
	// tests, though it's not clear to me why, based on the RFC.
	//
	// See: https://github.com/crossbario/autobahn-testsuite
	1016: true,
	1100: true,
	2000: true,
	2999: true,
}

func validateFrame(frame *Frame, maxFragmentSize int) error {
	// We do not support any extensions, per the spec all RSV bits must be 0:
	// https://datatracker.ietf.org/doc/html/rfc6455#section-5.2
	if frame.RSV1 || frame.RSV2 || frame.RSV3 {
		return fmt.Errorf("frame has unsupported RSV bits set")
	}

	switch frame.Opcode {
	case OpcodeContinuation, OpcodeText, OpcodeBinary:
		if len(frame.Payload) > maxFragmentSize {
			return fmt.Errorf("frame payload size %d exceeds maximum of %d bytes", len(frame.Payload), maxFragmentSize)
		}
	case OpcodeClose, OpcodePing, OpcodePong:
		// All control frames MUST have a payload length of 125 bytes or less
		// and MUST NOT be fragmented.
		// https://datatracker.ietf.org/doc/html/rfc6455#section-5.5
		if len(frame.Payload) > 125 {
			return fmt.Errorf("frame payload size %d exceeds 125 bytes", len(frame.Payload))
		}
		if !frame.Fin {
			return fmt.Errorf("control frame %v must not be fragmented", frame.Opcode)
		}
	}

	if frame.Opcode == OpcodeClose {
		if len(frame.Payload) == 0 {
			return nil
		}
		if len(frame.Payload) == 1 {
			return fmt.Errorf("close frame payload must be at least 2 bytes")
		}

		code := binary.BigEndian.Uint16(frame.Payload[:2])
		if code < 1000 || code >= 5000 {
			return fmt.Errorf("close frame status code %d out of range", code)
		}
		if reservedStatusCodes[code] {
			return fmt.Errorf("close frame status code %d is reserved", code)
		}

		if len(frame.Payload) > 2 {
			if !utf8.Valid(frame.Payload[2:]) {
				return errors.New("close frame payload must be vaid UTF-8")
			}
		}
	}

	return nil
}

func acceptKey(clientKey string) string {
	// Magic value comes from RFC 6455 section 1.3: Opening Handshake
	// https://www.rfc-editor.org/rfc/rfc6455#section-1.3
	h := sha1.New()
	io.WriteString(h, clientKey+"258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
