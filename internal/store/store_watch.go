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
