// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for noticing replication events that never arrived.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import "testing"

func arrive(s *Service, origin string, seq uint64) {
	s.noteReplArrival(wireRepl{Op: "SET", Key: "k", Origin: origin, Seq: seq})
}

// TestContiguousSequenceReportsNothing is the ordinary case: every event arrives, so nothing is
// counted. A false positive here would cry wolf on a healthy cluster.
func TestContiguousSequenceReportsNothing(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	for seq := uint64(1); seq <= 100; seq++ {
		arrive(svc, "peer-a", seq)
	}
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("contiguous sequences must report no loss, got %d", got)
	}
	if got := m.late.Load(); got != 0 {
		t.Fatalf("contiguous sequences must report nothing late, got %d", got)
	}
}

// TestSkippedSequenceIsCounted is the whole point: events that never arrived are now visible
// rather than leaving the two nodes silently holding different data.
func TestSkippedSequenceIsCounted(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	arrive(svc, "peer-a", 1)
	arrive(svc, "peer-a", 2)
	arrive(svc, "peer-a", 7) // 3,4,5,6 never arrived
	if got := m.gapMissed.Load(); got != 4 {
		t.Fatalf("expected 4 missed events, got %d", got)
	}
	// Recovery continues from where the peer actually is, not from the lost range.
	arrive(svc, "peer-a", 8)
	if got := m.gapMissed.Load(); got != 4 {
		t.Fatalf("expected no further loss after resuming, got %d", got)
	}
}

// TestSeparateOriginsTrackedIndependently guards the per-peer accounting. Interleaved sequences
// from two peers would otherwise look like constant loss.
func TestSeparateOriginsTrackedIndependently(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	for seq := uint64(1); seq <= 20; seq++ {
		arrive(svc, "peer-a", seq)
		arrive(svc, "peer-b", seq)
	}
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("interleaved peers must not look like loss, got %d", got)
	}
}

// TestPeerRestartIsNotLoss covers a peer whose counter begins again from one. Treating that as a
// backwards jump would report a spurious gap on every restart in the fleet.
func TestPeerRestartIsNotLoss(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	for seq := uint64(1); seq <= 50; seq++ {
		arrive(svc, "peer-a", seq)
	}
	arrive(svc, "peer-a", 1) // restarted
	arrive(svc, "peer-a", 2)
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("a restart must not be reported as loss, got %d", got)
	}
	if got := m.late.Load(); got != 0 {
		t.Fatalf("a restart must not be reported as late, got %d", got)
	}
}

// TestLateEventCounted covers a straggler delivered by a link that is being replaced, which is
// deliberately kept distinct from loss: nothing is missing, it just arrived behind.
func TestLateEventCounted(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	arrive(svc, "peer-a", 10)
	arrive(svc, "peer-a", 11)
	arrive(svc, "peer-a", 9)
	if got := m.late.Load(); got != 1 {
		t.Fatalf("expected 1 late event, got %d", got)
	}
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("a late event is not loss, got %d", got)
	}
}

// TestFirstEventEstablishesBaseline confirms a peer's first event is never loss. Events sent
// before this node connected are the snapshot's job, not the sequence tracker's.
func TestFirstEventEstablishesBaseline(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	arrive(svc, "peer-a", 5000)
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("the first event from a peer cannot be loss, got %d", got)
	}
	arrive(svc, "peer-a", 5001)
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("expected no loss, got %d", got)
	}
}

// TestUntrackedEventsIgnored covers events that carry nothing to track, including this node's
// own writes echoed back, which must never be mistaken for a peer's.
func TestUntrackedEventsIgnored(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	svc.noteReplArrival(wireRepl{Op: "SET", Seq: 5})                          // no origin
	svc.noteReplArrival(wireRepl{Op: "SET", Origin: "peer-a"})                // no sequence
	svc.noteReplArrival(wireRepl{Op: "SET", Origin: svc.nodeID, Seq: 900})    // our own
	svc.noteReplArrival(wireRepl{Op: "SET", Origin: svc.nodeID, Seq: 100000}) // our own
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("untrackable events must be ignored, got %d", got)
	}
}
