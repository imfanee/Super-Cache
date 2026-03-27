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
	var h map[string][]byte
	var old *entry
	if had {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hincrby: %w", ErrWrongType)
		}
		h = e.value.(map[string][]byte)
		old = e
	} else {
		h = make(map[string][]byte)
	}
	cur := int64(0)
	if v, ok := h[field]; ok {
		s := strings.TrimSpace(string(v))
		if s != "" {
			n, err := strconv.ParseInt(s, 10, 64)
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
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
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
	var h map[string][]byte
	var old *entry
	if had {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hincrbyfloat: %w", ErrWrongType)
		}
		h = e.value.(map[string][]byte)
		old = e
	} else {
		h = make(map[string][]byte)
	}
	cur := 0.0
	if v, ok := h[field]; ok {
		s := strings.TrimSpace(string(v))
		if s != "" {
			f, err := strconv.ParseFloat(s, 64)
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
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, old, ne)
	return next, nil
}
