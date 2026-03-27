// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Active expiry sampling (Redis-style) for the Super-Cache store.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"context"
	"time"
)

const (
	expirySamplePerShard = 20
	expiryRepeatRatio    = 0.25
)

// runActiveExpiry wakes every 100ms and samples keys for expiration until ctx is done.
func runActiveExpiry(ctx context.Context, s *Store) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	watchPruneCounter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleAllShards(s)
			// Prune dead watch entries every ~30 seconds (300 ticks * 100ms).
			watchPruneCounter++
			if watchPruneCounter >= 300 {
				watchPruneCounter = 0
				s.PruneWatch()
			}
		}
	}
}

func sampleAllShards(s *Store) {
	const maxResamples = 16 // cap iterations per shard to prevent starvation
	for si := 0; si < NumShards; si++ {
		for iter := 0; iter < maxResamples; iter++ {
			expired, checked := sampleOnceShard(s, si)
			if checked == 0 {
				break
			}
			if float64(expired)/float64(checked) <= expiryRepeatRatio {
				break
			}
		}
	}
}

func sampleOnceShard(s *Store, si int) (expired int, checked int) {
	sh := &s.shards[si]
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if len(sh.m) == 0 {
		return 0, 0
	}
	keys := randomSampleKeys(sh.m, expirySamplePerShard)
	for _, k := range keys {
		e := sh.m[k]
		if e == nil {
			continue
		}
		checked++
		if s.isExpired(e) {
			s.purgeEntryLocked(sh, k, e)
			expired++
		}
	}
	return expired, checked
}

func randomSampleKeys(m map[string]*entry, n int) []string {
	if len(m) == 0 {
		return nil
	}
	if len(m) <= n {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		return keys
	}
	// Use map iteration (random order in Go) and collect only n keys to avoid
	// allocating a slice of all keys and shuffling it on every sample cycle.
	keys := make([]string, 0, n)
	for k := range m {
		keys = append(keys, k)
		if len(keys) >= n {
			break
		}
	}
	return keys
}
