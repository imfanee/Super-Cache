// Tests for Super-Cache configuration loading, validation, and hot-reload.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	var c Config
	ApplyDefaults(&c)
	if c.ClientBind != DefaultClientBind {
		t.Fatalf("ClientBind: got %q", c.ClientBind)
	}
	if c.ClientPort != DefaultClientPort {
		t.Fatalf("ClientPort: got %d", c.ClientPort)
	}
	if c.PeerBind != DefaultPeerBind {
		t.Fatalf("PeerBind: got %q", c.PeerBind)
	}
	if c.PeerPort != DefaultPeerPort {
		t.Fatalf("PeerPort: got %d", c.PeerPort)
	}
	if c.BootstrapQueueDepth != DefaultBootstrapQueueDepth {
		t.Fatalf("BootstrapQueueDepth: got %d", c.BootstrapQueueDepth)
	}
	if c.PeerQueueDepth != DefaultPeerQueueDepth {
		t.Fatalf("PeerQueueDepth: got %d", c.PeerQueueDepth)
	}
	if c.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Fatalf("HeartbeatInterval: got %d", c.HeartbeatInterval)
	}
	if c.HeartbeatTimeout != DefaultHeartbeatTimeout {
		t.Fatalf("HeartbeatTimeout: got %d", c.HeartbeatTimeout)
	}
	if c.MgmtSocket != DefaultMgmtSocket {
		t.Fatalf("MgmtSocket: got %q", c.MgmtSocket)
	}
	if c.MaxMemory != "0" {
		t.Fatalf("MaxMemory: got %q", c.MaxMemory)
	}
	if c.MaxMemoryPolicy != "noeviction" {
		t.Fatalf("MaxMemoryPolicy: got %q", c.MaxMemoryPolicy)
	}
	if c.LogLevel != "info" {
		t.Fatalf("LogLevel: got %q", c.LogLevel)
	}
	if c.LogOutput != "stdout" {
		t.Fatalf("LogOutput: got %q", c.LogOutput)
	}
}

func TestValidateSharedSecret(t *testing.T) {
	c := minimalValidConfig()
	c.SharedSecret = strings.Repeat("a", 31)
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "shared_secret") {
		t.Fatalf("expected shared_secret error, got %v", err)
	}
	c.SharedSecret = strings.Repeat("a", 32)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePorts(t *testing.T) {
	c := minimalValidConfig()
	c.ClientPort = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for client_port")
	}
	c = minimalValidConfig()
	c.PeerPort = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for peer_port")
	}
	c = minimalValidConfig()
	c.ClientPort = 7379
	c.PeerPort = 7379
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for equal ports")
	}
}

func TestValidateMaxMemoryPolicy(t *testing.T) {
	c := minimalValidConfig()
	c.MaxMemoryPolicy = "invalid"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
	c = minimalValidConfig()
	c.MaxMemoryPolicy = "invalid_policy"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "invalid_policy") {
		t.Fatalf("expected error naming invalid_policy, got %v", err)
	}
	c = minimalValidConfig()
	c.MaxMemoryPolicy = "ALLKEYS-LRU"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLogLevel(t *testing.T) {
	c := minimalValidConfig()
	c.LogLevel = "verbose"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
	c.LogLevel = "WARN"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateHeartbeat(t *testing.T) {
	c := minimalValidConfig()
	c.HeartbeatInterval = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
	c = minimalValidConfig()
	c.HeartbeatInterval = 10
	c.HeartbeatTimeout = 10
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when timeout <= interval")
	}
	c = minimalValidConfig()
	c.HeartbeatInterval = 10
	c.HeartbeatTimeout = 9
	if err := c.Validate(); err == nil {
		t.Fatal("expected error when timeout < interval")
	}
}

func TestValidateTLSClientPair(t *testing.T) {
	c := minimalValidConfig()
	c.ClientTLSCertFile = filepath.Join(t.TempDir(), "c.pem")
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "client_tls") {
		t.Fatalf("expected client tls pair error, got %v", err)
	}
}

func TestValidatePeerTLSRequiresCA(t *testing.T) {
	td := t.TempDir()
	cf := filepath.Join(td, "cert.pem")
	kf := filepath.Join(td, "key.pem")
	if err := os.WriteFile(cf, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kf, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := minimalValidConfig()
	c.PeerTLSCertFile = cf
	c.PeerTLSKeyFile = kf
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "peer_tls_ca_file") {
		t.Fatalf("expected peer_tls_ca_file error, got %v", err)
	}
}

func TestValidatePeerTLSOrphanCA(t *testing.T) {
	td := t.TempDir()
	ca := filepath.Join(td, "ca.pem")
	if err := os.WriteFile(ca, []byte("ca"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := minimalValidConfig()
	c.PeerTLSCAFile = ca
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "peer_tls_ca_file is set") {
		t.Fatalf("expected orphan CA error, got %v", err)
	}
}

func TestValidatePeers(t *testing.T) {
	c := minimalValidConfig()
	c.Peers = []string{"bad"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for bad peer")
	}
	c = minimalValidConfig()
	c.Peers = []string{"notahost_noportnumber"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for malformed peer")
	}
	c = minimalValidConfig()
	c.Peers = []string{"127.0.0.1:0"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for port 0")
	}
	c = minimalValidConfig()
	c.Peers = []string{"127.0.0.1:65536"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for port 65536")
	}
	c = minimalValidConfig()
	c.Peers = []string{"host:99999"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for bad port")
	}
	c = minimalValidConfig()
	c.Peers = []string{"127.0.0.1:7379"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBootstrapPeer(t *testing.T) {
	c := minimalValidConfig()
	c.BootstrapPeer = "not-a-hostport"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for invalid bootstrap_peer")
	}
	c.BootstrapPeer = "127.0.0.1:7379"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.BootstrapPeer = ""
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMaxMemoryString(t *testing.T) {
	c := minimalValidConfig()
	c.MaxMemory = "xyz"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error")
	}
	c.MaxMemory = "512mb"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.MaxMemory = "0"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestParseMemoryBytes(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
	}{
		{"0", 0},
		{"1024", 1024},
		{"1kb", 1024},
		{"1mb", 1024 * 1024},
		{"2gb", 2 * 1024 * 1024 * 1024},
	}
	for _, tc := range tests {
		got, err := parseMemoryBytes(tc.in)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %d want %d", tc.in, got, tc.want)
		}
	}
}

func TestValidateLogOutput(t *testing.T) {
	c := minimalValidConfig()
	c.LogOutput = "stdout"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.LogOutput = "stderr"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "app.log")
	c.LogOutput = logPath
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() != 0 {
		badDir := filepath.Join(tmp, "nowrite")
		if err := os.Mkdir(badDir, 0o500); err != nil {
			t.Fatal(err)
		}
		c.LogOutput = filepath.Join(badDir, "x.log")
		if err := c.Validate(); err == nil {
			t.Fatal("expected error for unwritable parent")
		}
	}
}

func TestLoad(t *testing.T) {
	path := writeToml(t, `
shared_secret = "`+strings.Repeat("s", 32)+`"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientPort != DefaultClientPort {
		t.Fatalf("port %d", cfg.ClientPort)
	}
}

func TestReloadHotOK(t *testing.T) {
	secret := strings.Repeat("x", 32)
	path := writeToml(t, `
shared_secret = "`+secret+`"
log_level = "info"
`)
	cur, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
log_level = "warn"
`)
	newCfg, changed, err := Reload(cur, path2)
	if err != nil {
		t.Fatal(err)
	}
	if newCfg.LogLevel != "warn" {
		t.Fatalf("log level %q", newCfg.LogLevel)
	}
	if len(changed) != 1 || changed[0] != "log_level" {
		t.Fatalf("changed: %v", changed)
	}
}

func TestReloadBlocked(t *testing.T) {
	secret := strings.Repeat("x", 32)
	path := writeToml(t, `
shared_secret = "`+secret+`"
client_port = 6379
`)
	cur, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
client_port = 6380
`)
	_, _, err = Reload(cur, path2)
	if err == nil {
		t.Fatal("expected blocked reload error")
	}
	if !strings.Contains(err.Error(), "client_port") {
		t.Fatalf("err: %v", err)
	}
}

func TestReloadNilCurrent(t *testing.T) {
	path := writeToml(t, `
shared_secret = "`+strings.Repeat("y", 32)+`"
`)
	cfg, changed, err := Reload(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SharedSecret != strings.Repeat("y", 32) {
		t.Fatal("config mismatch")
	}
	if changed != nil {
		t.Fatalf("expected nil changed, got %v", changed)
	}
}

func minimalValidConfig() Config {
	c := Config{
		SharedSecret: strings.Repeat("a", 32),
	}
	ApplyDefaults(&c)
	return c
}

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeToml(t *testing.T, body string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestMaxMemoryBytes(t *testing.T) {
	c := minimalValidConfig()
	c.MaxMemory = "1mb"
	n, err := c.MaxMemoryBytes()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1024*1024 {
		t.Fatalf("got %d", n)
	}
	_, err = parseMemoryBytes("bad_suffix")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeHelpers(t *testing.T) {
	c := minimalValidConfig()
	c.LogLevel = " INFO "
	c.MaxMemoryPolicy = " NoEviction "
	if c.NormalizeLogLevel() != "info" {
		t.Fatal(c.NormalizeLogLevel())
	}
	if c.NormalizeMaxMemoryPolicy() != "noeviction" {
		t.Fatal(c.NormalizeMaxMemoryPolicy())
	}
	c.LogOutput = "/tmp/x.log"
	if !c.LogOutputIsFile() {
		t.Fatal("expected file log")
	}
	c.LogOutput = "stdout"
	if c.LogOutputIsFile() {
		t.Fatal("expected stdout")
	}
}

func TestOpenLogFile(t *testing.T) {
	c := minimalValidConfig()
	c.LogOutput = "stdout"
	if _, err := c.OpenLogFile(); err == nil {
		t.Fatal("expected error")
	}
	tmp := filepath.Join(t.TempDir(), "l.log")
	c.LogOutput = tmp
	f, err := c.OpenLogFile()
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

func TestLoadInvalidToml(t *testing.T) {
	path := writeToml(t, `not valid toml [[[`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadReadError(t *testing.T) {
	_, err := Load("/nonexistent/path/supercache.toml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestReloadMultipleHot(t *testing.T) {
	secret := strings.Repeat("z", 32)
	path1 := writeToml(t, `
shared_secret = "`+secret+`"
log_level = "info"
max_memory = "0"
`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
log_level = "error"
max_memory = "1mb"
`)
	newCfg, changed, err := Reload(cur, path2)
	if err != nil {
		t.Fatal(err)
	}
	if newCfg.LogLevel != "error" || newCfg.MaxMemory != "1mb" {
		t.Fatalf("cfg %+v", newCfg)
	}
	if len(changed) != 2 {
		t.Fatalf("changed %v", changed)
	}
}

func TestReloadBlockedPeers(t *testing.T) {
	secret := strings.Repeat("q", 32)
	path1 := writeToml(t, `
shared_secret = "`+secret+`"
peers = ["127.0.0.1:7379"]
`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
peers = ["127.0.0.1:7380"]
`)
	_, _, err = Reload(cur, path2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "peers") {
		t.Fatalf("%v", err)
	}
}

func TestReloadBootstrapPeerBlocked(t *testing.T) {
	secret := strings.Repeat("q", 32)
	path1 := writeToml(t, `
shared_secret = "`+secret+`"
bootstrap_peer = "127.0.0.1:7379"
`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
bootstrap_peer = "127.0.0.1:7380"
`)
	_, _, err = Reload(cur, path2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bootstrap_peer") {
		t.Fatalf("%v", err)
	}
}

func TestLoadYAML(t *testing.T) {
	secret := strings.Repeat("s", 32)
	path := writeYAML(t, `
shared_secret: "`+secret+`"
client_port: 6381
peer_port: 7378
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientPort != 6381 || cfg.PeerPort != 7378 {
		t.Fatalf("got client=%d peer=%d", cfg.ClientPort, cfg.PeerPort)
	}
}

func TestReloadPeersAdditiveOK(t *testing.T) {
	secret := strings.Repeat("q", 32)
	path1 := writeToml(t, `
shared_secret = "`+secret+`"
peers = ["127.0.0.1:7379"]
`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
peers = ["127.0.0.1:7379", "127.0.0.1:7380"]
`)
	newCfg, changed, err := Reload(cur, path2)
	if err != nil {
		t.Fatal(err)
	}
	if len(newCfg.Peers) != 2 {
		t.Fatalf("peers %+v", newCfg.Peers)
	}
	if len(changed) != 1 || changed[0] != "peers" {
		t.Fatalf("changed %v", changed)
	}
}

func TestReloadBootstrapDepthBlocked(t *testing.T) {
	secret := strings.Repeat("b", 32)
	path1 := writeToml(t, `
shared_secret = "`+secret+`"
bootstrap_queue_depth = 100000
`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
bootstrap_queue_depth = 99999
`)
	_, _, err = Reload(cur, path2)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMgmtTCPBind(t *testing.T) {
	c := minimalValidConfig()
	c.MgmtTCPPort = 9001
	c.MgmtTCPBind = "127.0.0.1"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c = minimalValidConfig()
	c.MgmtTCPPort = 9001
	c.MgmtTCPBind = "10.0.0.1"
	if err := c.Validate(); err == nil {
		t.Fatal("expected mgmt_tcp_bind error")
	}
}

func TestReloadTLSBlocked(t *testing.T) {
	secret := strings.Repeat("q", 32)
	td := t.TempDir()
	pc := filepath.Join(td, "p.crt")
	pk := filepath.Join(td, "p.key")
	ca := filepath.Join(td, "ca.pem")
	if err := os.WriteFile(pc, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pk, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ca, []byte("ca"), 0o644); err != nil {
		t.Fatal(err)
	}
	path1 := writeToml(t, `shared_secret = "`+secret+`"`)
	cur, err := Load(path1)
	if err != nil {
		t.Fatal(err)
	}
	path2 := writeToml(t, `
shared_secret = "`+secret+`"
peer_tls_cert_file = "`+pc+`"
peer_tls_key_file = "`+pk+`"
peer_tls_ca_file = "`+ca+`"
`)
	_, _, err = Reload(cur, path2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "peer_tls") {
		t.Fatalf("%v", err)
	}
}

func TestMergePeerListsAndBootstrapCandidates(t *testing.T) {
	got := MergePeerLists([]string{" 127.0.0.1:1 ", "127.0.0.1:1", ""}, []string{"127.0.0.1:2", "127.0.0.1:1"})
	if len(got) != 2 || got[0] != "127.0.0.1:1" || got[1] != "127.0.0.1:2" {
		t.Fatalf("merge: %v", got)
	}
	c := minimalValidConfig()
	c.BootstrapPeer = "10.0.0.1:7379"
	c.Peers = []string{"10.0.0.1:7380", "10.0.0.1:7379"}
	cand := BootstrapCandidates(&c)
	if len(cand) != 2 || cand[0] != "10.0.0.1:7379" || cand[1] != "10.0.0.1:7380" {
		t.Fatalf("candidates: %v", cand)
	}
	if BootstrapCandidates(nil) != nil {
		t.Fatal("nil config")
	}
}

func TestClientAndPeerTLSEnabled(t *testing.T) {
	var nilCfg *Config
	if nilCfg.ClientTLSEnabled() || nilCfg.PeerTLSEnabled() {
		t.Fatal("nil should be false")
	}
	c := minimalValidConfig()
	if c.ClientTLSEnabled() || c.PeerTLSEnabled() {
		t.Fatal("no certs")
	}
	c.ClientTLSCertFile = "/x.pem"
	c.ClientTLSKeyFile = "/y.pem"
	if !c.ClientTLSEnabled() {
		t.Fatal("client tls")
	}
	c = minimalValidConfig()
	c.PeerTLSCertFile = "/a.pem"
	c.PeerTLSKeyFile = "/b.pem"
	if !c.PeerTLSEnabled() {
		t.Fatal("peer tls")
	}
}

func TestValidateTLSMinVersionStrings(t *testing.T) {
	td := t.TempDir()
	cc := filepath.Join(td, "c.pem")
	ck := filepath.Join(td, "k.pem")
	if err := os.WriteFile(cc, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ck, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := minimalValidConfig()
	c.ClientTLSCertFile = cc
	c.ClientTLSKeyFile = ck
	c.ClientTLSMinVersion = "1.3"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.ClientTLSMinVersion = "9.9"
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "client_tls_min_version") {
		t.Fatalf("got %v", err)
	}
}

func TestResolveConfigPathHelpers(t *testing.T) {
	if p := ResolveConfigPathForLoad("/tmp/explicit.toml"); p != "/tmp/explicit.toml" {
		t.Fatalf("explicit: %q", p)
	}
	if p := ResolveConfigPathForLoad(""); p != DefaultConfigPath && FirstExistingDefaultConfigPath() == "" {
		// no default file on disk: spec says return DefaultConfigPath for consistent errors
		if p != DefaultConfigPath {
			t.Fatalf("empty explicit: got %q", p)
		}
	}
	_ = FirstExistingDefaultConfigPath()
}

func TestLoadMinimalTomlDefaults(t *testing.T) {
	path := writeToml(t, `shared_secret = "`+strings.Repeat("m", 32)+`"`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientBind != DefaultClientBind || cfg.ClientPort != DefaultClientPort ||
		cfg.PeerPort != DefaultPeerPort || cfg.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestLoadFullToml(t *testing.T) {
	sec := strings.Repeat("f", 32)
	body := `
shared_secret = "` + sec + `"
client_bind = "127.0.0.1"
client_port = 6382
peer_bind = "127.0.0.2"
peer_port = 7381
peers = ["10.0.0.1:7379"]
bootstrap_peer = "10.0.0.2:7379"
max_memory = "2mb"
max_memory_policy = "allkeys-lru"
auth_password = "redis-secret"
log_level = "warn"
log_output = "stderr"
bootstrap_queue_depth = 1000
peer_queue_depth = 10000
heartbeat_interval = 7
heartbeat_timeout = 21
mgmt_socket = "/tmp/supercache-test.sock"
`
	path := writeToml(t, body)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientBind != "127.0.0.1" || cfg.ClientPort != 6382 || cfg.PeerBind != "127.0.0.2" ||
		cfg.PeerPort != 7381 || len(cfg.Peers) != 1 || cfg.MaxMemory != "2mb" ||
		cfg.AuthPassword != "redis-secret" || cfg.LogLevel != "warn" || cfg.HeartbeatInterval != 7 {
		t.Fatalf("full cfg mismatch: %+v", cfg)
	}
}

func TestReloadLogLevelInfoToDebug(t *testing.T) {
	sec := strings.Repeat("d", 32)
	p1 := writeToml(t, `shared_secret = "`+sec+`"
log_level = "info"
`)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := writeToml(t, `shared_secret = "`+sec+`"
log_level = "debug"
`)
	newCfg, changed, err := Reload(cur, p2)
	if err != nil {
		t.Fatal(err)
	}
	if newCfg.LogLevel != "debug" {
		t.Fatal(newCfg.LogLevel)
	}
	found := false
	for _, f := range changed {
		if f == "log_level" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed: %v", changed)
	}
}

func TestReloadAuthPasswordHot(t *testing.T) {
	sec := strings.Repeat("p", 32)
	p1 := writeToml(t, `shared_secret = "`+sec+`"
auth_password = "old"
`)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := writeToml(t, `shared_secret = "`+sec+`"
auth_password = "new"
`)
	newCfg, changed, err := Reload(cur, p2)
	if err != nil {
		t.Fatal(err)
	}
	if newCfg.AuthPassword != "new" {
		t.Fatal(newCfg.AuthPassword)
	}
	found := false
	for _, f := range changed {
		if f == "auth_password" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed: %v", changed)
	}
}

func TestNewConfigFieldDefaults(t *testing.T) {
	var c Config
	ApplyDefaults(&c)
	if c.ShutdownTimeout != DefaultShutdownTimeout {
		t.Fatalf("ShutdownTimeout: got %d, want %d", c.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if c.ExpirySweepMs != DefaultExpirySweepMs {
		t.Fatalf("ExpirySweepMs: got %d, want %d", c.ExpirySweepMs, DefaultExpirySweepMs)
	}
	if c.ExpirySampleSize != DefaultExpirySampleSize {
		t.Fatalf("ExpirySampleSize: got %d, want %d", c.ExpirySampleSize, DefaultExpirySampleSize)
	}
	if c.AuthRateLimit != DefaultAuthRateLimit {
		t.Fatalf("AuthRateLimit: got %d, want %d", c.AuthRateLimit, DefaultAuthRateLimit)
	}
}

func TestSlowlogThresholdDefaultZero(t *testing.T) {
	var c Config
	ApplyDefaults(&c)
	if c.SlowlogThresholdUs != 0 {
		t.Fatalf("SlowlogThresholdUs should default to 0 (disabled), got %d", c.SlowlogThresholdUs)
	}
}
