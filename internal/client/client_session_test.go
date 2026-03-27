// Session and subscription manager tests.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package client

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/resp"
)

func testSessionWithBuffer(t *testing.T) *Session {
	t.Helper()
	var buf bytes.Buffer
	w := resp.NewWriter(&buf)
	return &Session{
		ID:           1,
		Writer:       w,
		SubChannels:  make(map[string]bool),
		PSubPatterns: make(map[string]bool),
		WatchedKeys:  make(map[string]uint64),
		CreatedAt:    time.Now(),
		LastCmdAt:    time.Now(),
	}
}

func TestSubscriptionManagerPublish(t *testing.T) {
	s := testSessionWithBuffer(t)
	m := NewSubscriptionManager()
	m.Subscribe("ch", s)
	n := m.Publish("ch", []byte("hello"))
	if n != 1 {
		t.Fatal(n)
	}
	m.PSubscribe("p*", s)
	n = m.Publish("p1", []byte("x"))
	if n != 1 {
		t.Fatal(n)
	}
	m.RemoveSessionFromAll(s)
}

func TestSessionPubSubPush(t *testing.T) {
	s := testSessionWithBuffer(t)
	if err := s.pushPubSubMessage("c", []byte("m")); err != nil {
		t.Fatal(err)
	}
	if err := s.pushPMessage("p*", "c", []byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestInSubscribeMode(t *testing.T) {
	s := testSessionWithBuffer(t)
	if s.InSubscribeMode() {
		t.Fatal()
	}
	s.SubChannels["c"] = true
	if !s.InSubscribeMode() {
		t.Fatal()
	}
}

func TestSessionIDSequential(t *testing.T) {
	var id int64
	for i := 0; i < 50; i++ {
		c1, c2 := net.Pipe()
		s := NewSession(atomic.AddInt64(&id, 1), c1, resp.NewParser(c1), resp.NewWriter(c1))
		if s.ID != int64(i+1) {
			t.Fatal(s.ID)
		}
		_ = c2.Close()
		_ = c1.Close()
	}
}

func TestSessionIDConcurrent(t *testing.T) {
	var seq atomic.Int64
	var seen sync.Map
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 5; i++ {
				c1, c2 := net.Pipe()
				n := seq.Add(1)
				s := NewSession(n, c1, resp.NewParser(c1), resp.NewWriter(c1))
				if _, loaded := seen.LoadOrStore(s.ID, true); loaded {
					t.Error("dup id", s.ID)
				}
				_ = c2.Close()
				_ = c1.Close()
			}
		}()
	}
	wg.Wait()
}

func TestCmdCountAndLastCmdAt(t *testing.T) {
	s := testSessionWithBuffer(t)
	if s.CmdCount != 0 {
		t.Fatal()
	}
	s.LastCmdAt = s.CreatedAt.Add(-time.Hour)
	s.CmdCount = 5
	if s.CmdCount != 5 {
		t.Fatal()
	}
}
