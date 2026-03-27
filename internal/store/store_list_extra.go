// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Additional list commands (LINDEX, LSET, LREM, LTRIM, LINSERT, LPUSHX, RPUSHX).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
)

func listElemAt(l *list.List, idx int) *list.Element {
	if idx < 0 || idx >= l.Len() {
		return nil
	}
	el := l.Front()
	for i := 0; i < idx; i++ {
		el = el.Next()
	}
	return el
}

// LIndex returns the element at index or nil if out of range.
func (s *Store) LIndex(key string, index int64) ([]byte, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return nil, nil
	}
	if e.dtype != TypeList {
		return nil, fmt.Errorf("lindex: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	n := l.Len()
	if n == 0 {
		return nil, nil
	}
	// Redis: positive index out of range returns nil (not the last element).
	if index >= 0 && index >= int64(n) {
		return nil, nil
	}
	idx := int(normalizeListIndex(index, n))
	if idx < 0 || idx >= n {
		return nil, nil
	}
	el := listElemAt(l, idx)
	if el == nil {
		return nil, nil
	}
	return append([]byte(nil), el.Value.([]byte)...), nil
}

// LSet sets the list element at index.
func (s *Store) LSet(key string, index int64, val []byte) error {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return fmt.Errorf("lset: %w", ErrKeyNotFound)
	}
	if e.dtype != TypeList {
		return fmt.Errorf("lset: %w", ErrWrongType)
	}
	l := e.value.(*list.List)
	n := l.Len()
	if index >= 0 && index >= int64(n) {
		return fmt.Errorf("lset: %w", ErrListIndex)
	}
	idx := int(normalizeListIndex(index, n))
	if idx < 0 || idx >= n {
		return fmt.Errorf("lset: %w", ErrListIndex)
	}
	el := listElemAt(l, idx)
	if el == nil {
		return fmt.Errorf("lset: %w", ErrListIndex)
	}
	old := e
	el.Value = append([]byte(nil), val...)
	ne := &entry{value: l, dtype: TypeList, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return nil
}

// ErrKeyNotFound is returned when a list key is missing for LSET.
var ErrKeyNotFound = errors.New("not found")

// ErrListIndex is returned when a list index is out of range.
var ErrListIndex = errors.New("list index out of range")

// LRem removes count occurrences of value. count >0 head, <0 tail, 0 all.
func (s *Store) LRem(key string, count int, value []byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return 0, nil
	}
	if e.dtype != TypeList {
		return 0, fmt.Errorf("lrem: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	if l.Len() == 0 {
		return 0, nil
	}
	removed := 0
	old := e
	switch {
	case count == 0:
		for el := l.Front(); el != nil; {
			next := el.Next()
			if bytes.Equal(el.Value.([]byte), value) {
				l.Remove(el)
				removed++
			}
			el = next
		}
	case count > 0:
		for el := l.Front(); el != nil && removed < count; {
			next := el.Next()
			if bytes.Equal(el.Value.([]byte), value) {
				l.Remove(el)
				removed++
			}
			el = next
		}
	default:
		need := -count
		for el := l.Back(); el != nil && removed < need; {
			prev := el.Prev()
			if bytes.Equal(el.Value.([]byte), value) {
				l.Remove(el)
				removed++
			}
			el = prev
		}
	}
	if l.Len() == 0 {
		s.purgeEntryLocked(sh, key, old)
		return removed, nil
	}
	ne := &entry{value: l, dtype: TypeList, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return removed, nil
}

// LTrim trims the list to the inclusive range [start, stop] (Redis semantics).
func (s *Store) LTrim(key string, start, stop int64) error {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return nil
	}
	if e.dtype != TypeList {
		return fmt.Errorf("ltrim: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	n := l.Len()
	if n == 0 {
		return nil
	}
	st := int(normalizeListIndex(start, n))
	en := int(normalizeListIndex(stop, n))
	if st < 0 {
		st = 0
	}
	if en >= n {
		en = n - 1
	}
	if st > en {
		old := e
		s.purgeEntryLocked(sh, key, old)
		return nil
	}
	nl := list.New()
	i := 0
	for el := l.Front(); el != nil; el = el.Next() {
		if i >= st && i <= en {
			nl.PushBack(append([]byte(nil), el.Value.([]byte)...))
		}
		i++
	}
	old := e
	ne := &entry{value: nl, dtype: TypeList, expiresAt: e.expiresAt}
	if nl.Len() == 0 {
		s.purgeEntryLocked(sh, key, old)
		return nil
	}
	s.replaceEntry(sh, key, old, ne)
	return nil
}

// LInsert inserts element before or after pivot (where=true means before).
func (s *Store) LInsert(key string, before bool, pivot, insert []byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return 0, nil
	}
	if e.dtype != TypeList {
		return 0, fmt.Errorf("linsert: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	oldL := e.value.(*list.List)
	// Find pivot position in old list.
	pivotIdx := -1
	idx := 0
	for el := oldL.Front(); el != nil; el = el.Next() {
		if bytes.Equal(el.Value.([]byte), pivot) {
			pivotIdx = idx
			break
		}
		idx++
	}
	if pivotIdx < 0 {
		return -1, nil
	}
	old := e
	// Clone the list so estimateMem on old entry remains accurate.
	nl := list.New()
	idx = 0
	ins := append([]byte(nil), insert...)
	for el := oldL.Front(); el != nil; el = el.Next() {
		if before && idx == pivotIdx {
			nl.PushBack(ins)
		}
		nl.PushBack(el.Value)
		if !before && idx == pivotIdx {
			nl.PushBack(ins)
		}
		idx++
	}
	ne := &entry{value: nl, dtype: TypeList, expiresAt: e.expiresAt}
	evicted, err := s.ensureMemoryWithRetry(sh, key, estimateMem(key, ne)-estimateMem(key, old))
	if err != nil {
		return 0, err
	}
	if evicted {
		old = sh.m[key]
	}
	s.replaceEntry(sh, key, old, ne)
	return nl.Len(), nil
}

// LPushX prepends values only if the key exists and holds a list.
func (s *Store) LPushX(key string, vals [][]byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.listPushX(sh, key, vals, true)
}

// RPushX appends values only if the key exists and holds a list.
func (s *Store) RPushX(key string, vals [][]byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.listPushX(sh, key, vals, false)
}

func (s *Store) listPushX(sh *shard, key string, vals [][]byte, left bool) (int, error) {
	if len(vals) == 0 {
		return s.listLenLocked(sh, key)
	}
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return 0, nil
	}
	if e.dtype != TypeList {
		return 0, fmt.Errorf("list: %w", ErrWrongType)
	}
	old := e
	oldL := e.value.(*list.List)
	// Clone the list so estimateMem on old entry remains accurate.
	nl := list.New()
	for el := oldL.Front(); el != nil; el = el.Next() {
		nl.PushBack(el.Value)
	}
	for _, v := range vals {
		b := append([]byte(nil), v...)
		if left {
			nl.PushFront(b)
		} else {
			nl.PushBack(b)
		}
	}
	ne := &entry{value: nl, dtype: TypeList, expiresAt: e.expiresAt}
	evicted, err := s.ensureMemoryWithRetry(sh, key, estimateMem(key, ne)-estimateMem(key, old))
	if err != nil {
		return 0, err
	}
	if evicted {
		old = sh.m[key]
	}
	s.replaceEntry(sh, key, old, ne)
	return nl.Len(), nil
}
