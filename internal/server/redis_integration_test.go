// Integration-style tests over TCP using raw RESP (P4.3): no redis-cli / go-redis dependency.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
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

func readLineCRLF(t *testing.T, c net.Conn, br *bufio.Reader) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	s, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(s, "\r\n")
}

// TestIntegrationAdvertisedCommands exercises a subset of the advertised Redis-style command set over TCP.
func TestIntegrationAdvertisedCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

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

	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	var cconn net.Conn
	for i := 0; i < 100; i++ {
		cconn, err = net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(cconn)

	mustSend := func(s string) {
		t.Helper()
		if _, err := cconn.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	// PING
	mustSend("*1\r\n$4\r\nPING\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+PONG" {
		t.Fatalf("PING: got %q want +PONG", s)
	}

	// SET k v
	mustSend("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+OK" {
		t.Fatalf("SET: got %q want +OK", s)
	}

	// GET k → bulk string
	mustSend("*2\r\n$3\r\nGET\r\n$1\r\nk\r\n")
	if s := readLineCRLF(t, cconn, br); s != "$1" {
		t.Fatalf("GET len: got %q want $1", s)
	}
	if s := readLineCRLF(t, cconn, br); s != "v" {
		t.Fatalf("GET val: got %q want v", s)
	}

	// FLUSHALL (single-DB: clears keyspace; same effect as FLUSHDB locally)
	mustSend("*1\r\n$8\r\nFLUSHALL\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+OK" {
		t.Fatalf("FLUSHALL: got %q want +OK", s)
	}

	// GET k → null bulk
	mustSend("*2\r\n$3\r\nGET\r\n$1\r\nk\r\n")
	if s := readLineCRLF(t, cconn, br); s != "$-1" {
		t.Fatalf("GET after flush: got %q want $-1", s)
	}

	// FLUSHDB (same local effect as FLUSHALL in single-DB mode; bulk length is 7)
	mustSend("*1\r\n$7\r\nFLUSHDB\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+OK" {
		t.Fatalf("FLUSHDB: got %q want +OK", s)
	}

	mustSend("*3\r\n$3\r\nSET\r\n$1\r\nx\r\n$1\r\ny\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+OK" {
		t.Fatalf("SET x: got %q want +OK", s)
	}
	mustSend("*1\r\n$7\r\nFLUSHDB\r\n")
	if s := readLineCRLF(t, cconn, br); s != "+OK" {
		t.Fatalf("FLUSHDB 2: got %q want +OK", s)
	}
	mustSend("*2\r\n$3\r\nGET\r\n$1\r\nx\r\n")
	if s := readLineCRLF(t, cconn, br); s != "$-1" {
		t.Fatalf("GET x after flushdb: got %q want $-1", s)
	}

	// Wrong arity (Redis-style ERR) — FLUSHALL with extra token
	mustSend("*2\r\n$8\r\nFLUSHALL\r\n$5\r\nASYNC\r\n")
	if s := readLineCRLF(t, cconn, br); !strings.HasPrefix(s, "-ERR") {
		t.Fatalf("FLUSHALL extra arg: got %q want -ERR...", s)
	}

	_ = cconn.Close()
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not exit")
	}
}
