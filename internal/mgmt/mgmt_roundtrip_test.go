// Unix/TCP management round-trip and client helper coverage.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pickTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitDialUnix(t *testing.T, sock string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for i := 0; i < 150; i++ {
		c, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unix socket not accepting")
}

func waitDialTCP(t *testing.T, addr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for i := 0; i < 150; i++ {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("tcp not accepting:", addr)
}

func testSecret() string { return strings.Repeat("z", 32) }

func newTestFakeExt() *fakeExt {
	return &fakeExt{
		uptime:    3,
		clients:   2,
		peers:     1,
		mem:       100,
		keys:      10,
		bootstrap: "complete",
		reloadChanged: []string{"log_level"},
		peersList: []map[string]any{
			{"address": "p1:1", "state": "connected"},
		},
		debugKeys:       map[string]any{"sample": []any{"k1"}},
		bootstrapStatus: map[string]any{"state": "idle"},
	}
}

func TestMgmtUnixClientHelpersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mgmt.sock")
	sec := testSecret()
	f := newTestFakeExt()
	srv := New(sock, "", sec, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialUnix(t, sock)
	short := func() context.Context {
		c, done := context.WithTimeout(context.Background(), 800*time.Millisecond)
		t.Cleanup(done)
		return c
	}

	if err := ClientReloadConfig(short(), "unix", sock, sec); err != nil {
		t.Fatal(err)
	}
	st, err := ClientStatus(short(), "unix", sock, sec)
	if err != nil || st["bootstrap_state"] != "complete" || fmt.Sprint(st["uptime_sec"]) != "3" {
		t.Fatalf("status %+v err=%v", st, err)
	}
	peers, err := ClientPeersList(short(), "unix", sock, sec)
	if err != nil || len(peers) != 1 {
		t.Fatalf("peers %+v err=%v", peers, err)
	}
	if err := ClientPeersAdd(short(), "unix", sock, sec, "127.0.0.1:9999"); err != nil {
		t.Fatal(err)
	}
	if err := ClientPeersRemove(short(), "unix", sock, sec, "127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	dk, err := ClientDebugKeyspace(short(), "unix", sock, sec, 5)
	if err != nil || dk["sample"] == nil {
		t.Fatalf("debug %+v err=%v", dk, err)
	}
	bs, err := ClientBootstrapStatus(short(), "unix", sock, sec)
	if err != nil || bs["state"] != "idle" {
		t.Fatalf("bootstrap %+v err=%v", bs, err)
	}
	if err := ClientShutdown(short(), "unix", sock, sec, false); err != nil {
		t.Fatal(err)
	}
	if err := ClientShutdown(short(), "unix", sock, sec, true); err != nil {
		t.Fatal(err)
	}
	cancel()
}

func TestMgmtUnixUnknownCommandRaw(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mgmt.sock")
	sec := testSecret()
	f := newTestFakeExt()
	srv := New(sock, "", sec, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialUnix(t, sock)
	cctx, done := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer done()
	_, err := clientCallRaw(cctx, "unix", sock, sec, "NOT_A_REAL_CMD", nil)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unknown") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
	cancel()
}

func TestMgmtUnixMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mgmt.sock")
	sec := testSecret()
	srv := New(sock, "", sec, stubHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialUnix(t, sock)
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("{not-json\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var res MgmtResponse
	if err := json.Unmarshal(buf[:n], &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "json") {
		t.Fatalf("got %+v", res)
	}
	cancel()
}

func TestMgmtTCPRunAndClientCalls(t *testing.T) {
	addr := pickTCPAddr(t)
	sec := testSecret()
	f := newTestFakeExt()
	srv := New("", addr, sec, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialTCP(t, addr)
	cctx, done := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer done()

	out, err := ClientPing(cctx, "tcp", addr, sec)
	if err != nil || out != "PONG" {
		t.Fatalf("ping %q %v", out, err)
	}
	if err := ClientReloadConfig(cctx, "tcp", addr, sec); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientStatus(cctx, "tcp", addr, sec); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientPeersList(cctx, "tcp", addr, sec); err != nil {
		t.Fatal(err)
	}
	if err := ClientPeersAdd(cctx, "tcp", addr, sec, "h:1"); err != nil {
		t.Fatal(err)
	}
	if err := ClientPeersRemove(cctx, "tcp", addr, sec, "h:2"); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientDebugKeyspace(cctx, "tcp", addr, sec, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := ClientBootstrapStatus(cctx, "tcp", addr, sec); err != nil {
		t.Fatal(err)
	}
	if err := ClientShutdown(cctx, "tcp", addr, sec, true); err != nil {
		t.Fatal(err)
	}
	cancel()
}

func TestMgmtTCPUnknownCommand(t *testing.T) {
	addr := pickTCPAddr(t)
	sec := testSecret()
	srv := New("", addr, sec, newTestFakeExt())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialTCP(t, addr)
	cctx, done := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer done()
	_, err := clientCallRaw(cctx, "tcp", addr, sec, "XYZZY", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	cancel()
}

func TestMgmtTCPMalformedJSON(t *testing.T) {
	addr := pickTCPAddr(t)
	sec := testSecret()
	srv := New("", addr, sec, stubHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitDialTCP(t, addr)
	c2, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Write([]byte(`{"oops` + "\n")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = c2.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	n, err := c2.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var res MgmtResponse
	if err := json.Unmarshal(buf[:n], &res); err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, "json") {
		t.Fatalf("got %+v", res)
	}
	cancel()
}

func TestRunNoListeners(t *testing.T) {
	s := New("-", "", testSecret(), stubHandler{})
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no listeners") {
		t.Fatalf("got %v", err)
	}
}

func TestRunNilHandler(t *testing.T) {
	s := New(filepath.Join(t.TempDir(), "x.sock"), "", "sec", nil)
	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("got %v", err)
	}
}

func TestClientCallRawDialError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := clientCallRaw(ctx, "tcp", "127.0.0.1:1", testSecret(), "PING", nil)
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestClientCallRawBadResponseJSON(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "badresp.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		_, _ = readLine(br)
		_, _ = c.Write([]byte("not-json-at-all\n"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	_, err = clientCallRaw(ctx, "unix", sock, "", "PING", nil)
	if err == nil || !strings.Contains(err.Error(), "response json") {
		t.Fatalf("got %v", err)
	}
}
