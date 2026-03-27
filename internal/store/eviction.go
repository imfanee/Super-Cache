// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Memory eviction policies and per-shard LRU bookkeeping.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"math/rand/v2"
	"time"
)

// touchAllKeysLRU records access for allkeys-lru.
func touchAllKeysLRU(s *Store, sh *shard, key string, e *entry) {
	if s.policy() != "allkeys-lru" {
		return
	}
	if el, ok := sh.allElems[key]; ok {
		sh.allList.MoveToFront(el)
		return
	}
	el := sh.allList.PushFront(key)
	sh.allElems[key] = el
	e.allElem = el
}

// touchVolatileLRU records access for volatile-lru (keys with TTL only).
func touchVolatileLRU(s *Store, sh *shard, key string, e *entry) {
	if s.policy() != "volatile-lru" {
		return
	}
	if !e.hasTTL() {
		if e.volElem != nil {
			sh.volList.Remove(e.volElem)
			delete(sh.volElems, key)
			e.volElem = nil
		}
		return
	}
	if el, ok := sh.volElems[key]; ok {
		sh.volList.MoveToFront(el)
		return
	}
	el := sh.volList.PushFront(key)
	sh.volElems[key] = el
	e.volElem = el
}

// lruAfterSet updates LRU structures after a key mutation.
func lruAfterSet(s *Store, sh *shard, key string, e *entry) {
	pol := s.policy()
	if pol == "allkeys-lru" {
		if el, ok := sh.allElems[key]; ok {
			sh.allList.MoveToFront(el)
		} else {
			el := sh.allList.PushFront(key)
			sh.allElems[key] = el
			e.allElem = el
		}
	}
	if pol == "volatile-lru" {
		if e.hasTTL() {
			if el, ok := sh.volElems[key]; ok {
				sh.volList.MoveToFront(el)
			} else {
				el := sh.volList.PushFront(key)
				sh.volElems[key] = el
				e.volElem = el
			}
		} else if e.volElem != nil {
			sh.volList.Remove(e.volElem)
			delete(sh.volElems, key)
			e.volElem = nil
		}
	}
}

// evictFromStore removes one key according to the active eviction policy.
// Callers must not hold any shard mutex while invoking this function.
func evictFromStore(s *Store, avoidKey string) bool {
	switch s.policy() {
	case "noeviction":
		return false
	case "allkeys-lru":
		return evictAllKeysLRU(s, avoidKey)
	case "volatile-lru":
		return evictVolatileLRU(s, avoidKey)
	case "allkeys-random":
		return evictRandom(s, nil, avoidKey)
	case "volatile-random":
		return evictRandom(s, func(e *entry) bool { return e.hasTTL() }, avoidKey)
	case "volatile-ttl":
		return evictVolatileTTL(s, avoidKey)
	default:
		return false
	}
}

func evictAllKeysLRU(s *Store, avoidKey string) bool {
	for attempt := 0; attempt < NumShards*2; attempt++ {
		si := int(s.evictCursor.Add(1)-1) % NumShards
		sh := &s.shards[si]
		sh.mu.Lock()
		if sh.allList.Len() == 0 {
			sh.mu.Unlock()
			continue
		}
		back := sh.allList.Back()
		if back == nil {
			sh.mu.Unlock()
			continue
		}
		victim := back.Value.(string)
		if avoidKey != "" && victim == avoidKey {
			sh.mu.Unlock()
			continue
		}
		e := sh.m[victim]
		if e == nil {
			sh.mu.Unlock()
			continue
		}
		s.purgeEntryLocked(sh, victim, e)
		sh.mu.Unlock()
		return true
	}
	return false
}

func evictVolatileLRU(s *Store, avoidKey string) bool {
	for attempt := 0; attempt < NumShards*2; attempt++ {
		si := int(s.evictCursor.Add(1)-1) % NumShards
		sh := &s.shards[si]
		sh.mu.Lock()
		if sh.volList.Len() == 0 {
			sh.mu.Unlock()
			continue
		}
		back := sh.volList.Back()
		if back == nil {
			sh.mu.Unlock()
			continue
		}
		victim := back.Value.(string)
		if avoidKey != "" && victim == avoidKey {
			sh.mu.Unlock()
			continue
		}
		e := sh.m[victim]
		if e == nil || !e.hasTTL() {
			sh.mu.Unlock()
			continue
		}
		s.purgeEntryLocked(sh, victim, e)
		sh.mu.Unlock()
		return true
	}
	return false
}

func evictRandom(s *Store, pred func(*entry) bool, avoidKey string) bool {
	for tries := 0; tries < NumShards*8; tries++ {
		si := rand.IntN(NumShards)
		sh := &s.shards[si]
		sh.mu.Lock()
		if len(sh.m) == 0 {
			sh.mu.Unlock()
			continue
		}
		var cand []string
		for k, e := range sh.m {
			if pred != nil && !pred(e) {
				continue
			}
			cand = append(cand, k)
		}
		if len(cand) == 0 {
			sh.mu.Unlock()
			continue
		}
		victim := cand[rand.IntN(len(cand))]
		if avoidKey != "" && victim == avoidKey {
			sh.mu.Unlock()
			continue
		}
		e := sh.m[victim]
		if e == nil {
			sh.mu.Unlock()
			continue
		}
		s.purgeEntryLocked(sh, victim, e)
		sh.mu.Unlock()
		return true
	}
	return false
}

func evictVolatileTTL(s *Store, avoidKey string) bool {
	var bestKey string
	var bestSi int
	var bestAt time.Time
	found := false
	for si := 0; si < NumShards; si++ {
		sh := &s.shards[si]
		sh.mu.Lock()
		for k, e := range sh.m {
			if !e.hasTTL() {
				continue
			}
			if avoidKey != "" && k == avoidKey {
				continue
			}
			if !found || e.expiresAt.Before(bestAt) {
				found = true
				bestAt = e.expiresAt
				bestKey = k
				bestSi = si
			}
		}
		sh.mu.Unlock()
	}
	if !found {
		return false
	}
	sh := &s.shards[bestSi]
	sh.mu.Lock()
	e := sh.m[bestKey]
	if e != nil && e.hasTTL() {
		s.purgeEntryLocked(sh, bestKey, e)
		sh.mu.Unlock()
		return true
	}
	sh.mu.Unlock()
	return false
}

