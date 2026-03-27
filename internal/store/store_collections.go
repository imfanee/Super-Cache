// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Hash, list, set operations, glob matching, and snapshot transfer helpers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"container/list"
	"errors"
	"fmt"
	"path"
	"time"
)

// HSet sets hash fields and returns the number of newly created fields.
func (s *Store) HSet(key string, pairs map[string][]byte) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
	}
	var h map[string][]byte
	var old *entry
	if ok {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hset: %w", ErrWrongType)
		}
		h = e.value.(map[string][]byte)
		old = e
	} else {
		h = make(map[string][]byte)
	}
	newFields := 0
	for fk, fv := range pairs {
		if _, exists := h[fk]; !exists {
			newFields++
		}
		cp := append([]byte(nil), fv...)
		h[fk] = cp
	}
	ne := &entry{value: h, dtype: TypeHash}
	if ok {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if ok {
		oldB = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, old, ne)
	return newFields, nil
}

// HGet returns a hash field value.
func (s *Store) HGet(key, field string) ([]byte, error) {
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
	if e.dtype != TypeHash {
		return nil, fmt.Errorf("hget: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	h := e.value.(map[string][]byte)
	v, ok := h[field]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), v...), nil
}

// HGetAll returns a copy of the hash.
func (s *Store) HGetAll(key string) (map[string][]byte, error) {
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
	if e.dtype != TypeHash {
		return nil, fmt.Errorf("hgetall: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	h := e.value.(map[string][]byte)
	out := make(map[string][]byte, len(h))
	for k, v := range h {
		out[k] = append([]byte(nil), v...)
	}
	return out, nil
}

// HDel deletes hash fields and returns how many were removed.
func (s *Store) HDel(key string, fields []string) (int, error) {
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
	if e.dtype != TypeHash {
		return 0, fmt.Errorf("hdel: %w", ErrWrongType)
	}
	h := e.value.(map[string][]byte)
	n := 0
	for _, f := range fields {
		if _, ok := h[f]; ok {
			delete(h, f)
			n++
		}
	}
	if len(h) == 0 {
		s.purgeEntryLocked(sh, key, e)
		return n, nil
	}
	old := e
	ne := &entry{value: h, dtype: TypeHash, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return n, nil
}

// HExists reports whether a field exists.
func (s *Store) HExists(key, field string) (bool, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return false, nil
	}
	if e.dtype != TypeHash {
		return false, fmt.Errorf("hexists: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	_, ok = e.value.(map[string][]byte)[field]
	return ok, nil
}

// HLen returns the number of fields in a hash.
func (s *Store) HLen(key string) (int, error) {
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
	if e.dtype != TypeHash {
		return 0, fmt.Errorf("hlen: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	return len(e.value.(map[string][]byte)), nil
}

// HSetNX sets field to value only if the field does not exist. Returns 1 if set, 0 if not.
func (s *Store) HSetNX(key, field string, val []byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
	}
	var h map[string][]byte
	var old *entry
	if ok {
		if e.dtype != TypeHash {
			return 0, fmt.Errorf("hsetnx: %w", ErrWrongType)
		}
		h = e.value.(map[string][]byte)
		old = e
		if _, exists := h[field]; exists {
			return 0, nil
		}
	} else {
		h = make(map[string][]byte)
	}
	h[field] = append([]byte(nil), val...)
	ne := &entry{value: h, dtype: TypeHash}
	if ok {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if ok {
		oldB = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, old, ne)
	return 1, nil
}

// LPush prepends values to a list (left is head).
func (s *Store) LPush(key string, vals [][]byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.listPush(sh, key, vals, true)
}

// RPush appends values to a list.
func (s *Store) RPush(key string, vals [][]byte) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.listPush(sh, key, vals, false)
}

func (s *Store) listPush(sh *shard, key string, vals [][]byte, left bool) (int, error) {
	if len(vals) == 0 {
		return s.listLenLocked(sh, key)
	}
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
	}
	var l *list.List
	var old *entry
	if ok {
		if e.dtype != TypeList {
			return 0, fmt.Errorf("list: %w", ErrWrongType)
		}
		l = e.value.(*list.List)
		old = e
	} else {
		l = list.New()
	}
	for _, v := range vals {
		b := append([]byte(nil), v...)
		if left {
			l.PushFront(b)
		} else {
			l.PushBack(b)
		}
	}
	ne := &entry{value: l, dtype: TypeList}
	if ok {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if ok {
		oldB = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, old, ne)
	return l.Len(), nil
}

func (s *Store) listLenLocked(sh *shard, key string) (int, error) {
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
	return e.value.(*list.List).Len(), nil
}

// LPop pops up to count elements from the head. count <= 0 means 1.
func (s *Store) LPop(key string, count int) ([][]byte, error) {
	if count <= 0 {
		count = 1
	}
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
		return nil, fmt.Errorf("lpop: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	if l.Len() == 0 {
		return [][]byte{}, nil
	}
	out := make([][]byte, 0, count)
	old := e
	for i := 0; i < count && l.Len() > 0; i++ {
		f := l.Front()
		v := f.Value.([]byte)
		out = append(out, append([]byte(nil), v...))
		l.Remove(f)
	}
	if l.Len() == 0 {
		s.purgeEntryLocked(sh, key, old)
		return out, nil
	}
	ne := &entry{value: l, dtype: TypeList, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return out, nil
}

// RPop pops up to count elements from the tail.
func (s *Store) RPop(key string, count int) ([][]byte, error) {
	if count <= 0 {
		count = 1
	}
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
		return nil, fmt.Errorf("rpop: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	if l.Len() == 0 {
		return [][]byte{}, nil
	}
	out := make([][]byte, 0, count)
	old := e
	for i := 0; i < count && l.Len() > 0; i++ {
		b := l.Back()
		v := b.Value.([]byte)
		out = append(out, append([]byte(nil), v...))
		l.Remove(b)
	}
	if l.Len() == 0 {
		s.purgeEntryLocked(sh, key, old)
		return out, nil
	}
	ne := &entry{value: l, dtype: TypeList, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return out, nil
}

// LLen returns list length.
func (s *Store) LLen(key string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.listLenLocked(sh, key)
}

// LRange returns inclusive range; negative indices count from the tail.
func (s *Store) LRange(key string, start, stop int64) ([][]byte, error) {
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
		return nil, fmt.Errorf("lrange: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	l := e.value.(*list.List)
	n := l.Len()
	if n == 0 {
		return [][]byte{}, nil
	}
	// Redis: a non-negative start beyond the list length yields an empty range (not the last element).
	if start >= 0 && start >= int64(n) {
		return [][]byte{}, nil
	}
	st := normalizeListIndex(start, n)
	en := normalizeListIndex(stop, n)
	if st < 0 {
		st = 0
	}
	if en >= n {
		en = n - 1
	}
	if st > en {
		return [][]byte{}, nil
	}
	out := make([][]byte, 0, en-st+1)
	i := 0
	for el := l.Front(); el != nil; el = el.Next() {
		if i > en {
			break
		}
		if i >= st {
			b := el.Value.([]byte)
			out = append(out, append([]byte(nil), b...))
		}
		i++
	}
	return out, nil
}

func normalizeListIndex(idx int64, n int) int {
	if idx < 0 {
		idx = int64(n) + idx
	}
	if idx < 0 {
		return 0
	}
	if int(idx) >= n {
		return n - 1
	}
	return int(idx)
}

// SAdd adds members to a set and returns how many were newly inserted.
func (s *Store) SAdd(key string, members [][]byte) (int, error) {
	if len(members) == 0 {
		return 0, nil
	}
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
	}
	var st map[string]struct{}
	var old *entry
	if ok {
		if e.dtype != TypeSet {
			return 0, fmt.Errorf("sadd: %w", ErrWrongType)
		}
		st = e.value.(map[string]struct{})
		old = e
	} else {
		st = make(map[string]struct{})
	}
	added := 0
	for _, m := range members {
		k := string(m)
		if _, exists := st[k]; !exists {
			st[k] = struct{}{}
			added++
		}
	}
	ne := &entry{value: st, dtype: TypeSet}
	if ok {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if ok {
		oldB = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, old, ne)
	return added, nil
}

// SRem removes members and returns how many were removed.
func (s *Store) SRem(key string, members [][]byte) (int, error) {
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
	if e.dtype != TypeSet {
		return 0, fmt.Errorf("srem: %w", ErrWrongType)
	}
	st := e.value.(map[string]struct{})
	n := 0
	for _, m := range members {
		k := string(m)
		if _, ok := st[k]; ok {
			delete(st, k)
			n++
		}
	}
	if len(st) == 0 {
		s.purgeEntryLocked(sh, key, e)
		return n, nil
	}
	old := e
	ne := &entry{value: st, dtype: TypeSet, expiresAt: e.expiresAt}
	s.replaceEntry(sh, key, old, ne)
	return n, nil
}

// SMembers returns all members.
func (s *Store) SMembers(key string) ([][]byte, error) {
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
	if e.dtype != TypeSet {
		return nil, fmt.Errorf("smembers: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	st := e.value.(map[string]struct{})
	out := make([][]byte, 0, len(st))
	for m := range st {
		out = append(out, []byte(m))
	}
	return out, nil
}

// SIsMember reports set membership.
func (s *Store) SIsMember(key string, member []byte) (bool, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return false, nil
	}
	if e.dtype != TypeSet {
		return false, fmt.Errorf("sismember: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	_, ok = e.value.(map[string]struct{})[string(member)]
	return ok, nil
}

// SCard returns set cardinality.
func (s *Store) SCard(key string) (int, error) {
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
	if e.dtype != TypeSet {
		return 0, fmt.Errorf("scard: %w", ErrWrongType)
	}
	s.touchLRU(sh, key, e)
	return len(e.value.(map[string]struct{})), nil
}

// ReplaceSet overwrites key with a set containing exactly members (empty set is stored if members is empty).
func (s *Store) ReplaceSet(key string, members [][]byte) error {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	st := make(map[string]struct{}, len(members))
	for _, m := range members {
		st[string(m)] = struct{}{}
	}
	ne := &entry{value: st, dtype: TypeSet}
	e, had := sh.m[key]
	if had && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		had = false
		e = nil
	}
	if had {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if had {
		oldB = estimateMem(key, e)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return err
	}
	s.replaceEntry(sh, key, e, ne)
	return nil
}

// matchGlob reports whether key matches pattern using path.Match semantics.
func matchGlob(pattern, key string) bool {
	ok, err := path.Match(pattern, key)
	return err == nil && ok
}

// Snapshot streams a copy-on-read snapshot of all keys.
func (s *Store) Snapshot() <-chan SnapshotEntry {
	out := make(chan SnapshotEntry, 1024)
	go func() {
		defer close(out)
		for si := 0; si < NumShards; si++ {
			sh := &s.shards[si]
			sh.mu.Lock()
			var batch []SnapshotEntry
			for k, e := range sh.m {
				if s.isExpired(e) {
					continue
				}
				batch = append(batch, buildSnapshotEntry(k, e))
			}
			sh.mu.Unlock()
			for _, ent := range batch {
				out <- ent
			}
		}
	}()
	return out
}

func buildSnapshotEntry(key string, e *entry) SnapshotEntry {
	ent := SnapshotEntry{Key: key}
	if !e.expiresAt.IsZero() {
		ent.TTLMs = time.Until(e.expiresAt).Milliseconds()
		if ent.TTLMs < 0 {
			ent.TTLMs = 0
		}
	}
	switch e.dtype {
	case TypeString:
		ent.Type = "string"
		ent.Value = append([]byte(nil), e.value.([]byte)...)
	case TypeHash:
		ent.Type = "hash"
		ent.Fields = make(map[string][]byte)
		for fk, fv := range e.value.(map[string][]byte) {
			ent.Fields[fk] = append([]byte(nil), fv...)
		}
	case TypeList:
		ent.Type = "list"
		l := e.value.(*list.List)
		for el := l.Front(); el != nil; el = el.Next() {
			ent.Elements = append(ent.Elements, append([]byte(nil), el.Value.([]byte)...))
		}
	case TypeSet:
		ent.Type = "set"
		for m := range e.value.(map[string]struct{}) {
			ent.Members = append(ent.Members, []byte(m))
		}
	}
	return ent
}

// ApplySnapshot replaces the database with the provided snapshot entries.
func (s *Store) ApplySnapshot(entries []SnapshotEntry) error {
	s.FlushDB()
	for _, ent := range entries {
		if err := s.applySnapshotEntry(ent); err != nil {
			return fmt.Errorf("apply snapshot: %w", err)
		}
	}
	return nil
}

// ApplySnapshotEntry applies one snapshot row (e.g. during streaming bootstrap). Caller must
// have cleared the DB first if replacing full state (see ApplySnapshot or FlushDB).
func (s *Store) ApplySnapshotEntry(ent SnapshotEntry) error {
	return s.applySnapshotEntry(ent)
}

func (s *Store) applySnapshotEntry(ent SnapshotEntry) error {
	sh := s.shardFor(ent.Key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var exp time.Time
	if ent.TTLMs > 0 {
		exp = time.Now().Add(time.Duration(ent.TTLMs) * time.Millisecond)
	}
	switch ent.Type {
	case "string":
		ne := &entry{value: append([]byte(nil), ent.Value...), dtype: TypeString, expiresAt: exp}
		if err := s.ensureMemoryWithRetry(sh, ent.Key, estimateMem(ent.Key, ne)); err != nil {
			return err
		}
		s.replaceEntry(sh, ent.Key, nil, ne)
	case "hash":
		h := make(map[string][]byte, len(ent.Fields))
		for k, v := range ent.Fields {
			h[k] = append([]byte(nil), v...)
		}
		ne := &entry{value: h, dtype: TypeHash, expiresAt: exp}
		if err := s.ensureMemoryWithRetry(sh, ent.Key, estimateMem(ent.Key, ne)); err != nil {
			return err
		}
		s.replaceEntry(sh, ent.Key, nil, ne)
	case "list":
		l := list.New()
		for _, b := range ent.Elements {
			l.PushBack(append([]byte(nil), b...))
		}
		ne := &entry{value: l, dtype: TypeList, expiresAt: exp}
		if err := s.ensureMemoryWithRetry(sh, ent.Key, estimateMem(ent.Key, ne)); err != nil {
			return err
		}
		s.replaceEntry(sh, ent.Key, nil, ne)
	case "set":
		st := make(map[string]struct{}, len(ent.Members))
		for _, m := range ent.Members {
			st[string(m)] = struct{}{}
		}
		ne := &entry{value: st, dtype: TypeSet, expiresAt: exp}
		if err := s.ensureMemoryWithRetry(sh, ent.Key, estimateMem(ent.Key, ne)); err != nil {
			return err
		}
		s.replaceEntry(sh, ent.Key, nil, ne)
	default:
		return fmt.Errorf("unknown snapshot type %q: %w", ent.Type, errors.New("invalid snapshot"))
	}
	return nil
}
