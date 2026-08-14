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
	"syscall"
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

	listener               net.Listener
	clientTLS              *tls.Config // non-nil when Redis client port uses TLS
	wg                     sync.WaitGroup
	listenerMu             sync.Mutex
	listenerCloseRequested bool

	sessionSeq   atomic.Int64
	activeConns  sync.Map
	shuttingDown atomic.Bool

	cancelRun context.CancelFunc
	onReload  func(changed []string)

	runFinished chan struct{}
	runDoneOnce sync.Once

	clientReady atomic.Bool
	// peerSeeds are dial candidates discovered from configuration at startup that are not in
	// the peers list, currently the advertise address of the node this configuration was
	// copied from.
	peerSeeds []string
}

// bootstrapSources returns every address this node could pull a snapshot from: the configured
// bootstrap_peer and peers, plus any seed discovered from a configuration copied off another
// node. An empty result means this node knows of nowhere to sync from.
func (s *Server) bootstrapSources(c *config.Config) []string {
	out := config.BootstrapCandidates(c)
	seen := make(map[string]struct{}, len(out))
	for _, a := range out {
		seen[config.NormalizePeerAddr(a)] = struct{}{}
	}
	for _, seed := range s.peerSeeds {
		if _, ok := seen[config.NormalizePeerAddr(seed)]; ok {
			continue
		}
		out = append(out, seed)
		seen[config.NormalizePeerAddr(seed)] = struct{}{}
	}
	return out
}

// bootstrapUntilSynced pulls a snapshot, retrying until one source answers or the server stops.
//
// It does not give up. A node that cannot reach any source is far more likely to be starting
// during a brief outage than to be the founder of a new cluster, and the two are impossible to
// tell apart from here. Exiting would crash-loop an autoscaled node whose peers are briefly
// unreachable, and serving would hand clients an empty dataset; staying up and refusing
// commands with LOADING is the only option that cannot lose data or hide the problem.
func (s *Server) bootstrapUntilSynced(ctx context.Context, candidates []string) {
	depth := s.config().BootstrapQueueDepth
	if depth < 1 {
		depth = 1
	}
	const (
		minBackoff = 500 * time.Millisecond
		maxBackoff = 30 * time.Second
	)
	backoff := minBackoff
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		err := s.bootstrapOnce(ctx, candidates, depth)
		if err == nil {
			s.stats.setBootstrapState("ready")
			s.clientReady.Store(true)
			keysLoaded := s.store.DBSize()
			slog.Info(fmt.Sprintf("Bootstrap complete. Serving clients. Keys loaded: %d.", keysLoaded),
				"node_id", s.stats.NodeID(),
				"db_size", keysLoaded,
				"keys_applied", s.stats.BootstrapKeysApplied(),
			)
			return
		}
		if ctx.Err() != nil {
			return
		}
		slog.Error("bootstrap failed; this node will not serve clients until it syncs",
			"attempt", attempt, "sources", len(candidates), "retry_in", backoff, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// bootstrapOnce runs a single bootstrap attempt across all sources.
//
// Inbound replication is buffered only for the duration of the attempt. Holding the buffer open
// across the wait between attempts would let it overflow during a long outage, and a failed
// attempt already empties the store, so the next attempt starts from a clean slate either way.
func (s *Server) bootstrapOnce(ctx context.Context, candidates []string, depth int) error {
	s.peer.SetBootstrapInboundActive(true, depth)
	defer s.peer.SetBootstrapInboundActive(false, 0)
	if err := s.awaitPeerLink(ctx); err != nil {
		return err
	}
	if err := s.peer.PullSnapshotFailover(ctx, candidates); err != nil {
		return err
	}
	return s.peer.DrainBootstrapInboundQueue(ctx)
}

// awaitPeerLink blocks until this node has at least one peer link, so that replication is
// already being buffered before the snapshot is taken.
//
// Without this the snapshot can complete while no link exists, and every write the source makes
// until the link comes up is missed: it is too late for the snapshot and too early for the
// buffer. Nothing detects the loss, because no receiver reads the sequence numbers that would
// reveal it. Buffering is already active when this is called, so the moment a link appears its
// frames are queued rather than applied.
func (s *Server) awaitPeerLink(ctx context.Context) error {
	const (
		wait = 15 * time.Second
		tick = 50 * time.Millisecond
	)
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		if s.peer.LiveLinkCount() > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("no peer link established within %s; cannot sync without one", wait)
		case <-time.After(tick):
		}
	}
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
	nodeID, advertise, seeds := resolveIdentity(cfg)
	stats := newStats(nodeID, cfg.ClientPort)
	ps := peer.NewService(cfg, st, stats, stats, nodeID)
	ps.SetAdvertiseAddr(advertise)
	s := &Server{
		store:       st,
		registry:    commands.NewRegistry(),
		pubsub:      client.NewSubscriptionManager(),
		peer:        ps,
		stats:       stats,
		peerSeeds:   seeds,
		runFinished: make(chan struct{}),
	}
	s.cfg.Store(cfg)
	s.clientReady.Store(true)
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

	// Dial candidates found in configuration but absent from the peers list. AddPeer validates
	// the address, discards it if it is this node, and skips it if it is already configured, so
	// a seed that turns out to be redundant costs nothing.
	for _, seed := range s.peerSeeds {
		if err := s.peer.AddPeer(seed); err != nil {
			slog.Debug("configured self address not added as peer", "addr", seed, "err", err)
			continue
		}
		slog.Info("dialing peer candidate taken from configured advertise_addr", "addr", seed)
	}

	// Every address this node knows of is a possible snapshot source. A node that knows a peer
	// must never begin serving from an empty store: it would answer misses for every key the
	// cluster holds, which reads as data loss to a client and is indistinguishable from a cache
	// that has simply expired.
	candidates := s.bootstrapSources(c)
	if len(candidates) == 0 {
		// Nowhere to sync from, so this node is the whole cluster as far as it can tell and its
		// own store is authoritative.
		s.clientReady.Store(true)
	} else {
		s.clientReady.Store(false)
		s.stats.setBootstrapState("syncing")
		go s.bootstrapUntilSynced(ctx, candidates)
	}
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

	var acceptDelay time.Duration
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				s.waitHandlersWithTimeout(10 * time.Second)
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				s.waitHandlersWithTimeout(10 * time.Second)
				return ctx.Err()
			default:
			}
			// Transient conditions (fd exhaustion, aborted connections, kernel buffer
			// pressure) can clear on their own; keep the listener serving. Parking here
			// instead would blackhole every new client: the kernel keeps completing
			// handshakes into the backlog while nothing accepts them.
			if isTransientAcceptError(err) {
				if acceptDelay == 0 {
					acceptDelay = 10 * time.Millisecond
				} else if acceptDelay < time.Second {
					acceptDelay *= 2
				}
				slog.Warn("accept failed; retrying", "err", err, "delay", acceptDelay)
				time.Sleep(acceptDelay)
				continue
			}
			// Fatal: stop listening so clients fail fast with a refused connection,
			// and return so the process exits and the supervisor restarts it.
			s.closeListenerOnce()
			return fmt.Errorf("accept: %w", err)
		}
		acceptDelay = 0
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

// isTransientAcceptError reports whether an Accept error is a resource or
// connection-level condition that can clear on its own rather than a dead listener.
func isTransientAcceptError(err error) bool {
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOBUFS) || errors.Is(err, syscall.ENOMEM) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EINTR)
}

// waitHandlersWithTimeout waits for client handlers to finish, but never parks
// process exit forever behind connections that will not close.
func (s *Server) waitHandlersWithTimeout(d time.Duration) {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		slog.Warn("client handlers did not drain before exit; continuing", "timeout", d)
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
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
