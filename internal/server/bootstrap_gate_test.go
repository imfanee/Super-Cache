// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests that a node which knows a peer never serves before it has synced.
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

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func TestBootstrapSourcesIncludesPeersWithoutBootstrapPeer(t *testing.T) {
	s := &Server{}
	cfg := &config.Config{Peers: []string{"10.0.0.1:7379", "10.0.0.2:7379"}}
	got := s.bootstrapSources(cfg)
	if len(got) != 2 || got[0] != "10.0.0.1:7379" || got[1] != "10.0.0.2:7379" {
		t.Fatalf("peers must be snapshot sources even with no bootstrap_peer, got %v", got)
	}
}

func TestBootstrapSourcesPrefersBootstrapPeer(t *testing.T) {
	s := &Server{}
	cfg := &config.Config{
		BootstrapPeer: "10.0.0.9:7379",
		Peers:         []string{"10.0.0.1:7379", "10.0.0.9:7379"},
	}
	got := s.bootstrapSources(cfg)
	if len(got) != 2 || got[0] != "10.0.0.9:7379" {
		t.Fatalf("explicit bootstrap_peer must come first and not repeat, got %v", got)
	}
}

func TestBootstrapSourcesIncludesSeeds(t *testing.T) {
	s := &Server{peerSeeds: []string{"10.0.0.5:7379"}}
	cfg := &config.Config{Peers: []string{"10.0.0.1:7379"}}
	got := s.bootstrapSources(cfg)
	if len(got) != 2 || got[1] != "10.0.0.5:7379" {
		t.Fatalf("a seed from a copied configuration must be a snapshot source, got %v", got)
	}
}

func TestBootstrapSourcesDeduplicatesSeed(t *testing.T) {
	s := &Server{peerSeeds: []string{"10.0.0.1:7379"}}
	cfg := &config.Config{Peers: []string{"10.0.0.1:7379"}}
	if got := s.bootstrapSources(cfg); len(got) != 1 {
		t.Fatalf("expected the duplicate seed to be dropped, got %v", got)
	}
}

func TestBootstrapSourcesEmptyWhenNothingKnown(t *testing.T) {
	s := &Server{}
	if got := s.bootstrapSources(&config.Config{}); len(got) != 0 {
		t.Fatalf("a node that knows no peer has nowhere to sync from, got %v", got)
	}
}

// TestNodeWithUnreachablePeerRefusesCommands is the behaviour this change exists for. A node
// configured with a peer it cannot reach must not answer as though its empty store were the
// cluster's data. It stays up and reports LOADING so the condition is visible and recoverable.
func TestNodeWithUnreachablePeerRefusesCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	port, peerPort := freePort(t), freePort(t)
	// Port 1 is never a Super-Cache peer, so bootstrap can never succeed here.
	c := &config.Config{
		SharedSecret: strings.Repeat("a", 32),
		ClientPort:   port,
		PeerPort:     peerPort,
		Peers:        []string{"127.0.0.1:1"},
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
		t.Fatalf("an unsynced node must refuse commands with LOADING, got %q", line)
	}
	if srv.stats.BootstrapState() != "syncing" {
		t.Fatalf("bootstrap_state should report syncing, got %q", srv.stats.BootstrapState())
	}
}

// TestStandaloneNodeServesImmediately guards the other half: a node that knows no peer is the
// whole cluster as far as it can tell, and must not be held back by the gate above.
func TestStandaloneNodeServesImmediately(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	port, peerPort := freePort(t), freePort(t)
	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
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
	if !strings.HasPrefix(line, "+PONG") {
		t.Fatalf("a standalone node should serve immediately, got %q", line)
	}
}

// TestFoundClusterServesWhenNoPeerReachable covers launching the first node of a new cluster
// from a snapshot: its configuration names the old cluster's peers, none of which it can reach,
// and without this it would refuse commands indefinitely.
func TestFoundClusterServesWhenNoPeerReachable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	port, peerPort := freePort(t), freePort(t)
	c := &config.Config{
		SharedSecret: strings.Repeat("a", 32),
		ClientPort:   port,
		PeerPort:     peerPort,
		// Reserved for documentation, so nothing can answer.
		Peers: []string{"203.0.113.7:7379", "203.0.113.8:7379"},
	}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetFoundCluster(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	conn := dialWithRetry(t, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	defer conn.Close()
	br := bufio.NewReader(conn)

	// The founding decision follows one failed attempt, which waits for a peer link.
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read reply: %v", err)
		}
		if strings.HasPrefix(line, "+PONG") {
			if srv.stats.BootstrapState() != "standalone" {
				t.Fatalf("expected standalone, got %q", srv.stats.BootstrapState())
			}
			return
		}
		if !strings.HasPrefix(line, "-LOADING") {
			t.Fatalf("unexpected reply %q", line)
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("a node launched to found a cluster never began serving")
}

// TestFoundClusterStillWaitsWhenAPeerAnswers is the safety half. The flag must not become a way
// to serve an empty store while the cluster is reachable, which would look like data loss to
// every client of this node.
func TestFoundClusterStillWaitsWhenAPeerAnswers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	// A listener that accepts but never completes a handshake: reachable, so a link forms at the
	// TCP level, yet no snapshot can be pulled from it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c
		}
	}()

	port, peerPort := freePort(t), freePort(t)
	c := &config.Config{
		SharedSecret: strings.Repeat("a", 32),
		ClientPort:   port,
		PeerPort:     peerPort,
		Peers:        []string{ln.Addr().String()},
	}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	srv.SetFoundCluster(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	conn := dialWithRetry(t, net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
	defer conn.Close()
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !strings.HasPrefix(line, "-LOADING") {
		t.Fatalf("must keep waiting while a peer is reachable, got %q", line)
	}
}

// TestWithoutFoundClusterNeverServesUnsynced confirms the default is unchanged.
func TestWithoutFoundClusterNeverServesUnsynced(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}
	port, peerPort := freePort(t), freePort(t)
	c := &config.Config{
		SharedSecret: strings.Repeat("a", 32),
		ClientPort:   port,
		PeerPort:     peerPort,
		Peers:        []string{"203.0.113.9:7379"},
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
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if !strings.HasPrefix(line, "-LOADING") {
		t.Fatalf("without the flag an unsynced node must refuse commands, got %q", line)
	}
}
