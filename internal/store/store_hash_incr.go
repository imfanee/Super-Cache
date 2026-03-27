// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Hash increment operations (HINCRBY, HINCRBYFLOAT).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"fmt"
	"strconv"
	"strings"
)

// HIncrBy increments a hash field as a signed 64-bit integer.
func (s *Store) HIncrBy(key, field string, delta int64) (int64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, had := sh.m[key]
	if had && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		had = false
	}
	var oldH map[string][]byte
	var old *entry
	if had {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hincrby: %w", ErrWrongType)
		}
		oldH = e.value.(map[string][]byte)
		old = e
	}
	// Clone the hash so estimateMem on old entry remains accurate.
	h := make(map[string][]byte, len(oldH)+1)
	for k, v := range oldH {
		h[k] = v
	}
	cur := int64(0)
	if v, ok := h[field]; ok {
		sv := strings.TrimSpace(string(v))
		if sv != "" {
			n, err := strconv.ParseInt(sv, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("not an integer: %w", err)
			}
			cur = n
		}
	}
	next, okAdd := addInt64(cur, delta)
	if !okAdd {
		return 0, fmt.Errorf("increment or decrement would overflow: %w", strconv.ErrRange)
	}
	out := strconv.FormatInt(next, 10)
	h[field] = []byte(out)
	ne := &entry{value: h, dtype: TypeHash}
	if had {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if had {
		oldB = estimateMem(key, old)
	}
	evicted, err := s.ensureMemoryWithRetry(sh, key, newB-oldB)
	if err != nil {
		return 0, err
	}
	if evicted {
		old = sh.m[key]
	}
	s.replaceEntry(sh, key, old, ne)
	return next, nil
}

// HIncrByFloat increments a hash field as a float64 string.
func (s *Store) HIncrByFloat(key, field string, delta float64) (float64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, had := sh.m[key]
	if had && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		had = false
	}
	var oldH map[string][]byte
	var old *entry
	if had {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hincrbyfloat: %w", ErrWrongType)
		}
		oldH = e.value.(map[string][]byte)
		old = e
	}
	// Clone the hash so estimateMem on old entry remains accurate.
	h := make(map[string][]byte, len(oldH)+1)
	for k, v := range oldH {
		h[k] = v
	}
	cur := 0.0
	if v, ok := h[field]; ok {
		sv := strings.TrimSpace(string(v))
		if sv != "" {
			f, err := strconv.ParseFloat(sv, 64)
			if err != nil {
				return 0, fmt.Errorf("not a float: %w", err)
			}
			cur = f
		}
	}
	next := cur + delta
	out := strconv.FormatFloat(next, 'f', -1, 64)
	h[field] = []byte(out)
	ne := &entry{value: h, dtype: TypeHash}
	if had {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if had {
		oldB = estimateMem(key, old)
	}
	evicted, err := s.ensureMemoryWithRetry(sh, key, newB-oldB)
	if err != nil {
		return 0, err
	}
	if evicted {
		old = sh.m[key]
	}
	s.replaceEntry(sh, key, old, ne)
	return next, nil
}
