package websocket_test

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin/websocket"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func TestHandshake(t *testing.T) {
	testCases := map[string]struct {
		reqHeaders      map[string]string
		wantStatus      int
		wantRespHeaders map[string]string
	}{
		"valid handshake": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantRespHeaders: map[string]string{
				"Connection":           "upgrade",
				"Upgrade":              "websocket",
				"Sec-Websocket-Accept": "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"valid handshake, header values case insensitive": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Upgrade":               "WebSocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantRespHeaders: map[string]string{
				"Connection":           "upgrade",
				"Upgrade":              "websocket",
				"Sec-Websocket-Accept": "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"missing Connection header is okay": {
			reqHeaders: map[string]string{
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"incorrect Connection header is also okay": {
			reqHeaders: map[string]string{
				"Connection":            "foo",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusSwitchingProtocols,
		},
		"missing Upgrade header": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect Upgrade header": {
			reqHeaders: map[string]string{
				"Connection":            "Upgrade",
				"Upgrade":               "http/2",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"missing version": {
			reqHeaders: map[string]string{
				"Connection":        "upgrade",
				"Upgrade":           "websocket",
				"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect version": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "12",
			},
			wantStatus: http.StatusBadRequest,
		},
		"missing Sec-WebSocket-Key": {
			reqHeaders: map[string]string{
				"Connection":            "upgrade",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ws := websocket.New(w, r, websocket.Limits{})
				if err := ws.Handshake(); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				ws.Serve(websocket.EchoHandler)
			}))
			defer srv.Close()

			req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
			for k, v := range tc.reqHeaders {
				req.Header.Set(k, v)
			}

			resp, err := http.DefaultClient.Do(req)
			assert.NilError(t, err)

			assert.StatusCode(t, resp, tc.wantStatus)
			for k, v := range tc.wantRespHeaders {
				assert.Equal(t, resp.Header.Get(k), v, "incorrect value for %q response header", k)
			}
		})
	}
}

func TestHandshakeOrder(t *testing.T) {
	handshakeReq := httptest.NewRequest(http.MethodGet, "/websocket/echo", nil)
	for k, v := range map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
		"Sec-WebSocket-Version": "13",
	} {
		handshakeReq.Header.Set(k, v)
	}

	t.Run("double handshake", func(t *testing.T) {
		w := httptest.NewRecorder()
		ws := websocket.New(w, handshakeReq, websocket.Limits{})

		// first handshake succeeds
		assert.NilError(t, ws.Handshake())
		assert.Equal(t, w.Code, http.StatusSwitchingProtocols, "incorrect status code")

		// second handshake fails
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected to catch panic on double handshake")
			}
			assert.Equal(t, fmt.Sprint(r), "websocket: handshake already completed", "incorrect panic message")
		}()
		ws.Handshake()
	})

	t.Run("handshake not completed", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected to catch panic on Serve before Handshake")
			}
			assert.Equal(t, fmt.Sprint(r), "websocket: serve: handshake not completed", "incorrect panic message")
		}()
		w := httptest.NewRecorder()
		websocket.New(w, handshakeReq, websocket.Limits{}).Serve(nil)
	})

	t.Run("http.Hijack not implemented", func(t *testing.T) {
		// confirm that httptest.ResponseRecorder does not implmeent
		// http.Hjijacker
		var rw http.ResponseWriter = httptest.NewRecorder()
		_, ok := rw.(http.Hijacker)
		assert.Equal(t, ok, false, "expected httptest.ResponseRecorder not to implement http.Hijacker")

		w := httptest.NewRecorder()
		ws := websocket.New(w, handshakeReq, websocket.Limits{})

		assert.NilError(t, ws.Handshake())
		assert.Equal(t, w.Code, http.StatusSwitchingProtocols, "incorrect status code")

		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected to catch panic on when http.Hijack not implemented")
			}
			assert.Equal(t, fmt.Sprint(r), "websocket: serve: server does not support hijacking", "incorrect panic message")
		}()
		ws.Serve(nil)
	})

	t.Run("hijack failed", func(t *testing.T) {
		w := &brokenHijackResponseWriter{}
		ws := websocket.New(w, handshakeReq, websocket.Limits{})

		assert.NilError(t, ws.Handshake())
		assert.Equal(t, w.Code, http.StatusSwitchingProtocols, "incorrect status code")

		defer func() {
			r := recover()
			if r == nil {
				t.Fatalf("expected to catch panic on Serve before Handshake")
			}
			assert.Equal(t, fmt.Sprint(r), "websocket: serve: hijack failed: error hijacking connection", "incorrect panic message")
		}()
		ws.Serve(nil)
	})
}

func TestConnectionLimits(t *testing.T) {
	t.Run("maximum request duration is enforced", func(t *testing.T) {
		t.Parallel()

		maxDuration := 200 * time.Millisecond

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws := websocket.New(w, r, websocket.Limits{
				MaxDuration: maxDuration,
				// TODO: test these limits as well
				MaxFragmentSize: 128,
				MaxMessageSize:  256,
			})
			if err := ws.Handshake(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ws.Serve(websocket.EchoHandler)
		}))
		defer srv.Close()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		reqParts := []string{
			"GET /websocket/echo HTTP/1.1",
			"Host: test",
			"Connection: upgrade",
			"Upgrade: websocket",
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
			"Sec-WebSocket-Version: 13",
		}
		reqBytes := []byte(strings.Join(reqParts, "\r\n") + "\r\n\r\n")
		t.Logf("raw request:\n%q", reqBytes)

		// start timer before sending the request to ensure the client
		// duration measurement is at least as long as the server's duration,
		// to avoid flakiness
		start := time.Now()

		// first, we write the request line and headers, which should cause the
		// server to respond with a 101 Switching Protocols response.
		{
			n, err := conn.Write(reqBytes)
			assert.NilError(t, err)
			assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusSwitchingProtocols)
		}

		// next, we try to read from the connection, expecting the connection
		// to be closed after roughly maxDuration seconds
		{
			resp, err := io.ReadAll(conn)
			elapsed := time.Since(start)
			// we sometimes get a non-nil error and some garbage in the
			// (partial?) resp read from the server, like
			//
			//     \x88\x18\x03\xf3read pipe: i/o timeout
			//
			// So for now we make sure the test took the expected amount
			// of time and only validate the error if we got one.
			if err != nil {
				assert.Error(t, err, io.EOF)
			}
			if len(resp) > 0 {
				t.Logf("unexpected response data: %q", resp)
			}
			assert.MinDuration(t, elapsed, maxDuration)
		}
	})

	t.Run("client closing connection", func(t *testing.T) {
		t.Parallel()

		// the client will close the connection well before the server closes
		// the connection. make sure the server properly handles the client
		// closure.
		var (
			clientTimeout     = 100 * time.Millisecond
			serverTimeout     = time.Hour // should never be reached
			elapsedClientTime time.Duration
			elapsedServerTime time.Duration
			wg                sync.WaitGroup

			// avoid racy/flaky test timings by starting server timer before
			// starting server itself to ensure elapsedServerTime is always
			// longer than clientTimeot
			serverStart = time.Now()
		)

		wg.Add(1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer wg.Done()
			ws := websocket.New(w, r, websocket.Limits{
				MaxDuration:     serverTimeout,
				MaxFragmentSize: 128,
				MaxMessageSize:  256,
			})
			if err := ws.Handshake(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ws.Serve(websocket.EchoHandler)
			elapsedServerTime = time.Since(serverStart)
		}))
		defer srv.Close()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		reqParts := []string{
			"GET /websocket/echo HTTP/1.1",
			"Host: test",
			"Connection: upgrade",
			"Upgrade: websocket",
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
			"Sec-WebSocket-Version: 13",
		}
		reqBytes := []byte(strings.Join(reqParts, "\r\n") + "\r\n\r\n")
		t.Logf("raw request:\n%q", reqBytes)

		// avoid racy/flaky test timings by starting client timer before
		// setting conn deadline to ensure elapsedClientTime is at least as
		// long as clientTimeout
		clientStart := time.Now()

		// deadline should cause the client end of the connection to close
		// well before the max request time configured above
		conn.SetDeadline(time.Now().Add(clientTimeout))

		// first, we write the request line and headers, which should cause the
		// server to respond with a 101 Switching Protocols response.
		{
			n, err := conn.Write(reqBytes)
			assert.NilError(t, err)
			assert.Equal(t, n, len(reqBytes), "incorrect number of bytes written")

			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			assert.NilError(t, err)
			assert.StatusCode(t, resp, http.StatusSwitchingProtocols)
		}

		// next, we try to read from the connection, expecting the connection
		// to be closed after roughly clientTimeout seconds.
		//
		// the server should detect the closed connection and abort the
		// handler, also after roughly clientTimeout seconds.
		{
			_, err := conn.Read(make([]byte, 1))
			elapsedClientTime = time.Since(clientStart)

			// close client connection, which should interrupt the server's
			// blocking read call on the connection
			conn.Close()

			assert.Equal(t, os.IsTimeout(err), true, "expected timeout error")
			assert.MinDuration(t, elapsedClientTime, clientTimeout)

			// wait for the server to finish
			wg.Wait()
			assert.MinDuration(t, elapsedServerTime, clientTimeout)
		}
	})

	t.Run("frame size limits are enforced before reading payload", func(t *testing.T) {
		t.Parallel()

		const (
			maxFragmentSize = 128
			maxMessageSize  = 256
		)

		// maskedFrame builds a complete client frame with a 16-bit payload
		// length and an all-zero masking key
		maskedFrame := func(b0 byte, payload []byte) []byte {
			frame := append([]byte{b0, 0x80 | 126}, binary.BigEndian.AppendUint16(nil, uint16(len(payload)))...)
			frame = append(frame, 0, 0, 0, 0)
			return append(frame, payload...)
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ws := websocket.New(w, r, websocket.Limits{
				MaxDuration:     time.Hour, // should never be reached
				MaxFragmentSize: maxFragmentSize,
				MaxMessageSize:  maxMessageSize,
			})
			if err := ws.Handshake(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			ws.Serve(websocket.EchoHandler)
		}))
		t.Cleanup(srv.Close)

		testCases := map[string]struct {
			// optional complete frames to send before header
			prelude []byte
			// frame header up to (but not including) the masking key; no
			// payload is ever sent, so the server must reject the frame based
			// on the header alone
			header    []byte
			wantCode  websocket.StatusCode
			wantError string
		}{
			"64-bit length exceeding max fragment size": {
				header:    append([]byte{0x82, 0x80 | 127}, binary.BigEndian.AppendUint64(nil, 1<<62)...),
				wantCode:  websocket.StatusTooLarge,
				wantError: fmt.Sprintf("frame too large: payload size %d exceeds maximum of %d bytes", uint64(1<<62), maxFragmentSize),
			},
			"64-bit length with most significant bit set": {
				header:    append([]byte{0x82, 0x80 | 127}, binary.BigEndian.AppendUint64(nil, 1<<63)...),
				wantCode:  websocket.StatusProtocolError,
				wantError: "invalid payload length: most significant bit of 64-bit length must be 0",
			},
			"16-bit length exceeding max fragment size": {
				header:    append([]byte{0x81, 0x80 | 126}, binary.BigEndian.AppendUint16(nil, maxFragmentSize+1)...),
				wantCode:  websocket.StatusTooLarge,
				wantError: fmt.Sprintf("frame too large: payload size %d exceeds maximum of %d bytes", maxFragmentSize+1, maxFragmentSize),
			},
			"control frame exceeding 125 bytes": {
				header:    append([]byte{0x89, 0x80 | 126}, binary.BigEndian.AppendUint16(nil, 126)...),
				wantCode:  websocket.StatusProtocolError,
				wantError: "invalid control frame: payload size 126 exceeds 125 bytes",
			},
			"continuation frame exceeding max message size": {
				// two max-size fragments fill the message to exactly the max
				// message size, so any continuation payload exceeds it
				prelude: append(
					maskedFrame(0x02, make([]byte, maxFragmentSize)),
					maskedFrame(0x00, make([]byte, maxFragmentSize))...,
				),
				header:    append([]byte{0x80, 0x80 | 126}, binary.BigEndian.AppendUint16(nil, 1)...),
				wantCode:  websocket.StatusTooLarge,
				wantError: fmt.Sprintf("message too large: size %d exceeds maximum of %d bytes", maxMessageSize+1, maxMessageSize),
			},
			"unmasked frame": {
				header:    []byte{0x81, 5},
				wantCode:  websocket.StatusProtocolError,
				wantError: "received unmasked client frame",
			},
		}
		for name, tc := range testCases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				conn, err := net.Dial("tcp", srv.Listener.Addr().String())
				assert.NilError(t, err)
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))

				reqParts := []string{
					"GET /websocket/echo HTTP/1.1",
					"Host: test",
					"Connection: upgrade",
					"Upgrade: websocket",
					"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==",
					"Sec-WebSocket-Version: 13",
				}
				_, err = conn.Write([]byte(strings.Join(reqParts, "\r\n") + "\r\n\r\n"))
				assert.NilError(t, err)

				br := bufio.NewReader(conn)
				resp, err := http.ReadResponse(br, nil)
				assert.NilError(t, err)
				assert.StatusCode(t, resp, http.StatusSwitchingProtocols)

				_, err = conn.Write(append(tc.prelude, tc.header...))
				assert.NilError(t, err)

				// manually read and minimally parse header and payload
				// because current websocket API does not expose this
				// functionality in a way we can use it here.
				hdr := make([]byte, 2)
				_, err = io.ReadFull(br, hdr)
				assert.NilError(t, err)
				assert.Equal(t, hdr[0], byte(0x88), "expected close frame")

				payload := make([]byte, hdr[1])
				_, err = io.ReadFull(br, payload)
				assert.NilError(t, err)
				assert.Equal(t, websocket.StatusCode(binary.BigEndian.Uint16(payload[:2])), tc.wantCode, "incorrect close status code")
				assert.Equal(t, string(payload[2:]), tc.wantError, "incorrect close reason")
			})
		}
	})
}

// brokenHijackResponseWriter implements just enough to satisfy the
// http.ResponseWriter and http.Hijacker interfaces and get through the
// handshake before failing to actually hijack the connection.
type brokenHijackResponseWriter struct {
	http.ResponseWriter
	Code int
}

func (w *brokenHijackResponseWriter) WriteHeader(code int) {
	w.Code = code
}

func (w *brokenHijackResponseWriter) Header() http.Header {
	return http.Header{}
}

func (brokenHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("error hijacking connection")
}

var (
	_ http.ResponseWriter = &brokenHijackResponseWriter{}
	_ http.Hijacker       = &brokenHijackResponseWriter{}
)
