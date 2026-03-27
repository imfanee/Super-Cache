// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// WATCH/EXEC version tracking for optimistic transactions.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

// WatchVersion returns the current mutation generation for key (0 if never written).
func (s *Store) WatchVersion(key string) uint64 {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	return s.watch[key]
}

func (s *Store) bumpWatchKey(key string) {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	if s.watch == nil {
		s.watch = make(map[string]uint64)
	}
	s.watch[key]++
}

// PruneWatch removes watch entries for keys that no longer exist in the store.
// Called periodically (e.g. from active expiry) to prevent unbounded growth.
func (s *Store) PruneWatch() {
	s.watchMu.Lock()
	// Snapshot the keys to check.
	keys := make([]string, 0, len(s.watch))
	for k := range s.watch {
		keys = append(keys, k)
	}
	s.watchMu.Unlock()

	// Check which keys still exist (acquires shard locks one at a time).
	dead := make([]string, 0)
	for _, k := range keys {
		sh := s.shardFor(k)
		sh.mu.RLock()
		_, exists := sh.m[k]
		sh.mu.RUnlock()
		if !exists {
			dead = append(dead, k)
		}
	}

	if len(dead) > 0 {
		s.watchMu.Lock()
		for _, k := range dead {
			delete(s.watch, k)
		}
		s.watchMu.Unlock()
	}
}
