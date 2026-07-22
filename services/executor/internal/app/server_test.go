package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const testTimeout = 5 * time.Second

func TestNewServerConfiguresConnectionBoundaries(t *testing.T) {
	t.Parallel()

	readHeaderTimeout := 10 * time.Second
	idleTimeout := 60 * time.Second
	server, err := NewServer(http.NewServeMux(), readHeaderTimeout, idleTimeout)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if server.Handler == nil {
		t.Error("NewServer().Handler = nil")
	}
	if server.ReadHeaderTimeout != readHeaderTimeout {
		t.Errorf("NewServer().ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, readHeaderTimeout)
	}
	if server.IdleTimeout != idleTimeout {
		t.Errorf("NewServer().IdleTimeout = %v, want %v", server.IdleTimeout, idleTimeout)
	}
	if server.ReadTimeout != 0 {
		t.Errorf("NewServer().ReadTimeout = %v, want 0", server.ReadTimeout)
	}
	if server.WriteTimeout != 0 {
		t.Errorf("NewServer().WriteTimeout = %v, want 0", server.WriteTimeout)
	}
}

func TestNewServerRejectsNilHandler(t *testing.T) {
	t.Parallel()

	if _, err := NewServer(nil, time.Second, 2*time.Second); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("NewServer(nil) error = %v, want ErrNilHandler", err)
	}
}

// nilPointerHandler and nilFuncHandler exercise the typed-nil interface pitfall:
// each is a non-nil http.Handler wrapping a nil concrete value, so the plain
// `handler == nil` comparison is false. NewServer must still reject them and
// never hand a panic-on-dispatch handler to http.Server.
type nilPointerHandler struct{}

func (*nilPointerHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

func TestNewServerRejectsTypedNilHandler(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		handler http.Handler
	}{
		{"nil pointer", (*nilPointerHandler)(nil)},
		{"nil func", http.HandlerFunc(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.handler == nil {
				t.Fatal("setup error: handler is untyped nil, not typed-nil")
			}
			_, err := NewServer(tc.handler, time.Second, 2*time.Second)
			if !errors.Is(err, ErrNilHandler) {
				t.Fatalf("NewServer(%s) error = %v, want ErrNilHandler", tc.name, err)
			}
		})
	}
}

func TestNewServerAcceptsTypedHandler(t *testing.T) {
	t.Parallel()

	// A non-nil pointer handler and a non-nil func handler must both be
	// accepted, proving the typed-nil guard does not over-reject.
	cases := []struct {
		name    string
		handler http.Handler
	}{
		{"pointer", &nilPointerHandler{}},
		{"func", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})},
		{"mux", http.NewServeMux()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server, err := NewServer(tc.handler, time.Second, 2*time.Second)
			if err != nil {
				t.Fatalf("NewServer(%s) error = %v", tc.name, err)
			}
			if server.Handler == nil {
				t.Error("NewServer().Handler = nil")
			}
		})
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	listener := newListener(t)
	server, _ := NewServer(http.NewServeMux(), time.Second, 2*time.Second)

	tests := []struct {
		name            string
		listener        net.Listener
		server          *http.Server
		shutdownTimeout time.Duration
		want            string
	}{
		{
			name:            "nil listener",
			server:          server,
			shutdownTimeout: time.Second,
			want:            "listener must not be nil",
		},
		{
			name:            "nil server",
			listener:        listener,
			shutdownTimeout: time.Second,
			want:            "server must not be nil",
		},
		{
			name:            "zero shutdown timeout",
			listener:        listener,
			server:          server,
			shutdownTimeout: 0,
			want:            "shutdown timeout must be positive",
		},
		{
			name:            "negative shutdown timeout",
			listener:        listener,
			server:          server,
			shutdownTimeout: -time.Second,
			want:            "shutdown timeout must be positive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Run(context.Background(), test.listener, test.server, test.shutdownTimeout)
			if err == nil || err.Error() != test.want {
				t.Fatalf("Run() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunWrapsUnexpectedServeError(t *testing.T) {
	serveErr := errors.New("listener failed")
	err := Run(context.Background(), failingListener{err: serveErr}, mustNewServer(t, http.NewServeMux(), time.Second, 2*time.Second), time.Second)
	if !errors.Is(err, serveErr) {
		t.Fatalf("Run() error = %v, want wrapped %v", err, serveErr)
	}
	if !strings.Contains(err.Error(), "serve HTTP server") {
		t.Fatalf("Run() error = %q, want serve context", err)
	}
}

func TestRunServesAndGracefullyShutsDown(t *testing.T) {
	listener := newListener(t)
	server := mustNewServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), time.Second, 2*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runServer(ctx, listener, server, time.Second)

	response, err := (&http.Client{Timeout: testTimeout}).Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	cancel()
	if err := receive(t, done, "Run"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunTreatsErrServerClosedAsNormal(t *testing.T) {
	listener := newListener(t)
	server := mustNewServer(t, http.NewServeMux(), time.Second, 2*time.Second)
	done := runServer(context.Background(), listener, server, time.Second)

	if err := server.Close(); err != nil {
		t.Fatalf("Server.Close() error = %v", err)
	}
	if err := receive(t, done, "Run"); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

func TestRunShutdownRejectsNewConnectionsAndWaitsForInFlightRequest(t *testing.T) {
	listener := &closeObservingListener{Listener: newListener(t), closed: make(chan struct{})}
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRequest()

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(started) })
		<-release
		w.WriteHeader(http.StatusNoContent)
	})}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runServer(ctx, listener, server, time.Second)

	requestDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: testTimeout}).Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()

	receive(t, started, "in-flight request start")
	cancel()
	receive(t, listener.closed, "listener close during shutdown")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), testTimeout)
	defer dialCancel()
	connection, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", listener.Addr().String())
	if err == nil {
		connection.Close()
		t.Fatal("new connection succeeded after shutdown began")
	}

	select {
	case err := <-done:
		t.Fatalf("Run() returned before in-flight request completed: %v", err)
	default:
	}

	releaseRequest()
	if err := receive(t, requestDone, "in-flight request"); err != nil {
		t.Fatalf("in-flight request: %v", err)
	}
	if err := receive(t, done, "Run"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunReturnsShutdownDeadlineAndCanBeCleanedUp(t *testing.T) {
	listener := newListener(t)
	started := make(chan struct{})
	release := make(chan struct{})
	serverDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRequest()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		close(serverDone)
	})}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runServer(ctx, listener, server, time.Nanosecond)
	requestDone := make(chan error, 1)
	go func() {
		response, err := (&http.Client{Timeout: testTimeout}).Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		requestDone <- err
	}()
	receive(t, started, "request start")
	cancel()
	if err := receive(t, done, "Run"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline exceeded", err)
	}
	releaseRequest()
	receive(t, serverDone, "server handler completion")
	if err := receive(t, requestDone, "request"); err != nil {
		t.Fatalf("request: %v", err)
	}
}

func runServer(ctx context.Context, listener net.Listener, server *http.Server, shutdownTimeout time.Duration) <-chan error {
	done := make(chan error, 1)
	go func() { done <- Run(ctx, listener, server, shutdownTimeout) }()
	return done
}

func receive[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(testTimeout):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func mustNewServer(t *testing.T, handler http.Handler, readHeaderTimeout, idleTimeout time.Duration) *http.Server {
	t.Helper()
	server, err := NewServer(handler, readHeaderTimeout, idleTimeout)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}

func newListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen(): %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

type failingListener struct {
	err error
}

func (listener failingListener) Accept() (net.Conn, error) { return nil, listener.err }
func (failingListener) Close() error                       { return nil }
func (failingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

type closeObservingListener struct {
	net.Listener
	closed chan struct{}
	once   sync.Once
}

func (listener *closeObservingListener) Close() error {
	err := listener.Listener.Close()
	listener.once.Do(func() { close(listener.closed) })
	return err
}
