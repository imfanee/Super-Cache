// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package server wires Super-Cache subsystems and exposes the root Server lifecycle.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/supercache/supercache/internal/client"
	"github.com/supercache/supercache/internal/commands"
	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/mgmt"
	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/store"
	"github.com/supercache/supercache/internal/tlsconfig"
)

// Server is the top-level Super-Cache process: TCP listener, store, commands, and metrics.
type Server struct {
	cfg        atomic.Pointer[config.Config]
	configPath string

	store    *store.Store
	registry *commands.Registry
	pubsub   *client.SubscriptionManager
	peer     *peer.Service
	stats    *stats

	listener  net.Listener
	clientTLS *tls.Config // non-nil when Redis client port uses TLS
	wg        sync.WaitGroup
	listenerMu            sync.Mutex
	listenerCloseRequested bool

	sessionSeq atomic.Int64
	activeConns sync.Map
	shuttingDown atomic.Bool

	cancelRun context.CancelFunc
	onReload  func(changed []string)

	runFinished chan struct{}
	runDoneOnce sync.Once

	clientReady atomic.Bool
}

// New constructs a Server from validated configuration (store, registry, pub/sub, metrics).
func New(cfg *config.Config) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	st, err := store.NewStore(cfg)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	nodeID := randomNodeID()
	stats := newStats(nodeID, cfg.ClientPort)
	ps := peer.NewService(cfg, st, stats, stats, nodeID)
	s := &Server{
		store:       st,
		registry:    commands.NewRegistry(),
		pubsub:      client.NewSubscriptionManager(),
		peer:        ps,
		stats:       stats,
		runFinished: make(chan struct{}),
	}
	s.cfg.Store(cfg)
	s.clientReady.Store(true)
	// Wire up PUBLISH replication: deliver to local subscribers.
	ps.SetOnPublish(func(channel string, message []byte) {
		s.pubsub.Publish(channel, message)
	})
	return s, nil
}

func (s *Server) config() *config.Config {
	return s.cfg.Load()
}

// SetConfigPath stores the TOML path used for SIGHUP hot reload.
func (s *Server) SetConfigPath(path string) {
	s.configPath = path
}

// ConfigPath returns the configured reload path (may be empty).
func (s *Server) ConfigPath() string {
	return s.configPath
}

// CurrentConfig returns the active configuration snapshot pointer.
func (s *Server) CurrentConfig() *config.Config {
	return s.cfg.Load()
}

// ApplyHotReload swaps in cfg for the server, store, and peer subsystems.
func (s *Server) ApplyHotReload(cfg *config.Config) {
	if cfg == nil {
		return
	}
	s.cfg.Store(cfg)
	s.store.ReplaceConfig(cfg)
	s.peer.SetConfig(cfg)
	s.peer.SyncPeersFromConfig(cfg.Peers)
}

// SetRunCancel stores the cancel function for the root context (used by mgmt SHUTDOWN).
func (s *Server) SetRunCancel(cancel context.CancelFunc) {
	s.cancelRun = cancel
}

// SetOnReload is invoked after a successful disk reload with the list of changed hot fields.
func (s *Server) SetOnReload(fn func(changed []string)) {
	s.onReload = fn
}

// SetBuildVersion sets the build label for INFO, Prometheus, and management snapshots (call from main).
func (s *Server) SetBuildVersion(v string) {
	s.stats.SetBuildVersion(v)
}

// ReloadFromDisk loads configuration from ConfigPath and applies it (additive peers allowed).
func (s *Server) ReloadFromDisk() ([]string, error) {
	path := s.ConfigPath()
	if path == "" {
		return nil, fmt.Errorf("config path not set")
	}
	cur := s.CurrentConfig()
	loaded, changed, err := config.Reload(cur, path)
	if err != nil {
		return nil, err
	}
	cfgCopy := loaded
	s.ApplyHotReload(&cfgCopy)
	return changed, nil
}

// ReloadConfig runs ReloadFromDisk and the onReload hook (same path as SIGHUP).
func (s *Server) ReloadConfig() ([]string, error) {
	changed, err := s.ReloadFromDisk()
	if err != nil {
		return nil, err
	}
	if s.onReload != nil {
		s.onReload(changed)
	}
	slog.Info("config reloaded", "changed", changed)
	return changed, nil
}

// Run listens on the configured client TCP port until ctx is cancelled or the listener closes.
func (s *Server) Run(ctx context.Context) error {
	defer s.closeStoreOnce()
	defer s.signalRunFinished()

	if err := ctx.Err(); err != nil {
		return err
	}

	c := s.config()
	mgmtUnix := strings.TrimSpace(c.MgmtSocket)
	mgmtTCPAddr := ""
	if c.MgmtTCPPort > 0 {
		bind := strings.TrimSpace(c.MgmtTCPBind)
		if bind == "" {
			bind = config.DefaultMgmtTCPBind
		}
		mgmtTCPAddr = net.JoinHostPort(bind, fmt.Sprintf("%d", c.MgmtTCPPort))
	}
	if (mgmtUnix != "" && mgmtUnix != "-") || mgmtTCPAddr != "" {
		ms := mgmt.New(mgmtUnix, mgmtTCPAddr, c.SharedSecret, s)
		if mgmtTCPAddr != "" {
			slog.Info("management API also on loopback TCP", "addr", mgmtTCPAddr)
		}
		go func() {
			if err := ms.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(os.Stderr, "supercache mgmt: %v\n", err)
			}
		}()
	}

	peerErr := make(chan error, 1)
	go func() {
		peerErr <- s.peer.Run(ctx)
	}()
	select {
	case <-s.peer.ListenReady():
	case err := <-peerErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("peer mesh: %w", err)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("peer listen timeout")
	}

	candidates := config.BootstrapCandidates(c)
	if len(candidates) > 0 {
		s.clientReady.Store(false)
		s.stats.setBootstrapState("syncing")
		depth := c.BootstrapQueueDepth
		if depth < 1 {
			depth = 1
		}
		s.peer.SetBootstrapInboundActive(true, depth)
		if err := s.peer.PullSnapshotFailover(ctx, candidates); err != nil {
			s.peer.SetBootstrapInboundActive(false, 0)
			return fmt.Errorf("bootstrap: %w", err)
		}
		if err := s.peer.DrainBootstrapInboundQueue(ctx); err != nil {
			s.peer.SetBootstrapInboundActive(false, 0)
			return err
		}
		s.peer.SetBootstrapInboundActive(false, 0)
		s.stats.setBootstrapState("ready")
		keysLoaded := s.store.DBSize()
		slog.Info(fmt.Sprintf("Bootstrap complete. Serving clients. Keys loaded: %d.", keysLoaded),
			"node_id", s.stats.NodeID(),
			"db_size", keysLoaded,
			"keys_applied", s.stats.BootstrapKeysApplied(),
		)
	}
	s.clientReady.Store(true)
	s.runPrometheusMetrics(ctx)

	addr := fmt.Sprintf("%s:%d", c.ClientBind, c.ClientPort)
	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.clientTLS = nil
	if c.ClientTLSEnabled() {
		minV, err := tlsconfig.ParseMinVersion(c.ClientTLSMinVersion)
		if err != nil {
			_ = tcpLn.Close()
			return fmt.Errorf("client tls min version: %w", err)
		}
		tlsCfg, err := tlsconfig.LoadServerTLS(c.ClientTLSCertFile, c.ClientTLSKeyFile, minV)
		if err != nil {
			_ = tcpLn.Close()
			return fmt.Errorf("client tls: %w", err)
		}
		s.clientTLS = tlsCfg
		l := tls.NewListener(tcpLn, tlsCfg)
		s.listenerMu.Lock()
		s.listener = l
		shouldClose := s.listenerCloseRequested
		s.listenerMu.Unlock()
		if shouldClose {
			_ = l.Close()
		}
		slog.Info("redis client port using TLS", "addr", addr)
	} else {
		s.listenerMu.Lock()
		s.listener = tcpLn
		shouldClose := s.listenerCloseRequested
		s.listenerMu.Unlock()
		if shouldClose {
			_ = tcpLn.Close()
		}
	}
	s.listenerMu.Lock()
	ln := s.listener
	s.listenerMu.Unlock()

	go func() {
		<-ctx.Done()
		s.closeListenerOnce()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.wg.Wait()
			if errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		connKey := fmt.Sprintf("%p", conn)
		s.activeConns.Store(connKey, conn)
		s.wg.Add(1)
		go func(key string, c net.Conn) {
			defer s.wg.Done()
			defer s.activeConns.Delete(key)
			s.serveConn(ctx, c)
		}(connKey, conn)
	}
}

func (s *Server) signalRunFinished() {
	s.runDoneOnce.Do(func() {
		close(s.runFinished)
	})
}

func (s *Server) closeListenerOnce() {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	s.listenerCloseRequested = true
	if s.listener != nil {
		_ = s.listener.Close()
		s.listener = nil
	}
}

func (s *Server) closeStoreOnce() {
	if s.store != nil {
		s.savePeerStateIfConfigured()
		s.store.Close()
	}
}

func (s *Server) savePeerStateIfConfigured() {
	if s.peer == nil {
		return
	}
	p := strings.TrimSpace(s.config().PeerStateFile)
	if p == "" {
		return
	}
	_ = peer.SavePeerStateFile(p, s.peer.ConfigPeerAddrs())
}

// replSpillPathResolved returns the JSON path for pending replication spill, or "" if disabled ("-" in config).
func (s *Server) replSpillPathResolved() string {
	p := strings.TrimSpace(s.config().ReplShutdownSpillPath)
	if p == "-" {
		return ""
	}
	if p != "" {
		return p
	}
	cp := s.ConfigPath()
	if cp != "" {
		return filepath.Join(filepath.Dir(cp), "supercache-repl-spill.json")
	}
	return filepath.Join(os.TempDir(), "supercache-repl-spill.json")
}

func replShutdownDuration(parent context.Context) time.Duration {
	if dl, ok := parent.Deadline(); ok {
		if rem := time.Until(dl); rem > 2*time.Second {
			return rem
		}
	}
	return 30 * time.Second
}

// Shutdown closes the client listener, waits for client handlers to finish, then flushes outbound
// replication to peers (bounded time) and writes any remaining queued lines to a JSON spill file
// when the path is enabled (see repl_shutdown_spill_path).
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("Shutdown initiated")
	s.shuttingDown.Store(true)
	timeout := time.Duration(s.config().ShutdownTimeout) * time.Second
	if timeout <= 0 {
		timeout = 7 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	deadlineReached := func(stage string) bool {
		select {
		case <-shutdownCtx.Done():
			slog.Warn("shutdown deadline reached, forcing exit", "stage", stage)
			return true
		default:
			return false
		}
	}

	// Stop accepting new client and peer connections first.
	s.closeListenerOnce()
	if s.peer != nil {
		s.peer.CloseListener()
	}
	if deadlineReached("close listeners") {
		return nil
	}

	// Tear down peer connections early so replication waits cannot block shutdown.
	if s.peer != nil {
		s.peer.CloseActiveConnections()
	}
	if deadlineReached("close peer connections") {
		return nil
	}

	// Allow client handlers to finish in-flight command/response work first.
	wgDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(wgDone)
	}()
	waitCtx, waitCancel := context.WithTimeout(shutdownCtx, 5*time.Second)
	defer waitCancel()
	select {
	case <-wgDone:
	case <-waitCtx.Done():
		if !errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			slog.Warn("shutdown deadline reached, forcing exit", "stage", "waitgroup")
			return nil
		}
		// Some handlers are still blocked in read/write; nudge them with a short future deadline.
		s.activeConns.Range(func(_, v any) bool {
			conn, ok := v.(net.Conn)
			if !ok || conn == nil {
				return true
			}
			_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
			return true
		})
		select {
		case <-wgDone:
		case <-time.After(2 * time.Second):
			slog.Warn("shutdown waitgroup timeout; continuing exit")
		case <-shutdownCtx.Done():
			slog.Warn("shutdown deadline reached, forcing exit", "stage", "post-drain waitgroup")
			return nil
		}
	}
	if deadlineReached("after waitgroup") {
		return nil
	}

	// After client handlers have drained, cancel root run context to stop remaining loops.
	if s.cancelRun != nil {
		s.cancelRun()
	}
	if deadlineReached("cancel run context") {
		return nil
	}

	// Best-effort replication finalization, bounded by remaining shutdown budget.
	if s.peer != nil {
		done := make(chan struct{})
		go func() {
			s.peer.FinalizeGracefulShutdown(shutdownCtx, s.replSpillPathResolved())
			close(done)
		}()
		select {
		case <-done:
		case <-shutdownCtx.Done():
			slog.Warn("shutdown deadline reached, forcing exit", "stage", "peer finalize")
			return nil
		}
	}
	slog.Info("Shutdown complete")
	return nil
}
