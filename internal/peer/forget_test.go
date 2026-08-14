// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for removing peers that have gone away without removing ones that have not.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"context"
	"testing"
	"time"
)

// ageAddr backdates an address's bookkeeping so it looks unreachable for the given duration.
func ageAddr(s *Service, addr string, by time.Duration) {
	s.peerMetaMu.Lock()
	defer s.peerMetaMu.Unlock()
	if m, ok := s.peerMeta[addr]; ok {
		m.added = time.Now().Add(-by).UnixMilli()
		if m.lastOK != 0 {
			m.lastOK = time.Now().Add(-by).UnixMilli()
		}
	}
}

func addPeers(t *testing.T, s *Service, src PeerSource, addrs ...string) {
	t.Helper()
	for _, a := range addrs {
		if err := s.AddPeerFrom(a, src); err != nil {
			t.Fatalf("AddPeerFrom(%q): %v", a, err)
		}
	}
}

// newForgetService builds a service whose dial machinery is live enough for AddPeer to succeed.
func newForgetService(t *testing.T) *Service {
	t.Helper()
	svc := newFrameTestService(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	svc.dialParent = ctx
	svc.activeDial = make(map[string]context.CancelFunc)
	return svc
}

// TestConfiguredPeersAreNeverForgotten is the safety property. An address in the file is the
// operator's stated intent, and however long it stays unreachable it must survive.
func TestConfiguredPeersAreNeverForgotten(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.5:7379")
	svc.NoteConfigPeers([]string{"203.0.113.1:7379"})
	addPeers(t, svc, SourceConfig, "203.0.113.1:7379")
	ageAddr(svc, "203.0.113.1:7379", 48*time.Hour)
	ageAddr(svc, "203.0.113.5:7379", 48*time.Hour)

	svc.ForgetUnreachablePeers(time.Hour)
	if src, ok := svc.PeerSourceOf("203.0.113.1:7379"); !ok || src != SourceConfig {
		t.Fatal("a configured peer must never be forgotten")
	}
}

// TestLearnedPeerForgottenWhenStale is the case that made this necessary: an instance the
// autoscaler destroyed, whose address would otherwise be redialled forever.
func TestLearnedPeerForgottenWhenStale(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.7:7379", "203.0.113.8:7379")
	ageAddr(svc, "203.0.113.7:7379", 48*time.Hour)

	got := svc.ForgetUnreachablePeers(time.Hour)
	if len(got) != 1 || got[0] != "203.0.113.7:7379" {
		t.Fatalf("expected the stale peer to be forgotten, got %v", got)
	}
	if _, ok := svc.PeerSourceOf("203.0.113.7:7379"); ok {
		t.Fatal("the record should be gone too")
	}
}

// TestRecentlyAddedPeerIsKept covers a node that has only just been learned and has not had time
// to connect yet.
func TestRecentlyAddedPeerIsKept(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.9:7379", "203.0.113.10:7379")
	if got := svc.ForgetUnreachablePeers(time.Hour); len(got) != 0 {
		t.Fatalf("a peer added moments ago must be kept, got %v", got)
	}
}

// TestLastPeerIsNeverForgotten guards the worst outcome available here. A node that forgets its
// final peer can only rejoin by being contacted, so if both sides of a long partition emptied
// their lists neither would ever reconnect.
func TestLastPeerIsNeverForgotten(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.11:7379")
	ageAddr(svc, "203.0.113.11:7379", 72*time.Hour)

	if got := svc.ForgetUnreachablePeers(time.Hour); len(got) != 0 {
		t.Fatalf("the only remaining peer must be kept, got %v", got)
	}
}

func TestForgetDisabledByZeroDuration(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.12:7379", "203.0.113.13:7379")
	ageAddr(svc, "203.0.113.12:7379", 72*time.Hour)
	if got := svc.ForgetUnreachablePeers(0); len(got) != 0 {
		t.Fatalf("a zero window must disable removal, got %v", got)
	}
}

// TestDiscoveryRemovesVanishedPeer covers the authoritative signal: a listing that no longer
// names a machine means it does not exist, which is stronger evidence than any timeout.
func TestDiscoveryRemovesVanishedPeer(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceDiscovery, "203.0.113.20:7379", "203.0.113.21:7379")

	got := svc.ForgetPeersMissingFrom([]string{"203.0.113.20:7379"})
	if len(got) != 1 || got[0] != "203.0.113.21:7379" {
		t.Fatalf("expected the unlisted peer to be removed, got %v", got)
	}
}

// TestDiscoveryKeepsPeersItNeverContributed is why the source is recorded. A peer learned from an
// inbound connection may sit outside the label selector, so a listing saying nothing about it is
// not evidence of anything.
func TestDiscoveryKeepsPeersItNeverContributed(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.30:7379")
	addPeers(t, svc, SourceDiscovery, "203.0.113.31:7379")

	got := svc.ForgetPeersMissingFrom([]string{"203.0.113.99:7379"})
	for _, a := range got {
		if a == "203.0.113.30:7379" {
			t.Fatal("a peer discovery never contributed must not be removed by a listing")
		}
	}
}

// TestConfiguredPeerSurvivesDiscovery confirms a listing cannot override the file either.
func TestConfiguredPeerSurvivesDiscovery(t *testing.T) {
	svc := newForgetService(t)
	svc.NoteConfigPeers([]string{"203.0.113.40:7379"})
	addPeers(t, svc, SourceConfig, "203.0.113.40:7379")
	addPeers(t, svc, SourceDiscovery, "203.0.113.41:7379")

	svc.ForgetPeersMissingFrom(nil)
	if src, ok := svc.PeerSourceOf("203.0.113.40:7379"); !ok || src != SourceConfig {
		t.Fatal("a configured peer must survive a listing that omits it")
	}
}

// TestDiscoveryDoesNotDemoteConfiguredPeer covers an address that is both in the file and in the
// listing: it must keep the stronger claim, or the next listing could remove it.
func TestDiscoveryDoesNotDemoteConfiguredPeer(t *testing.T) {
	svc := newForgetService(t)
	svc.NoteConfigPeers([]string{"203.0.113.50:7379"})
	addPeers(t, svc, SourceConfig, "203.0.113.50:7379")
	// Discovery listing an address already known is rejected as a duplicate, which is exactly
	// what keeps it from being reclassified as forgettable.
	if err := svc.AddPeerFrom("203.0.113.50:7379", SourceDiscovery); err == nil {
		t.Fatal("expected a duplicate address to be rejected")
	}
	if src, _ := svc.PeerSourceOf("203.0.113.50:7379"); src != SourceConfig {
		t.Fatalf("expected the address to stay configured, got %q", src)
	}
	// And a listing that omits it still must not remove it.
	svc.ForgetPeersMissingFrom([]string{"203.0.113.99:7379"})
	if src, ok := svc.PeerSourceOf("203.0.113.50:7379"); !ok || src != SourceConfig {
		t.Fatal("a configured address must survive a listing that omits it")
	}
}

// TestUnknownAddressIsNeverForgotten covers an address with no record at all, which must be
// treated as intent rather than guessed about.
func TestUnknownAddressIsNeverForgotten(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceLearned, "203.0.113.70:7379", "203.0.113.71:7379")
	svc.peerMetaMu.Lock()
	delete(svc.peerMeta, "203.0.113.70:7379")
	svc.peerMetaMu.Unlock()
	ageAddr(svc, "203.0.113.71:7379", 72*time.Hour)

	got := svc.ForgetUnreachablePeers(time.Hour)
	for _, a := range got {
		if a == "203.0.113.70:7379" {
			t.Fatal("an address with no provenance must not be removed")
		}
	}
}

// TestManualPeerIsNeverForgotten covers an address an operator added deliberately at runtime.
func TestManualPeerIsNeverForgotten(t *testing.T) {
	svc := newForgetService(t)
	addPeers(t, svc, SourceManual, "203.0.113.60:7379")
	addPeers(t, svc, SourceLearned, "203.0.113.61:7379")
	ageAddr(svc, "203.0.113.60:7379", 72*time.Hour)
	ageAddr(svc, "203.0.113.61:7379", 72*time.Hour)

	got := svc.ForgetUnreachablePeers(time.Hour)
	for _, a := range got {
		if a == "203.0.113.60:7379" {
			t.Fatal("a manually added peer must never be forgotten")
		}
	}
}
