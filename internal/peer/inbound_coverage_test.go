// Inbound handshake and post-handshake path coverage (deterministic TCP).
package peer

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

func TestInboundHandshakeWrongFirstFrame(t *testing.T) {
	secret := strings.Repeat("1", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	_, err = ReadMessage(br)
	if err != nil {
		t.Fatal(err)
	}
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

func TestInboundHandshakeInvalidHelloJSON(t *testing.T) {
	secret := strings.Repeat("2", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Valid JSON frame, but payload does not decode into wireHello (hits invalid HELLO json branch).
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: json.RawMessage(`[1,2,3]`)}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	_, _ = ReadMessage(br)
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

func TestInboundHandshakeWrongHelloOp(t *testing.T) {
	secret := strings.Repeat("3", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	h, err := json.Marshal(wireHello{Op: "BYE", Ver: PeerProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: h}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(c)
	_, _ = ReadMessage(br)
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

func TestInboundHandshakeSecondFrameNotAuth(t *testing.T) {
	secret := strings.Repeat("4", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHello, Payload: hPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = ReadMessage(br)
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

func TestInboundBootstrapRequestAfterAuth(t *testing.T) {
	secret := strings.Repeat("5", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	if err := st.Set("snapk", []byte("snapv"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	reqPayload, err := json.Marshal(wireRepl{Op: wireOpBootstrap})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeBootstrapReq, Payload: reqPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var sawChunk bool
	for i := 0; i < 10; i++ {
		m, err := ReadMessage(br)
		if err != nil {
			break
		}
		m = NormalizePeerMessage(m)
		if m.Type == MsgTypeBootstrapChunk {
			sawChunk = true
		}
		if m.Type == MsgTypeBootstrapDone {
			break
		}
	}
	if !sawChunk {
		t.Fatal("expected bootstrap chunk")
	}
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

func TestInboundReplicateFrameBootstrapOp(t *testing.T) {
	secret := strings.Repeat("8", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	if err := st.Set("bk", []byte("bv"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	bootPayload, err := json.Marshal(wireRepl{Op: wireOpBootstrap})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: bootPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var saw bool
	for i := 0; i < 10; i++ {
		m, err := ReadMessage(br)
		if err != nil {
			break
		}
		m = NormalizePeerMessage(m)
		if m.Type == MsgTypeBootstrapChunk {
			saw = true
		}
		if m.Type == MsgTypeBootstrapDone {
			break
		}
	}
	if !saw {
		t.Fatal("expected bootstrap chunk")
	}
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

func TestInboundHeartbeatAckPath(t *testing.T) {
	secret := strings.Repeat("6", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	ackp, err := json.Marshal(wireHeartbeat{Op: wireOpHeartbeatAck})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeHeartbeat, Payload: ackp}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
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

func TestInboundHandshakeAuthWrongProofOp(t *testing.T) {
	secret := strings.Repeat("9", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	var helloAck wireHelloAck
	if err := json.Unmarshal(msg.Payload, &helloAck); err != nil {
		t.Fatal(err)
	}
	nonce, err := hex.DecodeString(helloAck.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	proofPayload, err := json.Marshal(wireAuthProof{Op: "NOPE", Ver: PeerProtocolVersion, Hmac: peerHMACHex(secret, nonce)})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proofPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = ReadMessage(br)
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

func TestInboundHandshakeAuthBadHMAC(t *testing.T) {
	secret := strings.Repeat("0", 32)
	cfg := &config.Config{SharedSecret: secret, ClientPort: 6379, PeerPort: 0}
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
	svc := NewService(cfg, st, nil, nil, "n1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()
	select {
	case <-svc.ListenReady():
	case <-time.After(2 * time.Second):
		t.Fatal("ListenReady")
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.PeerPort)), time.Second)
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
	var helloAck wireHelloAck
	if err := json.Unmarshal(msg.Payload, &helloAck); err != nil {
		t.Fatal(err)
	}
	proofPayload, err := json.Marshal(wireAuthProof{Op: wireOpAuth, Ver: PeerProtocolVersion, Hmac: "deadbeef"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: proofPayload}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = ReadMessage(br)
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
