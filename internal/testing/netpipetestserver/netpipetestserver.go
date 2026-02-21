// Package netpipetestserver provides [httptest.Server] and [http.Client]
// pairs configured to work within a synctest bubble by swapping the network
// for a pair of in-memory [net.Pipe] connections.
package netpipetestserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// New creates a new httptest.Server and http.Client pair suitable for use
// within a synctest bubble, which can't span real network connections. The
// server and client communicate over a pair of in-memory [net.Pipe]
// connections.
func New(t *testing.T, handler http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()

	ln := newNetPipeListener()
	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Transport.(*http.Transport).DialContext = ln.DialContext

	return srv, client
}

// netPipeListener is used to enable a server to accept connections from
// clients via [net.Pipe]. Client transports must use the server listener's
// DialContext method to make connections.
type netPipeListener struct {
	connCh chan net.Conn
	done   chan struct{}
	addr   netPipeAddr
}

func newNetPipeListener() *netPipeListener {
	return &netPipeListener{
		connCh: make(chan net.Conn),
		done:   make(chan struct{}),
	}
}

// Accept accepts connections via [net.Pipe] from clients using the listener's
// own [DialContext] method.
func (ln *netPipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-ln.connCh:
		return conn, nil
	case <-ln.done:
		return nil, net.ErrClosed
	}
}

// Close closes the listener.
func (ln *netPipeListener) Close() error {
	select {
	case <-ln.done:
	default:
		close(ln.done)
	}
	return nil
}

// Dial allows tests using netPipeListener to directly access the underlying
// client connection via the [http.Client]'s transport. The client must be
// one created by [New].
func Dial(t testing.TB, client *http.Client) (net.Conn, error) {
	t.Helper()
	addr := netPipeAddr{}
	return client.Transport.(*http.Transport).DialContext(t.Context(), addr.Network(), addr.String())
}

// Addr returns a dummy [net.Addr] implementation. To actually connect to a
// listener, the listener's own [DialContext] method must be used (e.g. in
// the client's [http.Transport]).
func (ln *netPipeListener) Addr() net.Addr {
	return ln.addr
}

// DialContext creates both client and server conns via [net.Pipe] and
// returns the client conn. The server conn is enqueued for the listener to
// pick up in its [Accept] method.
func (ln *netPipeListener) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	clientConn, serverConn := net.Pipe()
	select {
	case ln.connCh <- serverConn:
		return clientConn, nil
	case <-ln.done:
		clientConn.Close()
		serverConn.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		clientConn.Close()
		serverConn.Close()
		return nil, ctx.Err()
	}
}

type netPipeAddr struct{}

func (netPipeAddr) Network() string { return "tcp" }
func (netPipeAddr) String() string  { return "netpipetestserver:0" }

var _ net.Addr = netPipeAddr{}
