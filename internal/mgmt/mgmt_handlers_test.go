// Management handler and dispatch coverage tests.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

type fakeExt struct {
	uptime          int64
	clients         int64
	peers           int
	mem             int64
	keys            int
	bootstrap       string
	reloadErr       error
	reloadChanged   []string
	peersList       []map[string]any
	peersAddErr     error
	peersRemoveErr  error
	debugKeys       map[string]any
	debugErr        error
	bootstrapStatus map[string]any
	shutdownErr     error
}

func (f *fakeExt) Ping() string { return "PONG" }
func (f *fakeExt) Info() map[string]any {
	return map[string]any{"k": 1}
}
func (f *fakeExt) ReloadConfig() ([]string, error) {
	if f.reloadErr != nil {
		return nil, f.reloadErr
	}
	return f.reloadChanged, nil
}
func (f *fakeExt) Status() map[string]any {
	return map[string]any{
		"uptime_sec":       f.uptime,
		"clients_connected": f.clients,
		"peers_connected":   f.peers,
		"mem_used_bytes":    f.mem,
		"key_count":         f.keys,
		"bootstrap_state":   f.bootstrap,
	}
}
func (f *fakeExt) PeersList() []map[string]any { return f.peersList }
func (f *fakeExt) PeersAdd(addr string) error { return f.peersAddErr }
func (f *fakeExt) PeersRemove(addr string) error {
	return f.peersRemoveErr
}
func (f *fakeExt) DebugKeyspace(maxKeys int) (map[string]any, error) {
	if f.debugErr != nil {
		return nil, f.debugErr
	}
	return f.debugKeys, nil
}
func (f *fakeExt) BootstrapStatus() map[string]any { return f.bootstrapStatus }
func (f *fakeExt) RequestShutdown(graceful bool) error { return f.shutdownErr }

// shutdownTrack embeds fakeExt and records RequestShutdown arguments.
type shutdownTrack struct {
	*fakeExt
	lastGraceful bool
	calls        int
}

func (s *shutdownTrack) RequestShutdown(graceful bool) error {
	s.lastGraceful = graceful
	s.calls++
	return s.fakeExt.RequestShutdown(graceful)
}

func TestHandleStatus(t *testing.T) {
	f := &fakeExt{
		uptime: 42, clients: 5, peers: 2, mem: 1048576, keys: 100, bootstrap: "complete",
	}
	r := HandleStatus(f)
	if !r.OK || r.Data == nil {
		t.Fatal(r)
	}
	m := r.Data.(map[string]any)
	if fmt.Sprint(m["uptime_sec"]) != "42" {
		t.Fatalf("uptime_sec: %v", m["uptime_sec"])
	}
	if fmt.Sprint(m["bootstrap_state"]) != "complete" {
		t.Fatalf("%v", m)
	}
}

func TestDispatchUnknown(t *testing.T) {
	r := DispatchMgmt(&fakeExt{}, &fakeExt{}, MgmtRequest{Cmd: "unknowncmd"})
	if r.OK || !strings.Contains(r.Error, "unknown") {
		t.Fatal(r)
	}
}

func TestHandleShutdown(t *testing.T) {
	t.Run("nil extended", func(t *testing.T) {
		r := HandleShutdown(nil, nil)
		if r.OK || !strings.Contains(r.Error, "shutdown not supported") {
			t.Fatal(r)
		}
	})
	t.Run("default graceful true", func(t *testing.T) {
		f := &shutdownTrack{fakeExt: &fakeExt{}}
		r := HandleShutdown(f, nil)
		if !r.OK || f.calls != 1 || !f.lastGraceful {
			t.Fatalf("r=%+v calls=%d g=%v", r, f.calls, f.lastGraceful)
		}
	})
	t.Run("graceful false in args", func(t *testing.T) {
		f := &shutdownTrack{fakeExt: &fakeExt{}}
		args := json.RawMessage(`{"graceful":false}`)
		r := HandleShutdown(f, args)
		if !r.OK || f.calls != 1 || f.lastGraceful {
			t.Fatal(r)
		}
	})
	t.Run("graceful true explicit", func(t *testing.T) {
		f := &shutdownTrack{fakeExt: &fakeExt{}}
		args := json.RawMessage(`{"graceful":true}`)
		r := HandleShutdown(f, args)
		if !r.OK || !f.lastGraceful {
			t.Fatal(r)
		}
	})
	t.Run("request error", func(t *testing.T) {
		f := &fakeExt{shutdownErr: fmt.Errorf("nope")}
		r := HandleShutdown(f, nil)
		if r.OK || r.Error != "nope" {
			t.Fatal(r)
		}
	})
}

func TestDispatchMgmtBranches(t *testing.T) {
	h := &fakeExt{}
	ext := h
	sec := MgmtRequest{Secret: "x", Cmd: "PING"}
	if !DispatchMgmt(h, ext, sec).OK {
		t.Fatal("PING")
	}
	sec.Cmd = "INFO"
	if !DispatchMgmt(h, ext, sec).OK {
		t.Fatal("INFO")
	}
	for _, cmd := range []string{"STATUS", "RELOAD_CONFIG", "PEERS_LIST", "BOOTSTRAP_STATUS"} {
		sec.Cmd = cmd
		if !DispatchMgmt(h, ext, sec).OK {
			t.Fatalf("%s: %+v", cmd, DispatchMgmt(h, ext, sec))
		}
	}
	sec.Cmd = "SHUTDOWN"
	sec.Args = json.RawMessage(`{"graceful":false}`)
	if !DispatchMgmt(h, ext, sec).OK {
		t.Fatal("SHUTDOWN")
	}
}

func TestHandlePeersListEmpty(t *testing.T) {
	f := &fakeExt{peersList: nil}
	r := HandlePeersList(f)
	if !r.OK {
		t.Fatal(r)
	}
}

func TestHandlePeersListTwo(t *testing.T) {
	f := &fakeExt{
		peersList: []map[string]any{
			{"node_id": "a", "address": "h:1", "state": "connected", "last_heartbeat_ago_sec": 1, "latency_ms": 2},
			{"node_id": "b", "address": "h:2", "state": "connected", "last_heartbeat_ago_sec": 1, "latency_ms": 2},
		},
	}
	r := HandlePeersList(f)
	if !r.OK {
		t.Fatal(r)
	}
}

func TestHandlePeersAddRemove(t *testing.T) {
	f := &fakeExt{}
	args, _ := json.Marshal(map[string]string{"addr": "127.0.0.1:9999"})
	r := HandlePeersAdd(f, args)
	if !r.OK {
		t.Fatal(r)
	}
	args2, _ := json.Marshal(map[string]string{"addr": "notvalid"})
	f.peersAddErr = fmt.Errorf("bad")
	r = HandlePeersAdd(f, args2)
	if r.OK {
		t.Fatal(r)
	}
	f.peersRemoveErr = nil
	args3, _ := json.Marshal(map[string]string{"addr": "127.0.0.1:1"})
	r = HandlePeersRemove(f, args3)
	if !r.OK {
		t.Fatal(r)
	}
	f.peersRemoveErr = fmt.Errorf("no peer")
	r = HandlePeersRemove(f, args3)
	if r.OK {
		t.Fatal(r)
	}
}

func TestHandleDebugKeyspace(t *testing.T) {
	f := &fakeExt{debugKeys: map[string]any{"keys": []any{}}}
	ac, _ := json.Marshal(map[string]int{"count": 5})
	r := HandleDebugKeyspace(f, ac)
	if !r.OK {
		t.Fatal(r)
	}
	r = HandleDebugKeyspace(f, json.RawMessage(`{}`))
	if !r.OK {
		t.Fatal(r)
	}
}

func TestHandleBootstrapStatus(t *testing.T) {
	f := &fakeExt{bootstrapStatus: map[string]any{"state": "idle"}}
	r := HandleBootstrapStatus(f)
	if !r.OK {
		t.Fatal(r)
	}
	f.bootstrapStatus = map[string]any{"state": "running", "bytes_received": 1, "keys_applied": 2, "queue_depth": 3}
	r = HandleBootstrapStatus(f)
	if !r.OK {
		t.Fatal(r)
	}
}

func TestMgmtUnixJSONRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "m.sock")
	sec := strings.Repeat("m", 32)
	f := &fakeExt{uptime: 1, clients: 0, peers: 0, mem: 1, keys: 0, bootstrap: "complete"}
	srv := New(sock, "", sec, f)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()
	var c net.Conn
	var err error
	for i := 0; i < 50; i++ {
		c, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	req := MgmtRequest{Secret: sec, Cmd: "status"}
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var res MgmtResponse
	if err := json.Unmarshal(buf[:n], &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatal(res)
	}
	cancel()
}

func TestReloadConfigHotViaHandler(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.toml")
	writeCfg := func(level string) {
		t.Helper()
		content := fmt.Sprintf(`
shared_secret = "%s"
client_port = 6379
peer_port = 7379
log_level = "%s"
`, strings.Repeat("x", 32), level)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg("info")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloader := &reloadHarness{path: path, cfg: cfg}
	r := HandleReloadConfig(reloader)
	if !r.OK {
		t.Fatal(r)
	}
	writeCfg("debug")
	r = HandleReloadConfig(reloader)
	if !r.OK {
		t.Fatal(r)
	}
}

type reloadHarness struct {
	path string
	cfg  *config.Config
}

func (r *reloadHarness) ReloadConfig() ([]string, error) {
	newCfg, changed, err := config.Reload(r.cfg, r.path)
	if err != nil {
		return nil, err
	}
	*r.cfg = newCfg
	return changed, nil
}

func (r *reloadHarness) Ping() string { return "PONG" }
func (r *reloadHarness) Info() map[string]any { return map[string]any{} }
func (r *reloadHarness) Status() map[string]any { return map[string]any{} }
func (r *reloadHarness) PeersList() []map[string]any { return nil }
func (r *reloadHarness) PeersAdd(string) error { return nil }
func (r *reloadHarness) PeersRemove(string) error { return nil }
func (r *reloadHarness) DebugKeyspace(int) (map[string]any, error) { return map[string]any{}, nil }
func (r *reloadHarness) BootstrapStatus() map[string]any { return map[string]any{"state": "idle"} }
func (r *reloadHarness) RequestShutdown(bool) error { return nil }
