package websocket_test

import (
	"bufio"
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
		tc := tc
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

		maxDuration := 500 * time.Millisecond

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
			start := time.Now()
			_, err := conn.Read(make([]byte, 1))
			elapsed := time.Since(start)

			assert.Error(t, err, io.EOF)
			assert.RoughDuration(t, elapsed, maxDuration, 25*time.Millisecond)
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
		)

		wg.Add(1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer wg.Done()
			start := time.Now()
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
			elapsedServerTime = time.Since(start)
		}))
		defer srv.Close()

		conn, err := net.Dial("tcp", srv.Listener.Addr().String())
		assert.NilError(t, err)
		defer conn.Close()

		// should cause the client end of the connection to close well before
		// the max request time configured above
		conn.SetDeadline(time.Now().Add(clientTimeout))

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
			start := time.Now()
			_, err := conn.Read(make([]byte, 1))
			elapsedClientTime = time.Since(start)

			// close client connection, which should interrupt the server's
			// blocking read call on the connection
			conn.Close()

			assert.Equal(t, os.IsTimeout(err), true, "expected timeout error")
			assert.RoughDuration(t, elapsedClientTime, clientTimeout, 10*time.Millisecond)

			// wait for the server to finish
			wg.Wait()
			assert.RoughDuration(t, elapsedServerTime, clientTimeout, 10*time.Millisecond)
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
