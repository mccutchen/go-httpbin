// package webocket_test allows us to test the package via the concrete
// implementation in httpbin's /websocket/echo handler, ensuring that
//
// a) the httpbin handler works as expected and
//
// b) we still get code coverage for the websocket package without duplicating
// tests.
package websocket_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mccutchen/go-httpbin/v2/httpbin"
	"github.com/mccutchen/go-httpbin/v2/httpbin/websocket"
	"github.com/mccutchen/go-httpbin/v2/internal/testing/assert"
)

func TestHandshake(t *testing.T) {
	app := httpbin.New()
	srv := httptest.NewServer(app)
	defer srv.Close()

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
		"missing Connection header": {
			reqHeaders: map[string]string{
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
		},
		"incorrect Connection header": {
			reqHeaders: map[string]string{
				"Connection":            "close",
				"Upgrade":               "websocket",
				"Sec-WebSocket-Key":     "dGhlIHNhbXBsZSBub25jZQ==",
				"Sec-WebSocket-Version": "13",
			},
			wantStatus: http.StatusBadRequest,
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
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/websocket/echo", nil)
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

var _ http.ResponseWriter = &brokenHijackResponseWriter{}
