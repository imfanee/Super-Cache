// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for periodic peer discovery and the standalone decision that depends on it.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/discovery"
)

func TestDiscoveryProvidersDisabledWithoutToken(t *testing.T) {
	t.Setenv(hcloudTokenEnv, "")
	if got := discoveryProviders(&config.Config{PeerPort: 7379}); len(got) != 0 {
		t.Fatalf("discovery must stay off without a token, got %d providers", len(got))
	}
}

func TestDiscoveryProvidersFromConfigToken(t *testing.T) {
	t.Setenv(hcloudTokenEnv, "")
	got := discoveryProviders(&config.Config{PeerPort: 7379, HetznerAPIToken: "tok", HetznerLabelSelector: "role=sc"})
	if len(got) != 1 || got[0].Name() != "hetzner" {
		t.Fatalf("expected the hetzner provider, got %v", got)
	}
}

// TestDiscoveryProvidersFromEnvironment covers keeping the token out of a machine image, which
// matters because that image is copied to every autoscaled node.
func TestDiscoveryProvidersFromEnvironment(t *testing.T) {
	t.Setenv(hcloudTokenEnv, "from-env")
	got := discoveryProviders(&config.Config{PeerPort: 7379})
	if len(got) != 1 {
		t.Fatalf("expected the environment token to enable discovery, got %d providers", len(got))
	}
}

func TestDiscoveryProvidersNilConfig(t *testing.T) {
	if got := discoveryProviders(nil); got != nil {
		t.Fatal("expected no providers for a nil config")
	}
}

// stubProvider returns a fixed answer and counts how often it was asked.
type stubProvider struct {
	addrs []string
	err   error
	calls atomic.Int64
}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) Peers(context.Context) ([]string, error) {
	s.calls.Add(1)
	return s.addrs, s.err
}

func newDiscoveryServer(t *testing.T) *Server {
	t.Helper()
	c := &config.Config{
		SharedSecret: strings.Repeat("a", 32),
		ClientPort:   freePort(t),
		PeerPort:     freePort(t),
	}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestDiscoveredPeersAreAdded is the core of the feature: an address this node was never
// configured with becomes a peer.
func TestDiscoveredPeersAreAdded(t *testing.T) {
	srv := newDiscoveryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitPeerReady(t, srv)

	p := &stubProvider{addrs: []string{"203.0.113.21:7379", "203.0.113.22:7379"}}
	srv.discoverOnce(ctx, []discovery.Provider{p})

	known := srv.peer.ConfigPeerAddrs()
	if len(known) != 2 {
		t.Fatalf("expected both discovered addresses to become peers, got %v", known)
	}
	if !srv.discoveryListed.Load() {
		t.Fatal("a successful listing must be recorded")
	}
}

// TestDiscoveryFailureIsNotFatal covers the provider being unreachable, which must leave the
// node running on its configured peers rather than taking it down.
func TestDiscoveryFailureIsNotFatal(t *testing.T) {
	srv := newDiscoveryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitPeerReady(t, srv)

	p := &stubProvider{err: fmt.Errorf("api down")}
	srv.discoverOnce(ctx, []discovery.Provider{p})

	if srv.discoveryListed.Load() {
		t.Fatal("a failed listing must not count as an answer, or the node could wrongly decide it is alone")
	}
}

// TestDiscoveryRepeatsOnInterval is the periodic half. A node whose known addresses have all
// been replaced only recovers because the listing runs again.
func TestDiscoveryRepeatsOnInterval(t *testing.T) {
	srv := newDiscoveryServer(t)
	cfg := *srv.config()
	cfg.DiscoveryInterval = 1
	srv.cfg.Store(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	waitPeerReady(t, srv)

	p := &stubProvider{addrs: []string{"203.0.113.31:7379"}}
	go srv.runPeerDiscovery(ctx, []discovery.Provider{p})

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if p.calls.Load() >= 2 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected discovery to run repeatedly, ran %d times", p.calls.Load())
}

// TestNodeWithDiscoveryWaitsBeforeServing is the safety property the provider introduces. A node
// with no configured peers but with discovery enabled might be joining an established cluster,
// so it must not answer from an empty store until discovery has said there is nobody.
func TestNodeWithDiscoveryWaitsBeforeServing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	// An API that never answers, so discovery cannot conclude anything.
	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Second)
	}))
	defer stuck.Close()

	port := freePort(t)
	c := &config.Config{
		SharedSecret:    strings.Repeat("a", 32),
		ClientPort:      port,
		PeerPort:        freePort(t),
		HetznerAPIToken: "tok",
	}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	conn := dialWithRetry(t, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	defer conn.Close()
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !strings.HasPrefix(line, "-LOADING") {
		t.Fatalf("a node still waiting on discovery must not serve, got %q", line)
	}
}

func waitPeerReady(t *testing.T, srv *Server) {
	t.Helper()
	select {
	case <-srv.peer.ListenReady():
	case <-time.After(3 * time.Second):
		t.Fatal("peer listener not ready")
	}
}

// TestResyncDisabledDoesNothing covers the opt-out for operators who would rather stay diverged
// and visible than refuse commands while refetching.
func TestResyncDisabledDoesNothing(t *testing.T) {
	srv := newDiscoveryServer(t)
	off := false
	cfg := *srv.config()
	cfg.ResyncOnGap = &off
	srv.cfg.Store(&cfg)
	srv.clientReady.Store(true)

	srv.RequestResync("test")
	if !srv.clientReady.Load() {
		t.Fatal("a disabled resync must not take the node out of service")
	}
	if srv.stats.Resyncs() != 0 {
		t.Fatal("a disabled resync must not be counted")
	}
}

// TestResyncRateLimited is what bounds the cost of a fault that keeps producing loss: without a
// floor between attempts such a node would refetch continuously and never serve.
func TestResyncRateLimited(t *testing.T) {
	srv := newDiscoveryServer(t)
	cfg := *srv.config()
	cfg.ResyncMinInterval = 3600
	srv.cfg.Store(&cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.setRunContext(ctx)

	// A resync that ran a moment ago.
	srv.lastResync.Store(time.Now().UnixMilli())
	srv.clientReady.Store(true)
	srv.RequestResync("test")

	if !srv.clientReady.Load() {
		t.Fatal("a rate-limited resync must leave the node serving")
	}
	if srv.stats.Resyncs() != 0 {
		t.Fatal("a rate-limited resync must not be counted")
	}
	if srv.resyncing.Load() {
		t.Fatal("the guard must be released when the request is refused")
	}
}

// TestResyncSkippedWhileAlreadyRunning keeps overlapping refetches from stacking up.
func TestResyncSkippedWhileAlreadyRunning(t *testing.T) {
	srv := newDiscoveryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.setRunContext(ctx)
	srv.resyncing.Store(true) // one already in flight
	srv.clientReady.Store(true)

	srv.RequestResync("test")
	if !srv.clientReady.Load() {
		t.Fatal("a second resync must not disturb the node while one is running")
	}
	if srv.stats.Resyncs() != 0 {
		t.Fatal("an overlapping resync must not be counted")
	}
}

// TestResyncTakesNodeOutOfService is the intended behaviour when loss really is confirmed: the
// node stops answering rather than serving data it knows is wrong.
func TestResyncTakesNodeOutOfService(t *testing.T) {
	srv := newDiscoveryServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.setRunContext(ctx)
	srv.clientReady.Store(true)

	srv.RequestResync("confirmed loss")
	if srv.clientReady.Load() {
		t.Fatal("a confirmed loss must stop the node answering until it has resynced")
	}
	if srv.stats.Resyncs() != 1 {
		t.Fatalf("expected the resync to be counted once, got %d", srv.stats.Resyncs())
	}
	if srv.stats.BootstrapState() != "syncing" {
		t.Fatalf("expected bootstrap_state syncing, got %q", srv.stats.BootstrapState())
	}
}

// TestResyncWithoutRunContextIsSafe covers a request arriving before or after the server ran.
func TestResyncWithoutRunContextIsSafe(t *testing.T) {
	srv := newDiscoveryServer(t)
	srv.clientReady.Store(true)
	srv.RequestResync("test")
	if !srv.clientReady.Load() {
		t.Fatal("a resync with nowhere to run must leave the node alone")
	}
	if srv.resyncing.Load() {
		t.Fatal("the guard must be released")
	}
}
