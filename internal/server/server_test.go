// Tests for the Super-Cache server package.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"bytes"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

func TestNewNilConfig(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunCancel(t *testing.T) {
	c := &config.Config{SharedSecret: strings.Repeat("a", 32)}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestPingOverTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()
	if peerPort == port {
		t.Fatal("could not allocate distinct client and peer ports")
	}

	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.ClientPort = port
	c.PeerPort = peerPort
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	dial, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal("dial server:", err)
	}
	_, err = dial.Write([]byte("*1\r\n$4\r\nPING\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := dial.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(buf[:n]), "+PONG") {
		t.Fatalf("expected PONG, got %q", buf[:n])
	}
	_ = dial.Close()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func BenchmarkServerParallelSetGet(b *testing.B) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()
	if peerPort == port {
		b.Fatal("ports")
	}
	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = d.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.ReportAllocs()
	b.SetParallelism(runtime.NumCPU())
	var seq atomic.Int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			b.Fatal(err)
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for pb.Next() {
			n := seq.Add(1)
			key := fmt.Sprintf("bk%d", n)
			set := fmt.Sprintf("*3\r\n$3\r\nSET\r\n$%d\r\n%s\r\n$1\r\nv\r\n", len(key), key)
			if _, err := io.WriteString(conn, set); err != nil {
				b.Fatal(err)
			}
			if _, err := br.ReadSlice('\n'); err != nil {
				b.Fatal(err)
			}
			get := fmt.Sprintf("*2\r\n$3\r\nGET\r\n$%d\r\n%s\r\n", len(key), key)
			if _, err := io.WriteString(conn, get); err != nil {
				b.Fatal(err)
			}
			if _, err := br.ReadSlice('\n'); err != nil {
				b.Fatal(err)
			}
			if _, err := br.ReadSlice('\n'); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.StopTimer()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		b.Fatal("Run did not exit")
	}
}

func TestServerGoroutineCleanup(t *testing.T) {
	before := runtime.NumGoroutine()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()
	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	time.Sleep(100 * time.Millisecond)
	var conns []net.Conn
	for i := 0; i < 20; i++ {
		d, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, d)
	}
	buf := make([]byte, 64)
	for _, d := range conns {
		if _, err := d.Write([]byte("*1\r\n$4\r\nPING\r\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Read(buf); err != nil {
			t.Fatal(err)
		}
		_ = d.Close()
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}
	time.Sleep(2 * time.Second)
	after := runtime.NumGoroutine()
	if after > before+10 {
		t.Fatalf("possible goroutine leak: before %d after %d", before, after)
	}
}

func TestServerShutdownUnderLoad(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()
	if peerPort == port {
		t.Fatal("could not allocate distinct client and peer ports")
	}

	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.ClientPort = port
	c.PeerPort = peerPort
	c.MgmtSocket = "-"

	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.SetRunCancel(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	readyDeadline := time.Now().Add(3 * time.Second)
	var ready bool
	for time.Now().Before(readyDeadline) {
		d, derr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if derr == nil {
			_ = d.Close()
			ready = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("server did not become ready")
	}

	done := make(chan struct{})
	clientErrs := make(chan error, 128)
	readyCh := make(chan struct{}, 20)
	var clientsWG sync.WaitGroup
	for i := 0; i < 20; i++ {
		clientsWG.Add(1)
		go func() {
			defer clientsWG.Done()
			conn, derr := net.DialTimeout("tcp", addr, 2*time.Second)
			if derr != nil {
				clientErrs <- derr
				return
			}
			defer conn.Close()
			readyCh <- struct{}{}
			br := bufio.NewReader(conn)
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, werr := conn.Write([]byte("*1\r\n$4\r\nPING\r\n")); werr != nil {
					if !errors.Is(werr, io.EOF) && !errors.Is(werr, io.ErrUnexpectedEOF) {
						clientErrs <- werr
					}
					return
				}
				_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
				line, rerr := br.ReadString('\n')
				if rerr != nil {
					if !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
						clientErrs <- rerr
					}
					return
				}
				if strings.Contains(strings.ToUpper(line), "LOADING SERVER IS SHUTTING DOWN") {
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		select {
		case <-readyCh:
		case <-time.After(3 * time.Second):
			t.Fatal("clients did not all connect before shutdown")
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	start := time.Now()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	if elapsed >= 10*time.Second {
		t.Fatalf("shutdown exceeded 10s: %v", elapsed)
	}

	time.Sleep(100 * time.Millisecond)
	if d, derr := net.DialTimeout("tcp", addr, 200*time.Millisecond); derr == nil {
		_ = d.Close()
		t.Fatal("listener still accepts connections after shutdown")
	}
	close(done)
	clientsWG.Wait()
	close(clientErrs)
	for e := range clientErrs {
		var opErr *net.OpError
		if errors.As(e, &opErr) {
			var errno syscall.Errno
			if errors.As(opErr.Err, &errno) && errno == syscall.ECONNRESET {
				t.Fatalf("unexpected ECONNRESET during shutdown: %v", e)
			}
		}
	}
	if !strings.Contains(logBuf.String(), "Shutdown initiated") {
		t.Fatalf("expected shutdown initiation log, got %q", logBuf.String())
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}
}

func TestServerShutdownLogsEmitted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	pln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peerPort := pln.Addr().(*net.TCPAddr).Port
	_ = pln.Close()
	c := &config.Config{SharedSecret: strings.Repeat("a", 32), ClientPort: port, PeerPort: peerPort}
	config.ApplyDefaults(c)
	c.MgmtSocket = "-"
	srv, err := New(c)
	if err != nil {
		t.Fatal(err)
	}

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prevLogger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.SetRunCancel(cancel)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, derr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if derr == nil {
			_, _ = d.Write([]byte("*1\r\n$4\r\nPING\r\n"))
			_ = d.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit")
	}

	logText := logBuf.String()
	if !strings.Contains(logText, "Shutdown") {
		t.Fatalf("expected shutdown logs, got %q", logText)
	}
	if !strings.Contains(logText, "Shutdown initiated") || !strings.Contains(logText, "Shutdown complete") {
		t.Fatalf("expected initiation and completion logs, got %q", logText)
	}
}
