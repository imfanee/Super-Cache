// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package mgmt serves the management API for Super-Cache (Unix socket and/or loopback TCP, FR-028).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
)

// Handler supplies read-only management operations (implemented by *server.Server).
type Handler interface {
	Ping() string
	Info() map[string]any
}

// Server listens on a Unix domain socket and/or TCP (loopback) and serves one NDJSON request per connection.
type Server struct {
	unixPath string
	tcpAddr  string
	secret   string
	handler  Handler
}

// New builds a management server. unixPath "" or "-" skips Unix; tcpAddr "" skips TCP; at least one must be non-empty (caller should ensure).
// secret must match the cluster shared_secret.
func New(unixPath, tcpAddr, secret string, h Handler) *Server {
	return &Server{unixPath: unixPath, tcpAddr: tcpAddr, secret: secret, handler: h}
}

// Run listens until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	if s.handler == nil {
		return fmt.Errorf("mgmt: invalid server")
	}
	skipUnix := strings.TrimSpace(s.unixPath) == "" || strings.TrimSpace(s.unixPath) == "-"
	if skipUnix && strings.TrimSpace(s.tcpAddr) == "" {
		return fmt.Errorf("mgmt: no listeners")
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	if !skipUnix {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- s.runUnix(ctx)
		}()
	}
	if strings.TrimSpace(s.tcpAddr) != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- s.runTCP(ctx)
		}()
	}
	go func() {
		wg.Wait()
		close(errCh)
	}()
	for err := range errCh {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return nil
}

func (s *Server) runUnix(ctx context.Context) error {
	path := strings.TrimSpace(s.unixPath)
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("mgmt unix listen %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o660)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("mgmt unix accept: %w", err)
		}
		go s.serveConn(conn)
	}
}

func (s *Server) runTCP(ctx context.Context) error {
	addr := strings.TrimSpace(s.tcpAddr)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mgmt tcp listen %s: %w", addr, err)
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("mgmt tcp accept: %w", err)
		}
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	line, err := readLine(br)
	if err != nil {
		return
	}
	var req MgmtRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = writeLine(c, MgmtResponse{OK: false, Error: "invalid json"})
		return
	}
	if !secretEqual(req.Secret, s.secret) {
		_ = writeLine(c, MgmtResponse{OK: false, Error: "bad auth"})
		return
	}
	ext, _ := s.handler.(ExtendedHandler)
	resp := DispatchMgmt(s.handler, ext, req)
	_ = writeLine(c, resp)
}

func secretEqual(a, b string) bool {
	aa, bb := []byte(a), []byte(b)
	if len(aa) != len(bb) {
		return false
	}
	return subtle.ConstantTimeCompare(aa, bb) == 1
}

// ClientPing sends a ping and returns the result string or an error.
// network is "unix" or "tcp"; address is a socket path or host:port.
func ClientPing(ctx context.Context, network, address, secret string) (string, error) {
	return clientCall(ctx, network, address, secret, "PING")
}

// ClientInfo requests the info map (decoded from JSON).
func ClientInfo(ctx context.Context, network, address, secret string) (map[string]any, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, "INFO", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode info: %w", err)
	}
	return m, nil
}

// ClientReloadConfig triggers the same reload path as SIGHUP.
func ClientReloadConfig(ctx context.Context, network, address, secret string) error {
	_, err := clientCallRaw(ctx, network, address, secret, "RELOAD_CONFIG", nil)
	return err
}

// ClientStatus returns the same structured view as the status CLI (uptime, peers, memory, db size, bootstrap).
func ClientStatus(ctx context.Context, network, address, secret string) (map[string]any, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, "STATUS", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode status: %w", err)
	}
	return m, nil
}

// ClientPeersList returns configured peers with connection state.
func ClientPeersList(ctx context.Context, network, address, secret string) ([]map[string]any, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, "PEERS_LIST", nil)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode peers list: %w", err)
	}
	return rows, nil
}

// ClientPeersAdd adds a runtime peer (mgmt PEERS_ADD).
func ClientPeersAdd(ctx context.Context, network, address, secret, addr string) error {
	_, err := clientCallRaw(ctx, network, address, secret, "PEERS_ADD", map[string]string{"addr": addr})
	return err
}

// ClientPeersRemove removes a runtime peer (mgmt PEERS_REMOVE).
func ClientPeersRemove(ctx context.Context, network, address, secret, addr string) error {
	_, err := clientCallRaw(ctx, network, address, secret, "PEERS_REMOVE", map[string]string{"addr": addr})
	return err
}

// ClientDebugKeyspace samples keys with TTL (args: count).
func ClientDebugKeyspace(ctx context.Context, network, address, secret string, count int) (map[string]any, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, "DEBUG_KEYSPACE", map[string]int{"count": count})
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode debug keyspace: %w", err)
	}
	return m, nil
}

// ClientBootstrapStatus returns bootstrap telemetry (P1.10).
func ClientBootstrapStatus(ctx context.Context, network, address, secret string) (map[string]any, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, "BOOTSTRAP_STATUS", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode bootstrap status: %w", err)
	}
	return m, nil
}

// ClientShutdown requests process shutdown (graceful waits for client drain up to the server timeout).
func ClientShutdown(ctx context.Context, network, address, secret string, graceful bool) error {
	_, err := clientCallRaw(ctx, network, address, secret, "SHUTDOWN", map[string]bool{"graceful": graceful})
	return err
}

func clientCall(ctx context.Context, network, address, secret, cmd string) (string, error) {
	raw, err := clientCallRaw(ctx, network, address, secret, cmd, nil)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("decode result: %w", err)
	}
	return s, nil
}

func clientCallRaw(ctx context.Context, network, address, secret, cmd string, args any) (json.RawMessage, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	req := MgmtRequest{Secret: secret, Cmd: strings.ToUpper(strings.TrimSpace(cmd))}
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		req.Args = b
	}
	if err := writeLine(c, req); err != nil {
		return nil, err
	}
	br := bufio.NewReader(c)
	line, err := readLine(br)
	if err != nil {
		return nil, err
	}
	var res MgmtResponse
	if err := json.Unmarshal(line, &res); err != nil {
		return nil, fmt.Errorf("response json: %w", err)
	}
	if !res.OK {
		if res.Error != "" {
			return nil, errors.New(res.Error)
		}
		return nil, errors.New("mgmt error")
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		return nil, err
	}
	return b, nil
}
