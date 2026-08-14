// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Confirming that replication events are truly lost, and asking for a resync when they are.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// A skipped sequence is not proof of loss on its own. Replacing one link to a peer with another
// can deliver a straggler from the old link after the new one has moved ahead, which looks
// exactly like a gap until the straggler arrives a moment later. Acting immediately would
// therefore flush and refetch the whole dataset over something that resolves itself.
//
// Each missing sequence is instead held for a grace period. Ones that arrive late are struck
// off; whatever is still missing when the period expires never arrived, and the node really is
// short of data the sender has.

const (
	// gapGrace is how long a missing sequence is given to turn up. It only has to outlast the
	// reordering a link switch can produce, which is bounded by what one socket had queued.
	gapGrace = 5 * time.Second
	// gapTrackLimit bounds how many individual sequences are held per gap. A jump larger than
	// this cannot be reordering, so it is treated as loss without waiting.
	gapTrackLimit = 256
	// gapSweepInterval is how often expired sequences are checked for.
	gapSweepInterval = time.Second
)

// ResyncRequester is called when a peer's events are confirmed lost and this node needs a fresh
// copy of the dataset. Implementations are expected to rate limit.
type ResyncRequester interface {
	RequestResync(reason string)
}

// gapTracker holds sequences that have not arrived yet, per origin.
type gapTracker struct {
	mu      sync.Mutex
	missing map[string]map[uint64]int64 // origin -> seq -> deadline unix ms
}

func newGapTracker() *gapTracker {
	return &gapTracker{missing: make(map[string]map[uint64]int64)}
}

// note records the sequences skipped between prev and got. It reports true when the jump is too
// large to be anything but loss.
func (g *gapTracker) note(origin string, prev, got uint64) bool {
	if got <= prev+1 {
		return false
	}
	if got-prev-1 > gapTrackLimit {
		return true
	}
	deadline := time.Now().Add(gapGrace).UnixMilli()
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.missing[origin]
	if m == nil {
		m = make(map[uint64]int64)
		g.missing[origin] = m
	}
	for seq := prev + 1; seq < got; seq++ {
		m[seq] = deadline
	}
	return false
}

// arrived strikes a sequence off, which is what a straggler from a replaced link does.
func (g *gapTracker) arrived(origin string, seq uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.missing[origin]; m != nil {
		delete(m, seq)
		if len(m) == 0 {
			delete(g.missing, origin)
		}
	}
}

// expired returns the origins with sequences that never arrived, and forgets them so the same
// loss is only reported once.
func (g *gapTracker) expired(now time.Time) map[string]int {
	cutoff := now.UnixMilli()
	out := make(map[string]int)
	g.mu.Lock()
	defer g.mu.Unlock()
	for origin, m := range g.missing {
		for seq, deadline := range m {
			if deadline <= cutoff {
				out[origin]++
				delete(m, seq)
			}
		}
		if len(m) == 0 {
			delete(g.missing, origin)
		}
	}
	return out
}

// reset clears all tracking, used after a resync has replaced the dataset.
func (g *gapTracker) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.missing = make(map[string]map[uint64]int64)
}

// SetResyncRequester installs the callback used when loss is confirmed. Nil disables resyncing,
// leaving the counters as the only signal.
func (s *Service) SetResyncRequester(r ResyncRequester) {
	s.resyncMu.Lock()
	defer s.resyncMu.Unlock()
	s.resync = r
}

func (s *Service) resyncRequester() ResyncRequester {
	s.resyncMu.Lock()
	defer s.resyncMu.Unlock()
	return s.resync
}

// ResetReplicationTracking forgets every peer's sequence position and any pending gaps.
//
// Called after a resync, because the snapshot replaces the dataset wholesale: the sequence this
// node had reached before it no longer says anything about what the new copy contains, and
// carrying it over would make the next arrival look like a fresh gap.
func (s *Service) ResetReplicationTracking() {
	s.originSeq.Range(func(k, _ any) bool {
		s.originSeq.Delete(k)
		return true
	})
	if s.gaps != nil {
		s.gaps.reset()
	}
}

// watchGaps reports loss once the grace period has passed without the missing events arriving.
func (s *Service) watchGaps(ctx context.Context) {
	t := time.NewTicker(gapSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			lost := s.gaps.expired(now)
			if len(lost) == 0 {
				continue
			}
			total := 0
			for origin, n := range lost {
				total += n
				slog.Error("replication events from a peer never arrived; this node is missing data it holds",
					"origin", origin, "events", n, "waited", gapGrace)
			}
			if r := s.resyncRequester(); r != nil {
				r.RequestResync("confirmed replication loss")
			}
			_ = total
		}
	}
}

// confirmLoss reports a jump too large to be reordering, without waiting out the grace period.
func (s *Service) confirmLoss(origin string, missed uint64) {
	slog.Error("replication sequence jumped too far to be reordering; treating it as loss",
		"origin", origin, "missed", missed)
	if r := s.resyncRequester(); r != nil {
		r.RequestResync("large replication gap")
	}
}
