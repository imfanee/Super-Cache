// Integration-style tests for the client idle-connection timeout (regression cover for
// the 2026-07-23 wedge: dead client sockets pinning handler goroutines and fds forever).
package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

func startIdleTestServer(t *testing.T, idleSeconds int) (addr string, cancel context.CancelFunc) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()

	c := &config.Config{
		SharedSecret:      strings.Repeat("a", 32),
		ClientPort:        port,
		PeerPort:          peerPort,
		ClientIdleTimeout: idleSeconds,
	}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancelCtx := context.WithCancel(context.Background())
	go func() { _ = srv.Run(ctx) }()
	return net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)), cancelCtx
}

func dialWithRetry(t *testing.T, addr string) net.Conn {
	t.Helper()
	var conn net.Conn
	var err error
	for i := 0; i < 100; i++ {
		conn, err = net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			return conn
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil
}

// TestClientIdleTimeoutReapsIdleConns verifies a silent connection is closed by the
// server after client_idle_timeout, while an active connection survives.
func TestClientIdleTimeoutReapsIdleConns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	addr, cancel := startIdleTestServer(t, 1)
	defer cancel()

	idle := dialWithRetry(t, addr)
	defer idle.Close()
	active := dialWithRetry(t, addr)
	defer active.Close()
	idleBR := bufio.NewReader(idle)
	activeBR := bufio.NewReader(active)

	// Both connections work initially.
	for _, tc := range []struct {
		c  net.Conn
		br *bufio.Reader
	}{{idle, idleBR}, {active, activeBR}} {
		if _, err := tc.c.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
			t.Fatal(err)
		}
		if s := readLineCRLF(t, tc.c, tc.br); s != "+PONG" {
			t.Fatalf("PING: got %q want +PONG", s)
		}
	}

	// Keep "active" busy past the idle window; "idle" stays silent.
	deadline := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := active.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
			t.Fatalf("active conn write failed: %v", err)
		}
		if s := readLineCRLF(t, active, activeBR); s != "+PONG" {
			t.Fatalf("active PING: got %q want +PONG", s)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// The idle connection must have been closed by the server.
	_ = idle.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := idleBR.ReadByte(); err == nil {
		t.Fatal("idle conn still open: expected server-side close")
	}

	// The active connection must still work.
	if _, err := active.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatalf("active conn write after idle window: %v", err)
	}
	if s := readLineCRLF(t, active, activeBR); s != "+PONG" {
		t.Fatalf("active PING after idle window: got %q want +PONG", s)
	}
}

// TestClientIdleTimeoutExemptsSubscribers verifies subscribe-mode connections are not
// reaped while idle (subscribers are legitimately quiet).
func TestClientIdleTimeoutExemptsSubscribers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	addr, cancel := startIdleTestServer(t, 1)
	defer cancel()

	sub := dialWithRetry(t, addr)
	defer sub.Close()
	subBR := bufio.NewReader(sub)

	if _, err := sub.Write([]byte("*2\r\n$9\r\nSUBSCRIBE\r\n$2\r\nch\r\n")); err != nil {
		t.Fatal(err)
	}
	// subscribe ack: *3, $9, subscribe, $2, ch, :1
	for i := 0; i < 6; i++ {
		readLineCRLF(t, sub, subBR)
	}

	time.Sleep(2500 * time.Millisecond)

	// Publish from a second connection; the subscriber must still be alive to get it.
	pub := dialWithRetry(t, addr)
	defer pub.Close()
	pubBR := bufio.NewReader(pub)
	if _, err := pub.Write([]byte("*3\r\n$7\r\nPUBLISH\r\n$2\r\nch\r\n$2\r\nhi\r\n")); err != nil {
		t.Fatal(err)
	}
	if s := readLineCRLF(t, pub, pubBR); s != ":1" {
		t.Fatalf("PUBLISH receivers: got %q want :1 (subscriber was reaped?)", s)
	}
	if s := readLineCRLF(t, sub, subBR); s != "*3" {
		t.Fatalf("subscriber push: got %q want *3", s)
	}
}
