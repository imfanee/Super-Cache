// Tests for the peer subsystem.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

func TestPeerManagerStub(t *testing.T) {
	_ = &PeerManager{}
}

// testPeerMetrics counts outbound peers for synchronization in integration tests.
type testPeerMetrics struct {
	outbound atomic.Int64
}

func (m *testPeerMetrics) SetReplicationStats(_ int, outboundAddrs []string) {
	m.outbound.Store(int64(len(outboundAddrs)))
}

func (m *testPeerMetrics) SetBootstrapInboundQueueDepth(_ int) {}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestPeerAuthFailureLogDoesNotLeakSharedSecret triggers a version-mismatch HELLO and asserts
// slog output never contains the configured shared secret (P0.4 logging audit).
func TestPeerAuthFailureLogDoesNotLeakSharedSecret(t *testing.T) {
	secret := "NEVER_LOG_THIS_SECRET_TOKEN_12345"
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

	var buf strings.Builder
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))
	prev := slog.Default()
	slog.SetDefault(log)
	defer slog.SetDefault(prev)

	svc := NewService(cfg, st, nil, nil, "test-node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

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

	hPayload, err := json.Marshal(wireHello{Op: wireOpHello, Ver: 99999})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	_, _ = ReadMessage(br)

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("log leaked shared secret: %q", out)
	}
	if !strings.Contains(out, "peer authentication failed") && !strings.Contains(out, `"remote"`) {
		t.Fatalf("expected auth failure log, got: %q", out)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for peer Run")
	}
}

func TestInboundAuthAndApply(t *testing.T) {
	secret := strings.Repeat("b", 32)
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

	svc := NewService(cfg, st, nil, nil, "test-node")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

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
		t.Fatalf("expected HELLO_ACK frame, got %q", msg.Type)
	}
	var helloAck wireHelloAck
	if err := json.Unmarshal(msg.Payload, &helloAck); err != nil {
		t.Fatal(err)
	}
	if !helloAck.OK || helloAck.Nonce == "" {
		t.Fatalf("HELLO_ACK failed: %+v", helloAck)
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
	msg2, err := ReadMessage(br)
	if err != nil {
		t.Fatal(err)
	}
	msg2 = NormalizePeerMessage(msg2)
	if msg2.Type != MsgTypeAck {
		t.Fatalf("expected ACK frame, got %q", msg2.Type)
	}
	var ack wireAck
	if err := json.Unmarshal(msg2.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if !ack.OK {
		t.Fatalf("auth failed: %+v", ack)
	}

	repl := wireRepl{Op: "SET", Key: "k", Value: []byte("v")}
	replPayload, err := json.Marshal(repl)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: replPayload}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	var val []byte
	for time.Now().Before(deadline) {
		var err error
		val, err = st.Get("k")
		if err != nil {
			t.Fatal(err)
		}
		if string(val) == "v" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if string(val) != "v" {
		t.Fatalf("got %q want v", val)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for peer Run")
	}
}

// TestTwoNodeOutboundReplicate starts two peer services: B dials A and Replicate on B
// applies the mutation on A's store.
func TestTwoNodeOutboundReplicate(t *testing.T) {
	secret := strings.Repeat("b", 32)
	portA := freeTCPPort(t)
	portB := freeTCPPort(t)

	cfgA := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     portA,
	}
	cfgB := &config.Config{
		SharedSecret: secret,
		ClientPort:   6378,
		PeerPort:     portB,
		Peers:        []string{net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))},
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

	metricsB := &testPeerMetrics{}
	svcA := NewService(cfgA, stA, nil, nil, "node-a")
	svcB := NewService(cfgB, stB, metricsB, nil, "node-b")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- svcA.Run(ctx) }()
	go func() { errB <- svcB.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for metricsB.outbound.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if metricsB.outbound.Load() < 1 {
		t.Fatal("timeout waiting for outbound peer connection from B to A")
	}

	payload := ReplicatePayload{Op: "SET", Key: "replkey", Value: []byte("replval")}
	if err := svcB.Replicate(payload); err != nil {
		t.Fatalf("Replicate: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	var val []byte
	for time.Now().Before(deadline) {
		val, err = stA.Get("replkey")
		if err != nil {
			t.Fatal(err)
		}
		if string(val) == "replval" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if string(val) != "replval" {
		t.Fatalf("peer A store: got %q want replval", val)
	}

	cancel()
	select {
	case err := <-errA:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run A: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for peer Run A")
	}
	select {
	case err := <-errB:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run B: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for peer Run B")
	}
}

func TestBootstrapPullSnapshot(t *testing.T) {
	secret := strings.Repeat("b", 32)
	portA := freeTCPPort(t)

	cfgA := &config.Config{
		SharedSecret: secret,
		ClientPort:   6379,
		PeerPort:     portA,
	}
	config.ApplyDefaults(cfgA)

	stA, err := store.NewStore(cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer stA.Close()

	if err := stA.Set("bootk", []byte("bootv"), time.Time{}); err != nil {
		t.Fatal(err)
	}

	svcA := NewService(cfgA, stA, nil, nil, "node-a")

	cfgB := &config.Config{
		SharedSecret: secret,
		ClientPort:   6378,
		PeerPort:     freeTCPPort(t),
	}
	config.ApplyDefaults(cfgB)

	stB, err := store.NewStore(cfgB)
	if err != nil {
		t.Fatal(err)
	}
	defer stB.Close()

	svcB := NewService(cfgB, stB, nil, nil, "node-b")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- svcA.Run(ctx) }()

	dialAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(portA))
	for i := 0; i < 100; i++ {
		c, err := net.DialTimeout("tcp", dialAddr, 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if i == 99 {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := svcB.PullSnapshot(ctx, dialAddr); err != nil {
		t.Fatalf("PullSnapshot: %v", err)
	}

	val, err := stB.Get("bootk")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "bootv" {
		t.Fatalf("got %q want bootv", val)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for peer Run")
	}
}

func TestWriteMessageReadMessageRoundTrip(t *testing.T) {
	t.Parallel()
	orig := PeerMessage{
		Version:   1,
		Type:      MsgTypeReplicate,
		NodeID:    "n1",
		SeqNum:    42,
		Timestamp: 7,
		Payload:   json.RawMessage(`{"op":"SET","key":"k"}`),
	}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, orig); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != orig.Type || got.NodeID != orig.NodeID || got.SeqNum != orig.SeqNum || got.Timestamp != orig.Timestamp {
		t.Fatalf("mismatch: %+v vs %+v", got, orig)
	}
	if string(got.Payload) != string(orig.Payload) {
		t.Fatalf("payload %s want %s", got.Payload, orig.Payload)
	}
}

func TestReadMessageInvalidMagic(t *testing.T) {
	t.Parallel()
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], 0xdeadbeef)
	binary.BigEndian.PutUint32(hdr[4:8], 2)
	r := bytes.NewReader(append(hdr[:], []byte(`{}`)...))
	_, err := ReadMessage(r)
	if err == nil || !errors.Is(err, ErrInvalidMagic) {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}
}

func TestReadMessagePayloadTooLarge(t *testing.T) {
	t.Parallel()
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[0:4], PeerMagic)
	binary.BigEndian.PutUint32(hdr[4:8], MaxPeerPayload+1)
	r := bytes.NewReader(hdr[:])
	_, err := ReadMessage(r)
	if err == nil || strings.Contains(err.Error(), "invalid magic") {
		t.Fatalf("expected oversize error, got %v", err)
	}
}

func TestReadMessagePartialChunks(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	orig := PeerMessage{Version: 1, Type: MsgTypeHello, Payload: json.RawMessage(`{"op":"HELLO","ver":2}`)}
	var buf bytes.Buffer
	if err := WriteMessage(&buf, orig); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	go func() {
		for _, b := range data {
			if _, err := pw.Write([]byte{b}); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	got, err := ReadMessage(pr)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != MsgTypeHello {
		t.Fatalf("got %q", got.Type)
	}
}

func TestPeerManagerConcurrentReplication(t *testing.T) {
	secret := strings.Repeat("c", 32)
	cfg := &config.Config{SharedSecret: secret}
	config.ApplyDefaults(cfg)
	st2, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()

	c1, c2 := net.Pipe()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var writeErr error
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < 125; i++ {
				idx := base*125 + i
				wr := wireRepl{Op: "SET", Key: fmt.Sprintf("pk%d", idx), Value: []byte("v")}
				payload, err := json.Marshal(wr)
				if err != nil {
					mu.Lock()
					writeErr = err
					mu.Unlock()
					return
				}
				msg := PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: payload}
				mu.Lock()
				err = WriteMessage(c2, msg)
				mu.Unlock()
				if err != nil {
					mu.Lock()
					writeErr = err
					mu.Unlock()
					return
				}
			}
		}(g)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 1000; i++ {
			msg, err := ReadMessage(c1)
			if err != nil {
				done <- err
				return
			}
			var wr wireRepl
			if err := json.Unmarshal(msg.Payload, &wr); err != nil {
				done <- err
				return
			}
			if err := ApplyReplicatePayload(st2, ReplicatePayload{Op: wr.Op, Key: wr.Key, Value: wr.Value}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	wg.Wait()
	_ = c2.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for replication reader")
	}
	if st2.DBSize() != 1000 {
		t.Fatalf("db size %d", st2.DBSize())
	}
}
