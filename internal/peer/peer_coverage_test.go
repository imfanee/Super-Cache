// Additional coverage tests for internal/peer (deterministic, short timeouts).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

func TestNoopReplicator(t *testing.T) {
	t.Parallel()
	var r NoopReplicator
	if err := r.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("x")}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSavePeerStateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")
	addrs := []string{"127.0.0.1:7001", "10.0.0.2:7002"}
	if err := SavePeerStateFile(path, addrs); err != nil {
		t.Fatal(err)
	}
	got, err := LoadPeerStateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(addrs) {
		t.Fatalf("got %v", got)
	}
	for i := range addrs {
		if got[i] != addrs[i] {
			t.Fatalf("idx %d: got %q want %q", i, got[i], addrs[i])
		}
	}
}

func TestLoadPeerStateFileInvalidJSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadPeerStateFile(path)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestServiceSetConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{SharedSecret: strings.Repeat("a", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	svc.SetConfig(nil) // no-op path
	if svc.c().ClientPort != cfg.ClientPort {
		t.Fatal("config changed unexpectedly")
	}
	cfg2 := *svc.c()
	cfg2.ClientPort = 9999
	svc.SetConfig(&cfg2)
	if svc.c().ClientPort != 9999 {
		t.Fatalf("SetConfig did not update: %d", svc.c().ClientPort)
	}
}

func TestNormalizePeerMessageBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  PeerMessage
		want MsgType
	}{
		{"typed passthrough", PeerMessage{Type: MsgTypeHello}, MsgTypeHello},
		{"hello", PeerMessage{Payload: json.RawMessage(`{"op":"HELLO"}`)}, MsgTypeHello},
		{"hello_ack", PeerMessage{Payload: json.RawMessage(`{"op":"HELLO_ACK"}`)}, MsgTypeHelloAck},
		{"auth", PeerMessage{Payload: json.RawMessage(`{"op":"AUTH"}`)}, MsgTypeAuth},
		{"hb", PeerMessage{Payload: json.RawMessage(`{"op":"HEARTBEAT"}`)}, MsgTypeHeartbeat},
		{"hb_ack", PeerMessage{Payload: json.RawMessage(`{"op":"HEARTBEAT_ACK"}`)}, MsgTypeHeartbeat},
		{"peer_ann", PeerMessage{Payload: json.RawMessage(`{"op":"PEER_ANNOUNCE"}`)}, MsgTypeNodeList},
		{"bootstrap", PeerMessage{Payload: json.RawMessage(`{"op":"BOOTSTRAP"}`)}, MsgTypeBootstrapReq},
		{"bootstrap_end", PeerMessage{Payload: json.RawMessage(`{"op":"BOOTSTRAP_END"}`)}, MsgTypeBootstrapDone},
		{"repl fallback", PeerMessage{Payload: json.RawMessage(`{"op":"SET"}`)}, MsgTypeReplicate},
		{"empty payload", PeerMessage{Payload: nil}, ""},
		{"bad json", PeerMessage{Payload: json.RawMessage(`{`)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizePeerMessage(tc.msg)
			if got.Type != tc.want {
				t.Fatalf("got %q want %q", got.Type, tc.want)
			}
		})
	}
}

func TestReadLineWriteLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	type row struct {
		Op string `json:"op"`
		K  string `json:"k"`
	}
	if err := writeLine(&buf, row{Op: "X", K: "y"}); err != nil {
		t.Fatal(err)
	}
	line, err := readLine(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(line, []byte(`"op":"X"`)) {
		t.Fatalf("line %s", line)
	}
}

func TestReadLineEOF(t *testing.T) {
	t.Parallel()
	_, err := readLine(bufio.NewReader(bytes.NewReader(nil)))
	if err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("got %v", err)
	}
}

type failWriteOnce struct{}

func (failWriteOnce) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func TestWriteLineErrorAndReadLineTrimCR(t *testing.T) {
	t.Parallel()
	if err := writeLine(failWriteOnce{}, map[string]string{"op": "x"}); err == nil {
		t.Fatal("expected write error")
	}
	line, err := readLine(bufio.NewReader(strings.NewReader("{\"op\":\"x\"}\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(line, []byte{'\r'}) || bytes.Contains(line, []byte{'\n'}) {
		t.Fatalf("line contains CRLF: %q", line)
	}
}

func TestBackoffSleepCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backoffSleep(ctx, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestPeersInfoAndConfigPeerAddrsEmpty(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{SharedSecret: strings.Repeat("p", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "node-x")
	if infos := svc.PeersInfo(); len(infos) != 0 {
		t.Fatalf("expected no peers, got %v", infos)
	}
	if addrs := svc.ConfigPeerAddrs(); len(addrs) != 0 {
		t.Fatalf("expected no configured peers, got %v", addrs)
	}
}

func TestPeerDialWithTimeoutConnectionRefused(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("d", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = svc.peerDialWithTimeout(ctx, "127.0.0.1:1", 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestEnsureDialNotRunning(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("e", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	if err := svc.ensureDial("127.0.0.1:9999"); err == nil || !strings.Contains(err.Error(), "dial map not initialized") {
		t.Fatalf("got %v", err)
	}
}

type captureBootstrapMetrics struct {
	depth atomic.Int64
}

func (c *captureBootstrapMetrics) SetReplicationStats(int, []string) {}
func (c *captureBootstrapMetrics) SetBootstrapInboundQueueDepth(d int) {
	c.depth.Store(int64(d))
}

func TestBootstrapInboundBufferDrain(t *testing.T) {
	secret := strings.Repeat("f", 32)
	cfg := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     0,
	}
	config.ApplyDefaults(cfg)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PeerPort = ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	metrics := &captureBootstrapMetrics{}
	svc := NewService(cfg, st, metrics, nil, "boot-node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady timeout")
	}

	svc.SetBootstrapInboundActive(true, 10)
	if metrics.depth.Load() != 0 {
		t.Fatalf("depth %d", metrics.depth.Load())
	}

	dialAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort))
	var c net.Conn
	for i := 0; i < 100; i++ {
		c, err = net.DialTimeout("tcp", dialAddr, 50*time.Millisecond)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	hPayload, err := json.Marshal(wireHello{Op: wireOpHello, Ver: PeerProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hPayload}); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	msg, err := ReadMessage(br)
	if err != nil {
		t.Fatal(err)
	}
	msg = NormalizePeerMessage(msg)
	if msg.Type != MsgTypeHelloAck {
		t.Fatalf("got %q", msg.Type)
	}
	var helloAck wireHelloAck
	if err := json.Unmarshal(msg.Payload, &helloAck); err != nil {
		t.Fatal(err)
	}
	nonce, err := hex.DecodeString(helloAck.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	proofPayload, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce)})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proofPayload}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadMessage(br); err != nil {
		t.Fatal(err)
	}

	repl := wireRepl{Op: "SET", Key: "bootq", Value: []byte("qv")}
	replPayload, err := json.Marshal(repl)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: replPayload}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for metrics.depth.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if metrics.depth.Load() < 1 {
		t.Fatal("expected bootstrap queue depth >= 1")
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	if err := svc.DrainBootstrapInboundQueue(drainCtx); err != nil {
		t.Fatal(err)
	}

	val, err := st.Get("bootq")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "qv" {
		t.Fatalf("got %q", val)
	}

	svc.SetBootstrapInboundActive(false, 1)
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run timeout")
	}
}

func TestDrainReplicationOutboundCancelled(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("g", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.DrainReplicationOutbound(ctx)
}

func TestDrainReplicationOutboundEmpty(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("h", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	svc.DrainReplicationOutbound(ctx)
}

func TestConfigPeerAddrsSyncPeersPeersInfoAddRemove(t *testing.T) {
	secret := strings.Repeat("i", 32)
	port := freeTCPPort(t)
	cfg := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     port,
		Peers:        []string{"127.0.0.1:59999"},
	}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "info-node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady timeout")
	}

	addrs := svc.ConfigPeerAddrs()
	if len(addrs) != 1 || addrs[0] != "127.0.0.1:59999" {
		t.Fatalf("ConfigPeerAddrs: %v", addrs)
	}

	svc.SyncPeersFromConfig([]string{"127.0.0.1:59999", "127.0.0.1:59998"})
	time.Sleep(50 * time.Millisecond)

	if err := svc.AddPeer("127.0.0.1:60001"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddPeer("127.0.0.1:60001"); err == nil {
		t.Fatal("expected duplicate error")
	}

	rows := svc.PeersInfo()
	if len(rows) < 2 {
		t.Fatalf("PeersInfo: %d", len(rows))
	}

	if err := svc.RemovePeer("127.0.0.1:60001"); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run timeout")
	}
}

func TestSetBootstrapInboundActiveMaxCapClamp(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("j", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	svc.SetBootstrapInboundActive(true, 0)
	svc.bootstrapReplMu.Lock()
	mc := svc.bootstrapMaxCap
	svc.bootstrapReplMu.Unlock()
	if mc < 1 {
		t.Fatalf("maxCap %d", mc)
	}
	svc.SetBootstrapInboundActive(false, 1)
}

func TestDrainBootstrapInboundQueueCancelled(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("k", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.DrainBootstrapInboundQueue(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestApplyReplicatePayloadUnknownOp(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("l", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = ApplyReplicatePayload(st, ReplicatePayload{Op: "NOT_A_REAL_OP", Key: "k"})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("got %v", err)
	}
}

func TestWriteReplSpillFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "spill", "out.json")
	entries := []replSpillEntry{
		{Peer: "127.0.0.1:1", Wire: wireRepl{Op: "SET", Key: "a", Seq: 3}},
	}
	if err := writeReplSpillFile(path, "nid", entries); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"node_id"`)) || !bytes.Contains(data, []byte(`127.0.0.1:1`)) {
		t.Fatalf("unexpected file: %s", data)
	}
}

func TestFinalizeGracefulShutdownConfiguredPeersNoOutbound(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("m", 32), Peers: []string{"127.0.0.1:7"}}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.FinalizeGracefulShutdown(ctx, filepath.Join(t.TempDir(), "x.json"))
}

func TestFinalizeGracefulShutdownSpillAndEmptyPath(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("n", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")

	ch := make(chan wireRepl, 2)
	ch <- wireRepl{Op: "SET", Key: "spk", Value: []byte("v")}
	done := make(chan struct{})
	close(done)

	op := &outPeer{
		addr:          "127.0.0.1:40000",
		replCh:        ch,
		cancelSession: func() {},
		writerDone:    done,
	}
	svc.mu.Lock()
	svc.out = append(svc.out, op)
	svc.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dir := t.TempDir()
	spillOK := filepath.Join(dir, "spill.json")
	svc.FinalizeGracefulShutdown(ctx, spillOK)
	if _, err := os.Stat(spillOK); err != nil {
		t.Fatalf("expected spill file: %v", err)
	}

	ch2 := make(chan wireRepl, 1)
	ch2 <- wireRepl{Op: "DEL", Key: "z"}
	op2 := &outPeer{
		addr:          "127.0.0.1:40001",
		replCh:        ch2,
		cancelSession: func() {},
		writerDone:    done,
	}
	svc.mu.Lock()
	svc.out = append(svc.out, op2)
	svc.mu.Unlock()
	svc.FinalizeGracefulShutdown(ctx, "")
}

func TestCloseListenerAndActiveConnections(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("n", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	svc.listener = ln
	svc.CloseListener()
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 50*time.Millisecond); err == nil {
		t.Fatal("expected listener to be closed")
	}

	c1, c2 := net.Pipe()
	defer c2.Close()
	op := &outPeer{
		addr:          "127.0.0.1:40010",
		conn:          c1,
		cancelSession: func() {},
	}
	svc.mu.Lock()
	svc.out = []*outPeer{op}
	svc.mu.Unlock()
	svc.CloseActiveConnections()
	_ = c1.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := c1.Read(buf); err == nil {
		t.Fatal("expected closed outbound conn read error")
	}
}

func TestDialLoopBackoffPath(t *testing.T) {
	secret := strings.Repeat("o", 32)
	cfg := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     freeTCPPort(t),
		Peers:        []string{"127.0.0.1:1"},
	}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady timeout")
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout")
	}
}

func TestGossipOutboundConsumesPeerAnnounce(t *testing.T) {
	secret := strings.Repeat("p", 32)
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)
	cfgA := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     portA,
		GossipPeers:  true,
	}
	cfgB := &config.Config{
		SharedSecret: secret,
		ClientPort:   6378,
		PeerPort:     portB,
		Peers:        []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))},
		GossipPeers:  true,
	}
	config.ApplyDefaults(cfgA)
	config.ApplyDefaults(cfgB)
	stA, err := store.NewStore(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer stA.Close()
	stB, err := store.NewStore(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	svcA := NewService(cfgA, stA, nil, nil, "ga")
	svcB := NewService(cfgB, stB, nil, nil, "gb")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = svcA.Run(ctx) }()
	go func() { _ = svcB.Run(ctx) }()
	select {
	case <-svcB.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady B")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows := svcB.PeersInfo()
		if len(rows) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
}

func TestUnregisterOutDirect(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	svc.mu.Lock()
	svc.out = append(svc.out, &outPeer{addr: "127.0.0.1:50001"})
	svc.mu.Unlock()
	svc.unregisterOut("127.0.0.1:50001")
	svc.mu.RLock()
	n := len(svc.out)
	svc.mu.RUnlock()
	if n != 0 {
		t.Fatalf("out len %d", n)
	}
}

func TestPeerDialWithTimeoutTLSInvalidAddr(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("t", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	svc.peerDialTLS = &tls.Config{}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err = svc.peerDialWithTimeout(ctx, "not-valid-hostport", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPullSnapshotFailoverAllSourcesFail(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("u", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = svc.PullSnapshotFailover(ctx, []string{"127.0.0.1:1", "127.0.0.1:2"})
	if err == nil || !strings.Contains(err.Error(), "all sources failed") {
		t.Fatalf("got %v", err)
	}
}

func TestPullSnapshotFailoverNoSources(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("u", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = svc.PullSnapshotFailover(ctx, []string{"", "   "})
	if err == nil || !strings.Contains(err.Error(), "no sources") {
		t.Fatalf("got %v", err)
	}
}

func TestPullSnapshotFailoverSkipsBadFirstAddr(t *testing.T) {
	secret := strings.Repeat("v", 32)
	portA := freeTCPPort(t)
	cfgA := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: portA}
	config.ApplyDefaults(cfgA)
	stA, err := store.NewStore(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer stA.Close()
	if err := stA.Set("fb", []byte("ok"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	svcA := NewService(cfgA, stA, nil, nil, "fa")
	cfgB := &config.Config{SharedSecret: secret, ClientPort: 6378, PeerPort: freeTCPPort(t)}
	config.ApplyDefaults(cfgB)
	stB, err := store.NewStore(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	svcB := NewService(cfgB, stB, nil, nil, "fb")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svcA.Run(ctx) }()
	select {
	case <-svcA.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	good := net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))
	ctxPull, cancelPull := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPull()
	if err := svcB.PullSnapshotFailover(ctxPull, []string{"127.0.0.1:1", good}); err != nil {
		t.Fatal(err)
	}
	val, err := stB.Get("fb")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "ok" {
		t.Fatalf("got %q", val)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run timeout")
	}
}

func TestConsumeOptionalPeerAnnounceValid(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("H", 32), GossipPeers: true}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	pa, err := json.Marshal(wirePeerAnnounce{Op: wireOpPeerAnnounce, Peers: []string{"127.0.0.1:60111"}})
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	go func() {
		defer c1.Close()
		_ = WriteMessage(c1, PeerMessage{Version: 1, Type: MsgTypeNodeList, Payload: pa})
	}()
	err = svc.consumeOptionalPeerAnnounce(c2, bufio.NewReader(c2))
	_ = c2.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestConsumeOptionalPeerAnnounceWrongFrame(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("w", 32), GossipPeers: true}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	c1, c2 := net.Pipe()
	go func() {
		_ = WriteMessage(c1, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: json.RawMessage(`{}`)})
		_ = c1.Close()
	}()
	err = svc.consumeOptionalPeerAnnounce(c2, bufio.NewReader(c2))
	_ = c2.Close()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBootstrapPullSnapshotGossipPeers(t *testing.T) {
	secret := strings.Repeat("x", 32)
	portA := freeTCPPort(t)
	cfgA := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     portA,
		GossipPeers:  true,
	}
	config.ApplyDefaults(cfgA)
	stA, err := store.NewStore(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer stA.Close()
	if err := stA.Set("gk", []byte("gv"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	svcA := NewService(cfgA, stA, nil, nil, "ga2")
	cfgB := &config.Config{SharedSecret: secret, ClientPort: 6378, PeerPort: freeTCPPort(t), GossipPeers: true}
	config.ApplyDefaults(cfgB)
	stB, err := store.NewStore(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	svcB := NewService(cfgB, stB, nil, nil, "gb2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svcA.Run(ctx) }()
	select {
	case <-svcA.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	ctxPull, cancelPull := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPull()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))
	if err := svcB.PullSnapshot(ctxPull, addr); err != nil {
		t.Fatal(err)
	}
	val, err := stB.Get("gk")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "gv" {
		t.Fatalf("got %q", val)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run timeout")
	}
}

func TestAddPeerValidationError(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("y", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	if err := svc.AddPeer("not-a-valid-addr"); err == nil {
		t.Fatal("expected error")
	}
}

func TestDrainReplicationOutboundDrainsBufferedPeer(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("z", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := NewService(cfg, st, nil, nil, "n1")
	ch := make(chan wireRepl, 2)
	ch <- wireRepl{Op: "SET", Key: "q", Value: []byte("1")}
	svc.mu.Lock()
	svc.out = append(svc.out, &outPeer{addr: "127.0.0.1:1", replCh: ch})
	svc.mu.Unlock()
	go func() {
		time.Sleep(20 * time.Millisecond)
		<-ch
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.DrainReplicationOutbound(ctx)
}

func TestWriteLineMarshalError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := writeLine(&buf, map[string]any{"ch": make(chan int)})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteMessageBodyTooLarge(t *testing.T) {
	t.Parallel()
	// Valid JSON string body large enough that the full framed marshal exceeds MaxPeerPayload.
	hugeStr := `"` + strings.Repeat("x", MaxPeerPayload+50) + `"`
	msg := PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: json.RawMessage(hugeStr)}
	err := WriteMessage(io.Discard, msg)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadPeerStateFileMissing(t *testing.T) {
	t.Parallel()
	_, err := LoadPeerStateFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil || !os.IsNotExist(err) {
		t.Fatalf("got %v", err)
	}
}

func TestReadLineCRLF(t *testing.T) {
	t.Parallel()
	line, err := readLine(bufio.NewReader(bytes.NewReader([]byte("hello\r\n"))))
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "hello" {
		t.Fatalf("got %q", line)
	}
}

func TestReplicateDropWhenQueueFull(t *testing.T) {
	secret := strings.Repeat("G", 32)
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)
	cfgA := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: portA}
	cfgB := &config.Config{
		SharedSecret:   secret,
		ClientPort:     6378,
		PeerPort:       portB,
		Peers:          []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))},
		PeerQueueDepth: 1,
	}
	config.ApplyDefaults(cfgA)
	config.ApplyDefaults(cfgB)
	stA, err := store.NewStore(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer stA.Close()
	stB, err := store.NewStore(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()
	svcA := NewService(cfgA, stA, nil, nil, "drop-a")
	svcB := NewService(cfgB, stB, nil, nil, "drop-b")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- svcA.Run(ctx) }()
	go func() { errB <- svcB.Run(ctx) }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		svcB.mu.RLock()
		n := len(svcB.out)
		svcB.mu.RUnlock()
		if n >= 1 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	svcB.mu.RLock()
	hasOut := len(svcB.out) >= 1
	svcB.mu.RUnlock()
	if !hasOut {
		t.Fatal("timeout waiting for outbound peer")
	}
	for i := 0; i < 400; i++ {
		_ = svcB.Replicate(ReplicatePayload{Op: "SET", Key: fmt.Sprintf("dropk%d", i), Value: []byte("v")})
	}
	cancel()
	select {
	case err := <-errA:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run A: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout A")
	}
	select {
	case err := <-errB:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run B: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout B")
	}
}

func TestOutboundReplWriterCancelDrainAndClosedChannel(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	op := &outPeer{
		nodeID: "n1",
		w:      bufio.NewWriter(c1),
		replCh: make(chan wireRepl, 2),
	}
	op.replCh <- wireRepl{Op: "SET", Key: "k", Value: []byte("v"), Seq: 1}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		outboundReplWriter(ctx, op)
		close(done)
	}()
	_ = c2.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 1024)
	_, _ = c2.Read(buf)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("outboundReplWriter did not exit after cancel")
	}

	op2 := &outPeer{
		nodeID: "n2",
		w:      bufio.NewWriter(io.Discard),
		replCh: make(chan wireRepl),
	}
	close(op2.replCh)
	outboundReplWriter(context.Background(), op2)
}
