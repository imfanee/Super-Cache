// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests that writes discarded during a snapshot pull are counted and reported, rather than
// leaving the node quietly short of data.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import "testing"

// TestBootstrapOverflowIsCounted is the gap this covers. A write that arrives during a snapshot
// pull and cannot be buffered is gone, but the sender queued and wrote it successfully, so no
// counter on its side moves. Before this the only trace was a log line.
func TestBootstrapOverflowIsCounted(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	svc.SetBootstrapInboundActive(true, 2)

	for i := 0; i < 5; i++ {
		if err := svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "k"}); err != nil {
			svc.noteBootstrapDrop("10.0.0.1:7379")
		}
	}

	if got := svc.BootstrapDropped(); got != 3 {
		t.Fatalf("expected 3 discarded writes (capacity 2 of 5), got %d", got)
	}
	if got := m.bootDropped.Load(); got != 3 {
		t.Fatalf("expected the metric to record 3, got %d", got)
	}
}

// TestBootstrapDropCountResetsPerAttempt matters because a failed attempt empties the store and
// starts again. Carrying a previous attempt's drops forward would fail a later attempt that
// buffered everything.
func TestBootstrapDropCountResetsPerAttempt(t *testing.T) {
	svc := newFrameTestService(t, &countingMetrics{})
	svc.SetBootstrapInboundActive(true, 1)
	_ = svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "a"})
	if err := svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "b"}); err != nil {
		svc.noteBootstrapDrop("10.0.0.1:7379")
	}
	if svc.BootstrapDropped() == 0 {
		t.Fatal("expected the first attempt to record a discard")
	}

	svc.SetBootstrapInboundActive(false, 0)
	svc.SetBootstrapInboundActive(true, 100)
	if got := svc.BootstrapDropped(); got != 0 {
		t.Fatalf("a new attempt must start from zero, got %d", got)
	}
}

// TestNoDropsWhenBufferFits is the control: the counter must stay silent on the ordinary path,
// or a bootstrap that buffered everything would be failed for no reason.
func TestNoDropsWhenBufferFits(t *testing.T) {
	m := &countingMetrics{}
	svc := newFrameTestService(t, m)
	svc.SetBootstrapInboundActive(true, 10)
	for i := 0; i < 10; i++ {
		if err := svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "k"}); err != nil {
			t.Fatalf("write %d should have fitted: %v", i, err)
		}
	}
	if got := svc.BootstrapDropped(); got != 0 {
		t.Fatalf("expected no discards, got %d", got)
	}
	if got := m.bootDropped.Load(); got != 0 {
		t.Fatalf("expected the metric to stay at zero, got %d", got)
	}
}

// TestBootstrapDropSurvivesNilMetrics covers a service built without metrics, which must count
// internally rather than panic — the server reads the count to decide whether to accept the
// snapshot.
func TestBootstrapDropSurvivesNilMetrics(t *testing.T) {
	svc := newFrameTestService(t, nil)
	svc.SetBootstrapInboundActive(true, 1)
	_ = svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "a"})
	if err := svc.enqueueBootstrapRepl(wireRepl{Op: "SET", Key: "b"}); err != nil {
		svc.noteBootstrapDrop("10.0.0.1:7379")
	}
	if got := svc.BootstrapDropped(); got != 1 {
		t.Fatalf("expected 1 discard recorded without metrics, got %d", got)
	}
}
