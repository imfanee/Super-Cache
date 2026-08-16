// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for single-encode replication fan-out and undelivered-event accounting.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

type countingMetrics struct {
	dropped    atomic.Int64
	sendErrors atomic.Int64
	gapMissed  atomic.Int64
	late       atomic.Int64
	lost       atomic.Int64
}

func (c *countingMetrics) SetReplicationStats(int, []string) {}
func (c *countingMetrics) SetBootstrapInboundQueueDepth(int) {}
func (c *countingMetrics) AddReplicationDropped(n int64)     { c.dropped.Add(n) }
func (c *countingMetrics) AddReplicationSendError(n int64)   { c.sendErrors.Add(n) }
func (c *countingMetrics) AddReplicationGap(n int64)         { c.gapMissed.Add(n) }
func (c *countingMetrics) AddReplicationLate(n int64)        { c.late.Add(n) }
func (c *countingMetrics) AddReplicationLost(n int64)        { c.lost.Add(n) }

func newFrameTestService(t *testing.T, metrics PeerMetrics) *Service {
	t.Helper()
	cfg := &config.Config{SharedSecret: strings.Repeat("z", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewService(cfg, st, metrics, nil, "node-under-test")
}

// TestEncodeMessageRoundTrips confirms the pre-encoded bytes are the same wire format
// WriteMessage produces, since peers decode them with no knowledge of how they were built.
func TestEncodeMessageRoundTrips(t *testing.T) {
	msg := PeerMessage{Version: 1, Type: MsgTypeReplicate, NodeID: "n1", SeqNum: 7,
		Payload: json.RawMessage(`{"op":"SET","key":"k"}`)}
	data, err := EncodeMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	var streamed bytes.Buffer
	if err := WriteMessage(&streamed, msg); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, streamed.Bytes()) {
		t.Fatal("EncodeMessage and WriteMessage must produce identical bytes")
	}
	got, err := ReadMessage(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != MsgTypeReplicate || got.NodeID != "n1" || got.SeqNum != 7 {
		t.Fatalf("round trip lost fields: %+v", got)
	}
}

func TestEncodeMessageRejectsOversizePayload(t *testing.T) {
	huge := make([]byte, MaxPeerPayload+1)
	for i := range huge {
		huge[i] = 'a'
	}
	body, err := json.Marshal(string(huge))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EncodeMessage(PeerMessage{Version: 1, Payload: body}); err == nil {
		t.Fatal("expected oversize payload to be rejected")
	}
}

// TestReplicateEncodesOnceForAllPeers is the point of the change: every link must receive the
// very same bytes, not a separately marshalled copy.
func TestReplicateEncodesOnceForAllPeers(t *testing.T) {
	svc := newFrameTestService(t, nil)
	var links []*outPeer
	for i, id := range []string{"peer-1", "peer-2", "peer-3"} {
		l := &outPeer{addr: "10.0.0." + string(rune('1'+i)) + ":7379", remoteID: id, replCh: make(chan *replFrame, 4)}
		links = append(links, l)
		svc.registerOut(l)
	}
	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	var first *replFrame
	for i, l := range links {
		select {
		case f := <-l.replCh:
			if i == 0 {
				first = f
				continue
			}
			if f != first {
				t.Fatal("each peer received a separate frame; encoding cannot be shared")
			}
		default:
			t.Fatalf("link %d received nothing", i)
		}
	}
	// One encode serves every link: the bytes handed out are the same backing array.
	a, err := first.bytes()
	if err != nil {
		t.Fatal(err)
	}
	b, err := first.bytes()
	if err != nil {
		t.Fatal(err)
	}
	if &a[0] != &b[0] {
		t.Fatal("frame re-encoded on second use")
	}
}

// TestReplicateCountsDroppedEvents covers the accounting that makes divergence visible. Without
// it a full queue silently discards a write and nothing downstream ever reports the loss.
func TestReplicateCountsDroppedEvents(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	// Capacity of one, already occupied, so the next event has nowhere to go.
	full := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan *replFrame, 1), metrics: m}
	svc.registerOut(full)
	for i := 0; i < 3; i++ {
		if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k", Value: []byte("v")}); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.dropped.Load(); got != 2 {
		t.Fatalf("expected 2 dropped events counted, got %d", got)
	}
}

func TestReplicateWithNoTargetsIsNotAnError(t *testing.T) {
	svc := newFrameTestService(t, nil)
	if err := svc.Replicate(ReplicatePayload{Op: "SET", Key: "k"}); err != nil {
		t.Fatalf("a standalone node should replicate to nobody without error: %v", err)
	}
}

// TestFlushCountsSendErrors covers the other silent-loss path: the frame left the queue but the
// socket write failed, so the peer never saw it.
func TestFlushCountsSendErrors(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	f := svc.buildReplFrame(wireRepl{Op: "SET", Key: "k", Seq: 1})
	op := &outPeer{addr: "10.0.0.9:7379", w: bufio.NewWriter(failWriteOnce{}), metrics: m}
	flushOutboundReplLine(op, f)
	if got := m.sendErrors.Load(); got != 1 {
		t.Fatalf("expected 1 send error counted, got %d", got)
	}
}

// BenchmarkReplicateFanOut measures the work a single write costs as peer count grows. Encoding
// per peer made this scale with the number of peers; encoding once should keep it flat.
func BenchmarkReplicateFanOut(b *testing.B) {
	for _, peers := range []int{1, 10, 30} {
		b.Run(strconv.Itoa(peers)+"peers", func(b *testing.B) {
			cfg := &config.Config{SharedSecret: strings.Repeat("z", 32)}
			config.ApplyDefaults(cfg)
			st, err := store.NewStore(cfg)
			if err != nil {
				b.Fatal(err)
			}
			defer st.Close()
			svc := NewService(cfg, st, nil, nil, "bench")
			for i := 0; i < peers; i++ {
				// Deep queues so the benchmark measures encoding and fan-out, not backpressure.
				svc.registerOut(&outPeer{
					addr:     "10.0.0.1:" + strconv.Itoa(7000+i),
					remoteID: "peer-" + strconv.Itoa(i),
					replCh:   make(chan *replFrame, 1<<20),
				})
			}
			payload := ReplicatePayload{Op: "SET", Key: "some/reasonably/long/key", Value: bytes.Repeat([]byte("v"), 256)}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := svc.Replicate(payload); err != nil {
					b.Fatal(err)
				}
				// Drain as the writers would, encoding on first use, so the benchmark counts
				// the same total work a real fan-out performs.
				svc.mu.RLock()
				for _, l := range svc.out {
					select {
					case f := <-l.replCh:
						if _, err := f.bytes(); err != nil {
							b.Fatal(err)
						}
					default:
					}
				}
				svc.mu.RUnlock()
			}
		})
	}
}

// TestReplicationTargetsCacheInvalidates guards the cached link selection. A stale cache would
// keep replicating to a link that is gone, or miss one that just appeared.
func TestReplicationTargetsCacheInvalidates(t *testing.T) {
	svc := newFrameTestService(t, nil)
	if got := svc.replicationTargets(); len(got) != 0 {
		t.Fatalf("expected no targets initially, got %d", len(got))
	}
	a := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan *replFrame, 1)}
	svc.registerOut(a)
	if got := svc.replicationTargets(); len(got) != 1 || got[0] != a {
		t.Fatalf("registering a link must invalidate the cache, got %v", got)
	}
	b := &outPeer{addr: "10.0.0.2:7379", remoteID: "peer-2", replCh: make(chan *replFrame, 1)}
	svc.registerOut(b)
	if got := svc.replicationTargets(); len(got) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(got))
	}
	svc.unregisterLink(a)
	got := svc.replicationTargets()
	if len(got) != 1 || got[0] != b {
		t.Fatalf("removing a link must invalidate the cache, got %v", got)
	}
}

// TestReplicationTargetsCacheReused confirms the selection is not rebuilt per write, which was
// the point of caching it.
func TestReplicationTargetsCacheReused(t *testing.T) {
	svc := newFrameTestService(t, nil)
	svc.registerOut(&outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan *replFrame, 1)})
	first := svc.replicationTargets()
	second := svc.replicationTargets()
	if &first[0] != &second[0] {
		t.Fatal("expected the cached selection to be reused between calls")
	}
}

// TestReplicationTargetsStillDeduplicates keeps the property the cache must not break: two links
// to one node deliver a write once, or every non-idempotent operation corrupts.
func TestReplicationTargetsStillDeduplicates(t *testing.T) {
	svc := newFrameTestService(t, nil)
	outbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", replCh: make(chan *replFrame, 1)}
	inbound := &outPeer{addr: "10.0.0.1:7379", remoteID: "peer-1", inbound: true, replCh: make(chan *replFrame, 1)}
	svc.registerOut(outbound)
	svc.registerOut(inbound)
	got := svc.replicationTargets()
	if len(got) != 1 || got[0] != outbound {
		t.Fatalf("two links to one node must yield one target, preferring the dial: %v", got)
	}
}
