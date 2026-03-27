// Direct coverage for handshakeOut error paths (stub TCP peer).
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

func TestHandshakeOutWrongFirstFrame(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("A", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeReplicate, Payload: json.RawMessage(`{}`)})
	}()

	if _, err := svc.handshakeOut(c); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshakeOutHelloAckDenied(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("B", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false, Err: "go away"})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
	}()

	if _, err := svc.handshakeOut(c); err == nil || !strings.Contains(err.Error(), "go away") {
		t.Fatalf("got %v", err)
	}
}

func TestHandshakeOutInvalidNonceHex(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("C", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: true, Ver: PeerProtocolVersion, Nonce: "zz"})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
	}()

	if _, err := svc.handshakeOut(c); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshakeOutHelloAckFailedNoMessage(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("E", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: false})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
	}()

	if _, err := svc.handshakeOut(c); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshakeOutAuthAckInvalidJSON(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("F", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		nonce := make([]byte, 16)
		for i := range nonce {
			nonce[i] = byte(i + 1)
		}
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: true, Ver: PeerProtocolVersion, Nonce: hex.EncodeToString(nonce)})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		_, _ = ReadMessage(br)
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: json.RawMessage(`[1]`)})
	}()

	if _, err := svc.handshakeOut(c); err == nil {
		t.Fatal("expected error")
	}
}

func TestHandshakeOutAuthAckRejected(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("D", 32)}
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
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		accepted <- c
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := <-accepted
	defer srv.Close()

	go func() {
		br := bufio.NewReader(srv)
		_, _ = ReadMessage(br)
		nonce := make([]byte, 16)
		for i := range nonce {
			nonce[i] = byte(i)
		}
		p, _ := json.Marshal(wireHelloAck{Op: wireOpHelloAck, OK: true, Ver: PeerProtocolVersion, Nonce: hex.EncodeToString(nonce)})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeHelloAck, Payload: p})
		_, _ = ReadMessage(br)
		badAck, _ := json.Marshal(wireAck{OK: false, Err: "nope"})
		_ = WriteMessage(srv, PeerMessage{Version: 1, Type: MsgTypeAck, Payload: badAck})
	}()

	if _, err := svc.handshakeOut(c); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestInboundHandshakeAuthInvalidProofJSON(t *testing.T) {
	secret := strings.Repeat("7", 32)
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
	if err := WriteMessage(c, PeerMessage{Version: 1, Type: MsgTypeAuth, Payload: json.RawMessage(`[1,2]`)}); err != nil {
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
