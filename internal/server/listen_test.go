package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/philsphicas/bgtask/internal/server"
)

// TestListen_ResolvesPort0 verifies port 0 resolves to a real, non-zero
// port without blocking -- Listen and Serve are separate calls precisely
// so a test (or the CLI's startup-line printer) can observe the resolved
// address before entering the blocking accept loop.
func TestListen_ResolvesPort0(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ln, err := srv.Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	if !strings.Contains(addr, "127.0.0.1:") {
		t.Fatalf("Addr = %q, want a 127.0.0.1:<port> address", addr)
	}
	if strings.HasSuffix(addr, ":0") {
		t.Fatalf("Addr = %q, want a resolved non-zero port", addr)
	}
}

// TestListenServeShutdown drives the full non-blocking lifecycle: Listen
// returns immediately, Serve is run in a goroutine, a real HTTP request
// against the resolved address succeeds, and Shutdown returns promptly
// without the test itself ever blocking on a fixed port.
func TestListenServeShutdown(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ln, err := srv.Listen("127.0.0.1", 0)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	url := "http://" + ln.Addr().String() + "/healthz"
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Serve returned an error after Shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of Shutdown")
	}
}

// TestHandler_DoesNotRequireListen verifies Handler() can be exercised
// (e.g. via httptest) without ever calling Listen/Serve, which is the
// pattern every other route test in this package relies on.
func TestHandler_DoesNotRequireListen(t *testing.T) {
	svc, _ := newTestService(t)
	srv, err := server.New(server.Options{Service: svc, Expose: []server.Exposure{server.ExposeREST}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.Handler() == nil {
		t.Fatal("Handler() returned nil")
	}
}
