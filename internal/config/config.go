// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package config provides Super-Cache configuration loading, validation, and hot-reload.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// Config holds all Super-Cache server settings loaded from TOML or YAML.
type Config struct {
	// ClientBind is the IP address or interface name for client connections.
	ClientBind string `toml:"client_bind" yaml:"client_bind"`
	// ClientPort is the TCP port for Redis-protocol clients.
	ClientPort int `toml:"client_port" yaml:"client_port"`
	// PeerBind is the IP address or interface name for peer connections.
	PeerBind string `toml:"peer_bind" yaml:"peer_bind"`
	// PeerPort is the TCP port for cluster peer communication.
	PeerPort int `toml:"peer_port" yaml:"peer_port"`
	// Peers is the list of peer addresses as host:port strings.
	Peers []string `toml:"peers" yaml:"peers"`
	// BootstrapPeer is an optional host:port to pull a full snapshot from once at startup (empty skips).
	BootstrapPeer string `toml:"bootstrap_peer" yaml:"bootstrap_peer"`
	// SharedSecret is the cluster authentication secret; required and at least 32 characters.
	SharedSecret string `toml:"shared_secret" yaml:"shared_secret"`
	// MaxMemory is the maximum memory limit, e.g. 2gb, 512mb, or 0 for unlimited.
	MaxMemory string `toml:"max_memory" yaml:"max_memory"`
	// MaxMemoryPolicy selects eviction behavior when memory is full.
	MaxMemoryPolicy string `toml:"max_memory_policy" yaml:"max_memory_policy"`
	// AuthPassword is the optional Redis AUTH password; empty disables authentication.
	AuthPassword string `toml:"auth_password" yaml:"auth_password"`
	// LogLevel selects logging verbosity: debug, info, warn, or error.
	LogLevel string `toml:"log_level" yaml:"log_level"`
	// LogOutput is stdout, stderr, or a file path for log output.
	LogOutput string `toml:"log_output" yaml:"log_output"`
	// LogFormat selects slog output: text, json, or logfmt (logfmt uses key=value lines like text; NFR-031).
	LogFormat string `toml:"log_format" yaml:"log_format"`
	// MetricsBind is the listen address for Prometheus /metrics (empty defaults to 0.0.0.0 when MetricsPort is set).
	MetricsBind string `toml:"metrics_bind" yaml:"metrics_bind"`
	// MetricsPort is the TCP port for the Prometheus scrape endpoint; 0 disables (NFR-032).
	MetricsPort int `toml:"metrics_port" yaml:"metrics_port"`
	// BootstrapQueueDepth is the capacity of the bootstrap write queue.
	BootstrapQueueDepth int `toml:"bootstrap_queue_depth" yaml:"bootstrap_queue_depth"`
	// PeerQueueDepth is the capacity of each peer outbound message queue.
	PeerQueueDepth int `toml:"peer_queue_depth" yaml:"peer_queue_depth"`
	// HeartbeatInterval is the peer heartbeat interval in seconds.
	HeartbeatInterval int `toml:"heartbeat_interval" yaml:"heartbeat_interval"`
	// HeartbeatTimeout is the peer heartbeat timeout in seconds.
	HeartbeatTimeout int `toml:"heartbeat_timeout" yaml:"heartbeat_timeout"`
	// MgmtSocket is the Unix domain socket path for management commands.
	MgmtSocket string `toml:"mgmt_socket" yaml:"mgmt_socket"`
	// MgmtTCPBind is the loopback address for optional TCP management (FR-028); default 127.0.0.1 when MgmtTCPPort > 0.
	MgmtTCPBind string `toml:"mgmt_tcp_bind" yaml:"mgmt_tcp_bind"`
	// MgmtTCPPort is the TCP port for loopback management API; 0 disables (Unix socket only).
	MgmtTCPPort int `toml:"mgmt_tcp_port" yaml:"mgmt_tcp_port"`
	// GossipPeers enables PEER_ANNOUNCE after mesh handshake so nodes learn peer addresses (P2.4).
	GossipPeers bool `toml:"gossip_peers" yaml:"gossip_peers"`
	// PeerStateFile is an optional JSON path to persist merged peer list across restarts (P2.5).
	PeerStateFile string `toml:"peer_state_file" yaml:"peer_state_file"`
	// ReplShutdownSpillPath is the JSON file written when pending outbound replication cannot be flushed before exit.
	// Empty uses <config_dir>/supercache-repl-spill.json; set to "-" to disable writing.
	ReplShutdownSpillPath string `toml:"repl_shutdown_spill_path" yaml:"repl_shutdown_spill_path"`
	// ClientTLSCertFile and ClientTLSKeyFile enable TLS on the Redis client port when both are non-empty (P6.1).
	ClientTLSCertFile string `toml:"client_tls_cert_file" yaml:"client_tls_cert_file"`
	ClientTLSKeyFile  string `toml:"client_tls_key_file" yaml:"client_tls_key_file"`
	// ClientTLSMinVersion is "1.2" (default) or "1.3".
	ClientTLSMinVersion string `toml:"client_tls_min_version" yaml:"client_tls_min_version"`
	// PeerTLSCertFile and PeerTLSKeyFile enable TLS on the peer mesh listener when both are non-empty.
	PeerTLSCertFile string `toml:"peer_tls_cert_file" yaml:"peer_tls_cert_file"`
	PeerTLSKeyFile  string `toml:"peer_tls_key_file" yaml:"peer_tls_key_file"`
	// PeerTLSCAFile is PEM CA bundle used to verify peer certificates on outbound dials and bootstrap (required when peer TLS is enabled).
	PeerTLSCAFile string `toml:"peer_tls_ca_file" yaml:"peer_tls_ca_file"`
	// PeerTLSMinVersion is "1.2" (default) or "1.3".
	PeerTLSMinVersion string `toml:"peer_tls_min_version" yaml:"peer_tls_min_version"`
}

var allowedMaxMemoryPolicies = map[string]struct{}{
	"noeviction":      {},
	"allkeys-lru":     {},
	"volatile-lru":    {},
	"allkeys-random":  {},
	"volatile-random": {},
	"volatile-ttl":    {},
}

var allowedLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

// Load reads and validates configuration from a TOML or YAML file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	var cfg Config
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config YAML: %w", err)
		}
	default:
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config TOML: %w", err)
		}
	}
	ApplyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// ApplyDefaults sets default values for zero or empty fields in cfg.
func ApplyDefaults(cfg *Config) {
	if cfg.ClientBind == "" {
		cfg.ClientBind = DefaultClientBind
	}
	if cfg.ClientPort == 0 {
		cfg.ClientPort = DefaultClientPort
	}
	if cfg.PeerBind == "" {
		cfg.PeerBind = DefaultPeerBind
	}
	if cfg.PeerPort == 0 {
		cfg.PeerPort = DefaultPeerPort
	}
	if cfg.BootstrapQueueDepth == 0 {
		cfg.BootstrapQueueDepth = DefaultBootstrapQueueDepth
	}
	if cfg.PeerQueueDepth == 0 {
		cfg.PeerQueueDepth = DefaultPeerQueueDepth
	}
	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.HeartbeatTimeout == 0 {
		cfg.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if cfg.MgmtSocket == "" {
		cfg.MgmtSocket = DefaultMgmtSocket
	}
	if cfg.MgmtTCPPort > 0 && strings.TrimSpace(cfg.MgmtTCPBind) == "" {
		cfg.MgmtTCPBind = DefaultMgmtTCPBind
	}
	if cfg.MaxMemory == "" {
		cfg.MaxMemory = "0"
	}
	if cfg.MaxMemoryPolicy == "" {
		cfg.MaxMemoryPolicy = "noeviction"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.LogOutput == "" {
		cfg.LogOutput = "stdout"
	}
}

// Validate checks all configuration constraints and returns an error describing violations.
func (c *Config) Validate() error {
	if len(strings.TrimSpace(c.SharedSecret)) < 32 {
		return fmt.Errorf("shared_secret must be non-empty and at least 32 characters")
	}
	if c.ClientPort < 1 || c.ClientPort > 65535 {
		return fmt.Errorf("client_port must be between 1 and 65535")
	}
	if c.PeerPort < 1 || c.PeerPort > 65535 {
		return fmt.Errorf("peer_port must be between 1 and 65535")
	}
	if c.ClientPort == c.PeerPort {
		return fmt.Errorf("client_port and peer_port must not be equal")
	}
	if _, ok := allowedMaxMemoryPolicies[strings.ToLower(strings.TrimSpace(c.MaxMemoryPolicy))]; !ok {
		return fmt.Errorf("max_memory_policy %q is invalid: must be one of noeviction, allkeys-lru, volatile-lru, allkeys-random, volatile-random, volatile-ttl", strings.TrimSpace(c.MaxMemoryPolicy))
	}
	if _, ok := allowedLogLevels[strings.ToLower(strings.TrimSpace(c.LogLevel))]; !ok {
		return fmt.Errorf("log_level must be one of debug, info, warn, error")
	}
	if c.HeartbeatInterval < 1 {
		return fmt.Errorf("heartbeat_interval must be 1 or greater")
	}
	if c.HeartbeatTimeout <= c.HeartbeatInterval {
		return fmt.Errorf("heartbeat_timeout must be greater than heartbeat_interval")
	}
	for i, peer := range c.Peers {
		if err := ValidatePeerAddr(peer); err != nil {
			return fmt.Errorf("peers[%d]: %w", i, err)
		}
	}
	if strings.TrimSpace(c.BootstrapPeer) != "" {
		if err := ValidatePeerAddr(c.BootstrapPeer); err != nil {
			return fmt.Errorf("bootstrap_peer: %w", err)
		}
	}
	if err := validateMaxMemoryString(c.MaxMemory); err != nil {
		return fmt.Errorf("max_memory: %w", err)
	}
	if err := validateLogOutputPath(c.LogOutput); err != nil {
		return fmt.Errorf("log_output: %w", err)
	}
	lf := strings.ToLower(strings.TrimSpace(c.LogFormat))
	if c.LogFormat != "" && lf != "text" && lf != "json" && lf != "logfmt" {
		return fmt.Errorf("log_format must be text, json, or logfmt")
	}
	if c.MetricsPort != 0 {
		if c.MetricsPort < 1 || c.MetricsPort > 65535 {
			return fmt.Errorf("metrics_port must be 0 or between 1 and 65535")
		}
		if c.MetricsPort == c.ClientPort || c.MetricsPort == c.PeerPort {
			return fmt.Errorf("metrics_port must not equal client_port or peer_port")
		}
	}
	if c.MgmtTCPPort != 0 {
		if c.MgmtTCPPort < 1 || c.MgmtTCPPort > 65535 {
			return fmt.Errorf("mgmt_tcp_port must be 0 or between 1 and 65535")
		}
		if c.MgmtTCPPort == c.ClientPort || c.MgmtTCPPort == c.PeerPort {
			return fmt.Errorf("mgmt_tcp_port must not equal client_port or peer_port")
		}
		if c.MetricsPort != 0 && c.MgmtTCPPort == c.MetricsPort {
			return fmt.Errorf("mgmt_tcp_port must not equal metrics_port")
		}
		if err := validateMgmtTCPBind(c.MgmtTCPBind); err != nil {
			return fmt.Errorf("mgmt_tcp_bind: %w", err)
		}
	} else if strings.TrimSpace(c.MgmtTCPBind) != "" {
		return fmt.Errorf("mgmt_tcp_bind is set but mgmt_tcp_port is 0")
	}
	cc, ck := strings.TrimSpace(c.ClientTLSCertFile), strings.TrimSpace(c.ClientTLSKeyFile)
	if (cc != "") != (ck != "") {
		return fmt.Errorf("client_tls_cert_file and client_tls_key_file must both be set or both empty")
	}
	if cc != "" {
		if err := validateReadableFile(cc); err != nil {
			return fmt.Errorf("client_tls_cert_file: %w", err)
		}
		if err := validateReadableFile(ck); err != nil {
			return fmt.Errorf("client_tls_key_file: %w", err)
		}
	}
	cv := strings.TrimSpace(c.ClientTLSMinVersion)
	if cv != "" {
		if _, err := parseTLSMinVersionString(cv); err != nil {
			return fmt.Errorf("client_tls_min_version: %w", err)
		}
	}
	pc, pk := strings.TrimSpace(c.PeerTLSCertFile), strings.TrimSpace(c.PeerTLSKeyFile)
	if (pc != "") != (pk != "") {
		return fmt.Errorf("peer_tls_cert_file and peer_tls_key_file must both be set or both empty")
	}
	if pc != "" {
		if err := validateReadableFile(pc); err != nil {
			return fmt.Errorf("peer_tls_cert_file: %w", err)
		}
		if err := validateReadableFile(pk); err != nil {
			return fmt.Errorf("peer_tls_key_file: %w", err)
		}
		ca := strings.TrimSpace(c.PeerTLSCAFile)
		if ca == "" {
			return fmt.Errorf("peer_tls_ca_file is required when peer TLS is enabled")
		}
		if err := validateReadableFile(ca); err != nil {
			return fmt.Errorf("peer_tls_ca_file: %w", err)
		}
	} else if strings.TrimSpace(c.PeerTLSCAFile) != "" {
		return fmt.Errorf("peer_tls_ca_file is set but peer TLS certificate files are not configured")
	}
	pv := strings.TrimSpace(c.PeerTLSMinVersion)
	if pv != "" {
		if _, err := parseTLSMinVersionString(pv); err != nil {
			return fmt.Errorf("peer_tls_min_version: %w", err)
		}
	}
	return nil
}

func parseTLSMinVersionString(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "1.2" || s == "1.3" {
		return s, nil
	}
	return "", fmt.Errorf("must be 1.2 or 1.3")
}

func validateReadableFile(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("not a regular file")
	}
	return nil
}

func validateMgmtTCPBind(bind string) error {
	b := strings.TrimSpace(bind)
	if b == "" {
		return nil
	}
	if strings.EqualFold(b, "localhost") {
		return nil
	}
	ip := net.ParseIP(b)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("must be a loopback address (127.0.0.1, ::1, localhost)")
}

// ClientTLSEnabled reports whether the Redis client port should use TLS.
func (c *Config) ClientTLSEnabled() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.ClientTLSCertFile) != "" && strings.TrimSpace(c.ClientTLSKeyFile) != ""
}

// PeerTLSEnabled reports whether the peer mesh listener uses TLS (outbound dials use peer_tls_ca_file).
func (c *Config) PeerTLSEnabled() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.PeerTLSCertFile) != "" && strings.TrimSpace(c.PeerTLSKeyFile) != ""
}

// ValidatePeerAddr checks that s is a non-empty host:port suitable for peer addresses.
func ValidatePeerAddr(s string) error {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("parse host:port: %w", err)
	}
	if host == "" {
		return fmt.Errorf("host must be non-empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func validateMaxMemoryString(s string) error {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "0" || s == "" {
		return nil
	}
	_, err := parseMemoryBytes(s)
	if err != nil {
		return err
	}
	return nil
}

// parseMemoryBytes parses a max_memory string like "512mb" or "2gb" into bytes. Returns 0 for unlimited.
func parseMemoryBytes(s string) (uint64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "0" || s == "" {
		return 0, nil
	}
	i := len(s)
	for i > 0 && (s[i-1] < '0' || s[i-1] > '9') {
		i--
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid max_memory format")
	}
	numStr := s[:i]
	suffix := strings.TrimSpace(s[i:])
	n, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse max_memory number: %w", err)
	}
	var mul uint64 = 1
	switch suffix {
	case "", "b":
		mul = 1
	case "k", "kb":
		mul = 1024
	case "m", "mb":
		mul = 1024 * 1024
	case "g", "gb":
		mul = 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("unknown max_memory suffix %q", suffix)
	}
	if n > ^uint64(0)/mul {
		return 0, fmt.Errorf("max_memory value overflow")
	}
	return n * mul, nil
}

func validateLogOutputPath(logOutput string) error {
	lo := strings.TrimSpace(strings.ToLower(logOutput))
	if lo == "stdout" || lo == "stderr" {
		return nil
	}
	dir := filepath.Dir(logOutput)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("parent directory does not exist: %w", err)
		}
		return fmt.Errorf("stat parent directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("parent path %q is not a directory", dir)
	}
	// Check writable by attempting to create a temp file in the directory.
	tmp, err := os.CreateTemp(dir, ".supercache-log-check-*")
	if err != nil {
		return fmt.Errorf("parent directory not writable: %w", err)
	}
	_ = tmp.Close()
	_ = os.Remove(tmp.Name())
	return nil
}

// Reload loads configuration from path, compares it to current, and returns the new config,
// the list of hot-reloadable field names that changed, and an error if validation fails or
// any non-hot-reloadable field differs from current.
func Reload(current *Config, path string) (Config, []string, error) {
	newCfg, err := Load(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("reload config: %w", err)
	}
	if current == nil {
		return *newCfg, nil, nil
	}
	changed, blocked := diffConfigs(current, newCfg)
	if len(blocked) > 0 {
		return Config{}, nil, fmt.Errorf("cannot hot-reload changed fields: %s", strings.Join(blocked, ", "))
	}
	return *newCfg, changed, nil
}

func diffConfigs(a, b *Config) (changed []string, blocked []string) {
	var hot []string
	if a.ClientBind != b.ClientBind {
		blocked = append(blocked, "client_bind")
	}
	if a.ClientPort != b.ClientPort {
		blocked = append(blocked, "client_port")
	}
	if a.PeerBind != b.PeerBind {
		blocked = append(blocked, "peer_bind")
	}
	if a.PeerPort != b.PeerPort {
		blocked = append(blocked, "peer_port")
	}
	if a.SharedSecret != b.SharedSecret {
		blocked = append(blocked, "shared_secret")
	}
	if a.MgmtSocket != b.MgmtSocket {
		blocked = append(blocked, "mgmt_socket")
	}
	if strings.TrimSpace(a.MgmtTCPBind) != strings.TrimSpace(b.MgmtTCPBind) {
		blocked = append(blocked, "mgmt_tcp_bind")
	}
	if a.MgmtTCPPort != b.MgmtTCPPort {
		blocked = append(blocked, "mgmt_tcp_port")
	}
	if !peersEqual(a.Peers, b.Peers) {
		if peersMultisetSubset(a.Peers, b.Peers) {
			hot = append(hot, "peers")
		} else {
			blocked = append(blocked, "peers")
		}
	}
	if strings.TrimSpace(a.BootstrapPeer) != strings.TrimSpace(b.BootstrapPeer) {
		blocked = append(blocked, "bootstrap_peer")
	}
	if a.BootstrapQueueDepth != b.BootstrapQueueDepth {
		blocked = append(blocked, "bootstrap_queue_depth")
	}
	if a.LogLevel != b.LogLevel {
		hot = append(hot, "log_level")
	}
	if a.LogOutput != b.LogOutput {
		hot = append(hot, "log_output")
	}
	if strings.ToLower(strings.TrimSpace(a.LogFormat)) != strings.ToLower(strings.TrimSpace(b.LogFormat)) {
		hot = append(hot, "log_format")
	}
	if strings.TrimSpace(a.MetricsBind) != strings.TrimSpace(b.MetricsBind) {
		blocked = append(blocked, "metrics_bind")
	}
	if a.MetricsPort != b.MetricsPort {
		blocked = append(blocked, "metrics_port")
	}
	if strings.TrimSpace(a.ClientTLSCertFile) != strings.TrimSpace(b.ClientTLSCertFile) {
		blocked = append(blocked, "client_tls_cert_file")
	}
	if strings.TrimSpace(a.ClientTLSKeyFile) != strings.TrimSpace(b.ClientTLSKeyFile) {
		blocked = append(blocked, "client_tls_key_file")
	}
	if strings.TrimSpace(a.ClientTLSMinVersion) != strings.TrimSpace(b.ClientTLSMinVersion) {
		blocked = append(blocked, "client_tls_min_version")
	}
	if strings.TrimSpace(a.PeerTLSCertFile) != strings.TrimSpace(b.PeerTLSCertFile) {
		blocked = append(blocked, "peer_tls_cert_file")
	}
	if strings.TrimSpace(a.PeerTLSKeyFile) != strings.TrimSpace(b.PeerTLSKeyFile) {
		blocked = append(blocked, "peer_tls_key_file")
	}
	if strings.TrimSpace(a.PeerTLSCAFile) != strings.TrimSpace(b.PeerTLSCAFile) {
		blocked = append(blocked, "peer_tls_ca_file")
	}
	if strings.TrimSpace(a.PeerTLSMinVersion) != strings.TrimSpace(b.PeerTLSMinVersion) {
		blocked = append(blocked, "peer_tls_min_version")
	}
	if a.MaxMemory != b.MaxMemory {
		hot = append(hot, "max_memory")
	}
	if a.MaxMemoryPolicy != b.MaxMemoryPolicy {
		hot = append(hot, "max_memory_policy")
	}
	if a.AuthPassword != b.AuthPassword {
		hot = append(hot, "auth_password")
	}
	if a.HeartbeatInterval != b.HeartbeatInterval {
		hot = append(hot, "heartbeat_interval")
	}
	if a.HeartbeatTimeout != b.HeartbeatTimeout {
		hot = append(hot, "heartbeat_timeout")
	}
	if a.PeerQueueDepth != b.PeerQueueDepth {
		hot = append(hot, "peer_queue_depth")
	}
	if a.GossipPeers != b.GossipPeers {
		blocked = append(blocked, "gossip_peers")
	}
	if strings.TrimSpace(a.PeerStateFile) != strings.TrimSpace(b.PeerStateFile) {
		blocked = append(blocked, "peer_state_file")
	}
	if strings.TrimSpace(a.ReplShutdownSpillPath) != strings.TrimSpace(b.ReplShutdownSpillPath) {
		blocked = append(blocked, "repl_shutdown_spill_path")
	}
	return hot, blocked
}

func peersEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// peersMultisetSubset is true when every address in old appears in new at least as many times
// (hot-reload may only add peers, not remove or change counts downward).
func peersMultisetSubset(old, new []string) bool {
	if len(old) > len(new) {
		return false
	}
	m := make(map[string]int)
	for _, p := range new {
		m[p]++
	}
	for _, p := range old {
		if m[p] == 0 {
			return false
		}
		m[p]--
	}
	return true
}

// MergePeerLists appends extra addresses not already present (P2.5 startup merge).
func MergePeerLists(base, extra []string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, p := range base {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		out = append(out, p)
		seen[p] = struct{}{}
	}
	for _, p := range extra {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		out = append(out, p)
		seen[p] = struct{}{}
	}
	return out
}

// BootstrapCandidates returns ordered bootstrap sources when bootstrap_peer is set: that address first,
// then remaining peers as failover (P2.6). If bootstrap_peer is empty, no bootstrap is performed.
func BootstrapCandidates(c *Config) []string {
	if c == nil {
		return nil
	}
	primary := strings.TrimSpace(c.BootstrapPeer)
	if primary == "" {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	out = append(out, primary)
	seen[primary] = struct{}{}
	for _, p := range c.Peers {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		out = append(out, p)
		seen[p] = struct{}{}
	}
	return out
}

// MaxMemoryBytes returns the configured max memory in bytes, or 0 for unlimited.
func (c *Config) MaxMemoryBytes() (uint64, error) {
	return parseMemoryBytes(c.MaxMemory)
}

// LogOutputIsFile reports whether LogOutput is a file path rather than stdout or stderr.
func (c *Config) LogOutputIsFile() bool {
	lo := strings.TrimSpace(strings.ToLower(c.LogOutput))
	return lo != "stdout" && lo != "stderr"
}

// OpenLogFile opens the log file for append when LogOutput is a file path.
func (c *Config) OpenLogFile() (*os.File, error) {
	if !c.LogOutputIsFile() {
		return nil, fmt.Errorf("log_output is not a file path")
	}
	f, err := os.OpenFile(c.LogOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	return f, nil
}

// NormalizeLogLevel returns the canonical lowercase log level.
func (c *Config) NormalizeLogLevel() string {
	return strings.ToLower(strings.TrimSpace(c.LogLevel))
}

// NormalizeMaxMemoryPolicy returns the canonical lowercase eviction policy.
func (c *Config) NormalizeMaxMemoryPolicy() string {
	return strings.ToLower(strings.TrimSpace(c.MaxMemoryPolicy))
}
