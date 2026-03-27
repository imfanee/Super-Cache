// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Active expiry sampling (Redis-style) for the Super-Cache store.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"context"
	"math/rand/v2"
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sampleAllShards(s)
		}
	}
}

func sampleAllShards(s *Store) {
	for si := 0; si < NumShards; si++ {
		for {
			expired, checked := sampleOnceShard(s, si)
			if checked == 0 {
				break
			}
			if float64(expired)/float64(checked) > expiryRepeatRatio {
				continue
			}
			break
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
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) <= n {
		return keys
	}
	rand.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	return keys[:n]
}
