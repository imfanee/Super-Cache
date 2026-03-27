// Tests for the management interface.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stubHandler struct{}

func (stubHandler) Ping() string { return "PONG" }

func (stubHandler) Info() map[string]any {
	return map[string]any{"test": true}
}

func TestMgmtPingRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mgmt.sock")
	secret := strings.Repeat("p", 32)

	srv := New(sock, "", secret, stubHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	var lastErr error
	for i := 0; i < 100; i++ {
		_, lastErr = ClientPing(context.Background(), "unix", sock, secret)
		if lastErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatal("dial mgmt:", lastErr)
	}

	out, err := ClientPing(context.Background(), "unix", sock, secret)
	if err != nil {
		t.Fatal(err)
	}
	if out != "PONG" {
		t.Fatalf("got %q", out)
	}

	m, err := ClientInfo(context.Background(), "unix", sock, secret)
	if err != nil {
		t.Fatal(err)
	}
	if m["test"] != true {
		t.Fatalf("info: %+v", m)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for mgmt Run")
	}
}

func TestMgmtBadSecret(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "m2.sock")
	secret := strings.Repeat("p", 32)

	srv := New(sock, "", secret, stubHandler{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Run(ctx) }()

	ready := false
	for i := 0; i < 100; i++ {
		_, err := ClientPing(context.Background(), "unix", sock, secret)
		if err == nil {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("mgmt server not up")
	}

	_, err := ClientPing(context.Background(), "unix", sock, secret+"x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad auth") {
		t.Fatalf("got %v", err)
	}
	cancel()
}
