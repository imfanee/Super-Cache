// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for telling real replication loss apart from events that merely arrived late.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"sync/atomic"
	"testing"
	"time"
)

type recordingResync struct {
	calls   atomic.Int64
	reasons chan string
}

func newRecordingResync() *recordingResync {
	return &recordingResync{reasons: make(chan string, 8)}
}

func (r *recordingResync) RequestResync(reason string) {
	r.calls.Add(1)
	select {
	case r.reasons <- reason:
	default:
	}
}

// TestStragglerCancelsTheGap is the reason loss is not acted on immediately. Replacing one link
// to a peer with another can deliver an event from the old link behind the new one, which looks
// exactly like a gap until it arrives.
func TestStragglerCancelsTheGap(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 4, 7) // 5 and 6 missing
	g.arrived("peer-a", 5)
	g.arrived("peer-a", 6)
	if got := g.expired(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("events that arrived late are not loss, got %v", got)
	}
}

// TestUnfilledGapIsLoss is the other half: what never arrives is real.
func TestUnfilledGapIsLoss(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 4, 8) // 5,6,7 missing
	g.arrived("peer-a", 6)
	got := g.expired(time.Now().Add(time.Hour))
	if got["peer-a"] != 2 {
		t.Fatalf("expected 2 events confirmed lost, got %v", got)
	}
}

// TestGapNotReportedBeforeGrace confirms nothing is judged until the grace period is up.
func TestGapNotReportedBeforeGrace(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 1, 4)
	if got := g.expired(time.Now()); len(got) != 0 {
		t.Fatalf("a gap must not be judged immediately, got %v", got)
	}
}

// TestLossReportedOnlyOnce keeps a single loss from triggering a refetch on every sweep.
func TestLossReportedOnlyOnce(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 1, 4)
	if got := g.expired(time.Now().Add(time.Hour)); len(got) != 1 {
		t.Fatalf("expected the loss to be reported, got %v", got)
	}
	if got := g.expired(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("expected the same loss not to be reported again, got %v", got)
	}
}

// TestHugeJumpIsLossImmediately covers a gap far too large for reordering, where waiting would
// only delay a refetch that is certainly needed.
func TestHugeJumpIsLossImmediately(t *testing.T) {
	g := newGapTracker()
	if !g.note("peer-a", 1, gapTrackLimit+50) {
		t.Fatal("a jump beyond the tracking limit should be treated as loss at once")
	}
}

func TestGapTrackerSeparatesOrigins(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 1, 3)
	g.note("peer-b", 1, 3)
	g.arrived("peer-a", 2)
	got := g.expired(time.Now().Add(time.Hour))
	if got["peer-a"] != 0 || got["peer-b"] != 1 {
		t.Fatalf("origins must be judged independently, got %v", got)
	}
}

func TestGapTrackerResetClearsEverything(t *testing.T) {
	g := newGapTracker()
	g.note("peer-a", 1, 5)
	g.reset()
	if got := g.expired(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("reset must discard pending gaps, got %v", got)
	}
}

// TestConfirmedLossRequestsResync joins the halves: a gap nothing fills eventually asks for a
// fresh copy of the dataset.
func TestConfirmedLossRequestsResync(t *testing.T) {
	svc := newFrameTestService(t, nil)
	r := newRecordingResync()
	svc.SetResyncRequester(r)

	arrive(svc, "peer-a", 1)
	arrive(svc, "peer-a", 9) // 2..8 skipped

	// Age the pending entries so the sweep judges them without waiting out the real grace.
	svc.gaps.mu.Lock()
	for _, m := range svc.gaps.missing {
		for seq := range m {
			m[seq] = time.Now().Add(-time.Minute).UnixMilli()
		}
	}
	svc.gaps.mu.Unlock()

	if lost := svc.gaps.expired(time.Now()); len(lost) == 0 {
		t.Fatal("expected the unfilled gap to be confirmed")
	}
	svc.confirmLoss("peer-a", 7)
	if r.calls.Load() == 0 {
		t.Fatal("confirmed loss must ask for a resync")
	}
}

// TestNoResyncRequesterIsSafe covers a service with no callback installed, which must simply
// count the loss rather than panic.
func TestNoResyncRequesterIsSafe(t *testing.T) {
	svc := newFrameTestService(t, nil)
	svc.confirmLoss("peer-a", 3)
}

// TestResetReplicationTrackingClearsPositions confirms a refetch starts from a clean slate. The
// sequence reached before a snapshot says nothing about the copy that replaced it, and keeping
// it would make the next arrival look like a fresh gap.
func TestResetReplicationTrackingClearsPositions(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	for seq := uint64(1); seq <= 20; seq++ {
		arrive(svc, "peer-a", seq)
	}
	svc.ResetReplicationTracking()

	// A far higher sequence is now a first sighting, not a gap.
	arrive(svc, "peer-a", 5000)
	if got := m.gapMissed.Load(); got != 0 {
		t.Fatalf("expected no loss reported after a reset, got %d", got)
	}
}

// TestObservedGapIsNotCountedAsLoss is the metric-semantics fix. A gap seen on arrival is not
// evidence of loss: concurrent writers allocate sequence numbers atomically but enqueue them
// independently, so a burst of parallel writes routinely arrives slightly out of order with
// nothing missing. Counting that as lost data made the metric fire constantly on a healthy
// cluster, which is worse than not having it.
func TestObservedGapIsNotCountedAsLoss(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)

	arrive(svc, "peer-a", 1)
	arrive(svc, "peer-a", 4) // 2 and 3 skipped
	if got := m.lost.Load(); got != 0 {
		t.Fatalf("an observed gap must not count as loss yet, got %d", got)
	}
	if got := m.gapMissed.Load(); got == 0 {
		t.Fatal("the gap itself should still be recorded for diagnostics")
	}

	// They turn up moments later, as reordering does.
	arrive(svc, "peer-a", 2)
	arrive(svc, "peer-a", 3)
	if got := svc.gaps.expired(time.Now().Add(time.Hour)); len(got) != 0 {
		t.Fatalf("stragglers should have cleared the gap, got %v", got)
	}
	if got := m.lost.Load(); got != 0 {
		t.Fatalf("nothing was lost, got %d", got)
	}
}

// TestConfirmedLossIsCounted is the other half: what genuinely never arrives is counted, and is
// what an operator should alert on.
func TestConfirmedLossIsCounted(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	svc.SetResyncRequester(newRecordingResync())

	arrive(svc, "peer-a", 1)
	arrive(svc, "peer-a", 5) // 2,3,4 never arrive

	// Age them past the grace period, then run one sweep as the watcher would.
	svc.gaps.mu.Lock()
	for _, mm := range svc.gaps.missing {
		for seq := range mm {
			mm[seq] = time.Now().Add(-time.Minute).UnixMilli()
		}
	}
	svc.gaps.mu.Unlock()

	lost := svc.gaps.expired(time.Now())
	total := 0
	for _, n := range lost {
		total += n
	}
	if total != 3 {
		t.Fatalf("expected 3 events confirmed lost, got %d", total)
	}
	svc.metrics.AddReplicationLost(int64(total))
	if got := m.lost.Load(); got != 3 {
		t.Fatalf("confirmed loss must be counted, got %d", got)
	}
}

// TestLargeJumpCountsAsLossImmediately covers the shortcut for a jump too big to be reordering.
func TestLargeJumpCountsAsLossImmediately(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	svc.confirmLoss("peer-a", 5000)
	if got := m.lost.Load(); got != 5000 {
		t.Fatalf("expected 5000 counted as lost, got %d", got)
	}
}
