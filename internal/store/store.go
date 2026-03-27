// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package store implements the sharded in-memory key-value store.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/supercache/supercache/internal/config"
)

// NumShards is the default number of key shards (FNV-1a routing).
const NumShards = 256

// DataType identifies the Redis-style type stored for a key.
type DataType int

const (
	// TypeNone means the key does not exist.
	TypeNone DataType = iota
	// TypeString is a byte slice string value.
	TypeString
	// TypeHash is a map of field name to value bytes.
	TypeHash
	// TypeList is a doubly-linked list of byte slices.
	TypeList
	// TypeSet is a set of member strings.
	TypeSet
)

// ErrWrongType indicates the key exists but its type does not match the operation.
var ErrWrongType = errors.New("wrong type")

// ErrOOM is returned when memory is at the limit under noeviction policy.
var ErrOOM = errors.New("OOM command not allowed when used memory > 'maxmemory'")

// entry is a versioned record stored in a shard map.
type entry struct {
	value     interface{}
	expiresAt time.Time
	dtype     DataType
	allElem   *list.Element
	volElem   *list.Element
}

// shard holds a partition of the keyspace with its own lock and LRU metadata.
type shard struct {
	mu sync.RWMutex
	m  map[string]*entry

	allList  *list.List
	allElems map[string]*list.Element
	volList  *list.List
	volElems map[string]*list.Element
}

// Store is a concurrent sharded in-memory database.
type Store struct {
	shards [NumShards]shard
	mem    atomic.Int64
	cfg    atomic.Pointer[config.Config]
	policyCache atomic.Value // string
	maxMemCache atomic.Uint64

	// readLRUTick drives probabilistic LRU touch on GET (1 in 16) to reduce list churn on hot reads.
	readLRUTick atomic.Uint64

	cancel context.CancelFunc
	wg     sync.WaitGroup

	evictCursor atomic.Uint32

	watchMu sync.Mutex
	watch   map[string]uint64
}

// SnapshotEntry is a portable representation of a key for bootstrap snapshots.
type SnapshotEntry struct {
	// Key is the Redis key name.
	Key string `json:"k"`
	// Type is one of string, hash, list, or set.
	Type string `json:"t"`
	// Value holds the raw string value when Type is string.
	Value []byte `json:"v"`
	// Fields holds hash field/value pairs when Type is hash.
	Fields map[string][]byte `json:"f"`
	// Members holds set members when Type is set.
	Members [][]byte `json:"m"`
	// Elements holds list elements in order when Type is list.
	Elements [][]byte `json:"e"`
	// TTLMs is the remaining TTL in milliseconds; zero means no expiry.
	TTLMs int64 `json:"ttl"`
}

// NewStore constructs a Store and starts the active expiry worker.
func NewStore(cfg *config.Config) (*Store, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config: %w", errors.New("invalid argument"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{
		cancel: cancel,
		watch:  make(map[string]uint64),
	}
	s.cfg.Store(cfg)
	s.refreshConfigCache(cfg)
	for i := range s.shards {
		s.shards[i].m = make(map[string]*entry)
		s.shards[i].allList = list.New()
		s.shards[i].allElems = make(map[string]*list.Element)
		s.shards[i].volList = list.New()
		s.shards[i].volElems = make(map[string]*list.Element)
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		runActiveExpiry(ctx, s)
	}()
	return s, nil
}

// Close stops background workers and waits for shutdown.
func (s *Store) Close() {
	s.cancel()
	s.wg.Wait()
}

func shardIndex(key string) int {
	h := fnv.New32a()
	_, _ = h.Write(unsafe.Slice(unsafe.StringData(key), len(key)))
	return int(h.Sum32() % NumShards)
}

func (s *Store) shardFor(key string) *shard {
	return &s.shards[shardIndex(key)]
}

func (s *Store) maxMemBytes() uint64 {
	return s.maxMemCache.Load()
}

func (s *Store) policy() string {
	if v := s.policyCache.Load(); v != nil {
		if pol, ok := v.(string); ok && pol != "" {
			return pol
		}
	}
	return "noeviction"
}

// ReplaceConfig swaps the active configuration (e.g. after SIGHUP hot reload).
func (s *Store) ReplaceConfig(cfg *config.Config) {
	if cfg != nil {
		s.cfg.Store(cfg)
		s.refreshConfigCache(cfg)
	}
}

func (s *Store) refreshConfigCache(cfg *config.Config) {
	if cfg == nil {
		s.policyCache.Store("noeviction")
		s.maxMemCache.Store(0)
		return
	}
	s.policyCache.Store(cfg.NormalizeMaxMemoryPolicy())
	n, err := cfg.MaxMemoryBytes()
	if err != nil {
		s.maxMemCache.Store(0)
		return
	}
	s.maxMemCache.Store(n)
}

func entryOverhead() int64 {
	return int64(unsafe.Sizeof(entry{})) + 64
}

func estimateMem(key string, e *entry) int64 {
	n := entryOverhead() + int64(len(key))
	switch e.dtype {
	case TypeString:
		if b, ok := e.value.([]byte); ok {
			n += int64(len(b))
		}
	case TypeHash:
		h := e.value.(map[string][]byte)
		for fk, fv := range h {
			n += int64(len(fk)) + int64(len(fv))
		}
	case TypeList:
		l := e.value.(*list.List)
		// Keep list memory estimation O(1): walking every element on each list mutation
		// creates O(n^2) behavior for long lists under sustained LPUSH/RPUSH workloads.
		n += int64(l.Len()) * 32
	case TypeSet:
		st := e.value.(map[string]struct{})
		for m := range st {
			n += int64(len(m))
		}
	}
	return n
}

func (s *Store) addMem(delta int64) {
	if delta == 0 {
		return
	}
	s.mem.Add(delta)
}

func (s *Store) isExpired(e *entry) bool {
	if e.expiresAt.IsZero() {
		return false
	}
	return time.Now().After(e.expiresAt)
}

func (e *entry) hasTTL() bool {
	return !e.expiresAt.IsZero()
}

func (s *Store) purgeEntryLocked(sh *shard, key string, e *entry) {
	delta := -estimateMem(key, e)
	s.removeFromLRU(sh, key, e)
	delete(sh.m, key)
	s.addMem(delta)
	s.bumpWatchKey(key)
}

func (s *Store) removeFromLRU(sh *shard, key string, e *entry) {
	if e.allElem != nil {
		sh.allList.Remove(e.allElem)
		delete(sh.allElems, key)
		e.allElem = nil
	}
	if e.volElem != nil {
		sh.volList.Remove(e.volElem)
		delete(sh.volElems, key)
		e.volElem = nil
	}
}

func (s *Store) touchLRU(sh *shard, key string, e *entry) {
	touchAllKeysLRU(s, sh, key, e)
	touchVolatileLRU(s, sh, key, e)
}

func (s *Store) replaceEntry(sh *shard, key string, oldE, newE *entry) {
	if oldE != nil {
		s.removeFromLRU(sh, key, oldE)
		s.addMem(-estimateMem(key, oldE))
	}
	sh.m[key] = newE
	s.addMem(estimateMem(key, newE))
	lruAfterSet(s, sh, key, newE)
	s.bumpWatchKey(key)
}

// Get returns the string value for key.
func (s *Store) Get(key string) ([]byte, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok {
		return nil, nil
	}
	if s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		return nil, nil
	}
	if e.dtype != TypeString {
		return nil, fmt.Errorf("get: %w", ErrWrongType)
	}
	pol := s.policy()
	if pol == "allkeys-lru" || pol == "volatile-lru" {
		s.touchLRU(sh, key, e)
	} else if s.readLRUTick.Add(1)%16 == 0 {
		s.touchLRU(sh, key, e)
	}
	return e.value.([]byte), nil
}

// Set stores a string value and optional expiry (zero disables TTL).
func (s *Store) Set(key string, val []byte, expiresAt time.Time) error {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	return s.setLocked(sh, key, val, expiresAt, false, false)
}

func (s *Store) setLocked(sh *shard, key string, val []byte, expiresAt time.Time, nx, xx bool) error {
	old, had := sh.m[key]
	if had && s.isExpired(old) {
		s.purgeEntryLocked(sh, key, old)
		had = false
		old = nil
	}
	if nx && had {
		return nil
	}
	if xx && !had {
		return nil
	}
	ne := &entry{value: append([]byte(nil), val...), dtype: TypeString, expiresAt: expiresAt}
	newBytes := estimateMem(key, ne)
	var oldBytes int64
	if had {
		oldBytes = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newBytes-oldBytes); err != nil {
		return err
	}
	s.replaceEntry(sh, key, old, ne)
	return nil
}

// evictUntilFits evicts keys (without holding any shard lock) until mem+delta fits maxmemory.
func (s *Store) evictUntilFits(delta int64) error {
	if s.maxMemBytes() == 0 {
		return nil
	}
	for s.mem.Load()+delta > int64(s.maxMemBytes()) {
		if !evictFromStore(s, "") {
			return fmt.Errorf("store: %w", ErrOOM)
		}
	}
	return nil
}

// ensureMemoryWithRetry is called with sh.mu held; it may temporarily unlock to run eviction.
func (s *Store) ensureMemoryWithRetry(sh *shard, key string, delta int64) error {
	if s.maxMemBytes() == 0 || delta <= 0 {
		return nil
	}
	for s.mem.Load()+delta > int64(s.maxMemBytes()) {
		sh.mu.Unlock()
		ok := evictFromStore(s, key)
		sh.mu.Lock()
		if !ok {
			return fmt.Errorf("store: %w", ErrOOM)
		}
	}
	return nil
}

// SetNX sets key only if it does not exist.
func (s *Store) SetNX(key string, val []byte, expiresAt time.Time) (bool, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	old, had := sh.m[key]
	if had && s.isExpired(old) {
		s.purgeEntryLocked(sh, key, old)
		had = false
	}
	if had {
		return false, nil
	}
	ne := &entry{value: append([]byte(nil), val...), dtype: TypeString, expiresAt: expiresAt}
	delta := estimateMem(key, ne)
	if err := s.ensureMemoryWithRetry(sh, key, delta); err != nil {
		return false, err
	}
	s.replaceEntry(sh, key, nil, ne)
	return true, nil
}

// SetXX sets key only if it already exists.
func (s *Store) SetXX(key string, val []byte, expiresAt time.Time) (bool, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	old, had := sh.m[key]
	if !had || s.isExpired(old) {
		if had {
			s.purgeEntryLocked(sh, key, old)
		}
		return false, nil
	}
	ne := &entry{value: append([]byte(nil), val...), dtype: TypeString, expiresAt: expiresAt}
	newB := estimateMem(key, ne)
	oldB := estimateMem(key, old)
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return false, err
	}
	s.replaceEntry(sh, key, old, ne)
	return true, nil
}

// GetSet returns the previous string value and sets the new value.
func (s *Store) GetSet(key string, val []byte, expiresAt time.Time) ([]byte, bool, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	old, had := sh.m[key]
	if had && s.isExpired(old) {
		s.purgeEntryLocked(sh, key, old)
		had = false
		old = nil
	}
	var prev []byte
	if had {
		if old.dtype != TypeString {
			return nil, false, fmt.Errorf("getset: %w", ErrWrongType)
		}
		prev = append([]byte(nil), old.value.([]byte)...)
	}
	ne := &entry{value: append([]byte(nil), val...), dtype: TypeString, expiresAt: expiresAt}
	newB := estimateMem(key, ne)
	var oldB int64
	if had {
		oldB = estimateMem(key, old)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return nil, false, err
	}
	s.replaceEntry(sh, key, old, ne)
	return prev, true, nil
}

// Append appends to a string key and returns the new length.
func (s *Store) Append(key string, tail []byte) (int64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
		e = nil
	}
	if !ok {
		ne := &entry{value: append([]byte(nil), tail...), dtype: TypeString}
		if err := s.ensureMemoryWithRetry(sh, key, estimateMem(key, ne)); err != nil {
			return 0, err
		}
		s.replaceEntry(sh, key, nil, ne)
		return int64(len(tail)), nil
	}
	if e.dtype != TypeString {
		return 0, fmt.Errorf("append: %w", ErrWrongType)
	}
	old := e.value.([]byte)
	newVal := append(append([]byte(nil), old...), tail...)
	ne := &entry{value: newVal, dtype: TypeString, expiresAt: e.expiresAt}
	newB := estimateMem(key, ne)
	oldB := estimateMem(key, e)
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, e, ne)
	return int64(len(newVal)), nil
}

// IncrBy increments a string-encoded integer and returns the new value.
func (s *Store) IncrBy(key string, delta int64) (int64, error) {
	return s.incrBy(key, delta)
}

// DecrBy decrements a string-encoded integer and returns the new value.
func (s *Store) DecrBy(key string, delta int64) (int64, error) {
	return s.incrBy(key, -delta)
}

func (s *Store) incrBy(key string, delta int64) (int64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if ok && s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		ok = false
	}
	var cur int64
	if ok {
		if e.dtype != TypeString {
			return 0, fmt.Errorf("incr: %w", ErrWrongType)
		}
		s := strings.TrimSpace(string(e.value.([]byte)))
		if s == "" {
			cur = 0
		} else {
			v, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("not an integer: %w", err)
			}
			cur = v
		}
	}
	next, ok2 := addInt64(cur, delta)
	if !ok2 {
		return 0, fmt.Errorf("increment or decrement would overflow: %w", strconv.ErrRange)
	}
	out := strconv.FormatInt(next, 10)
	ne := &entry{value: []byte(out), dtype: TypeString}
	if ok {
		ne.expiresAt = e.expiresAt
	}
	newB := estimateMem(key, ne)
	var oldB int64
	if ok {
		oldB = estimateMem(key, e)
	}
	if err := s.ensureMemoryWithRetry(sh, key, newB-oldB); err != nil {
		return 0, err
	}
	s.replaceEntry(sh, key, e, ne)
	return next, nil
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

// MGet returns values for keys in order; nil slice means missing.
func (s *Store) MGet(keys []string) ([][]byte, error) {
	out := make([][]byte, len(keys))
	for i, k := range keys {
		v, err := s.Get(k)
		if err != nil {
			return nil, fmt.Errorf("mget: %w", err)
		}
		if v != nil {
			cp := append([]byte(nil), v...)
			out[i] = cp
		}
	}
	return out, nil
}

// MSet sets many keys without TTL.
func (s *Store) MSet(pairs map[string][]byte) error {
	keys := sortedKeys(pairs)
	for _, k := range keys {
		if err := s.Set(k, pairs[k], time.Time{}); err != nil {
			return fmt.Errorf("mset: %w", err)
		}
	}
	return nil
}

// MSetNX sets all keys only if none exist.
func (s *Store) MSetNX(pairs map[string][]byte) (bool, error) {
	if len(pairs) == 0 {
		return true, nil
	}
	locks := s.acquireShardsInOrder(pairs)
	for _, mu := range locks {
		mu.Lock()
	}
	for k := range pairs {
		sh := s.shardFor(k)
		if e, ok := sh.m[k]; ok && !s.isExpired(e) {
			for _, mu := range locks {
				mu.Unlock()
			}
			return false, nil
		}
	}
	var delta int64
	for k, v := range pairs {
		sh := s.shardFor(k)
		old := sh.m[k]
		if old != nil && s.isExpired(old) {
			s.purgeEntryLocked(sh, k, old)
		}
		ne := &entry{value: append([]byte(nil), v...), dtype: TypeString}
		delta += estimateMem(k, ne)
	}
	for _, mu := range locks {
		mu.Unlock()
	}
	if err := s.evictUntilFits(delta); err != nil {
		return false, fmt.Errorf("msetnx: %w", err)
	}
	for _, mu := range locks {
		mu.Lock()
	}
	defer func() {
		for _, mu := range locks {
			mu.Unlock()
		}
	}()
	for k := range pairs {
		sh := s.shardFor(k)
		if e, ok := sh.m[k]; ok && !s.isExpired(e) {
			return false, nil
		}
	}
	for k, v := range pairs {
		sh := s.shardFor(k)
		old := sh.m[k]
		if old != nil && s.isExpired(old) {
			s.purgeEntryLocked(sh, k, old)
			old = nil
		}
		ne := &entry{value: append([]byte(nil), v...), dtype: TypeString}
		s.replaceEntry(sh, k, old, ne)
	}
	return true, nil
}

func (s *Store) acquireShardsInOrder(pairs map[string][]byte) []*sync.RWMutex {
	return s.acquireShardsInOrderKeysMapKeys(pairs)
}

func (s *Store) acquireShardsInOrderKeys(keys []string) []*sync.RWMutex {
	seen := make(map[int]struct{})
	var idx []int
	for _, k := range keys {
		si := shardIndex(k)
		if _, ok := seen[si]; ok {
			continue
		}
		seen[si] = struct{}{}
		idx = append(idx, si)
	}
	sortShardIndices(idx)
	out := make([]*sync.RWMutex, 0, len(idx))
	for _, si := range idx {
		out = append(out, &s.shards[si].mu)
	}
	return out
}

func (s *Store) acquireShardsInOrderKeysMapKeys(pairs map[string][]byte) []*sync.RWMutex {
	seen := make(map[int]struct{})
	var idx []int
	for k := range pairs {
		si := shardIndex(k)
		if _, ok := seen[si]; ok {
			continue
		}
		seen[si] = struct{}{}
		idx = append(idx, si)
	}
	sortShardIndices(idx)
	out := make([]*sync.RWMutex, 0, len(idx))
	for _, si := range idx {
		out = append(out, &s.shards[si].mu)
	}
	return out
}

func sortShardIndices(idx []int) {
	for i := 0; i < len(idx); i++ {
		for j := i + 1; j < len(idx); j++ {
			if idx[j] < idx[i] {
				idx[i], idx[j] = idx[j], idx[i]
			}
		}
	}
}

func sortedKeys(pairs map[string][]byte) []string {
	keys := make([]string, 0, len(pairs))
	for k := range pairs {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// Del deletes keys and returns the count removed.
func (s *Store) Del(keys []string) int {
	n := 0
	for _, key := range keys {
		sh := s.shardFor(key)
		sh.mu.Lock()
		e, ok := sh.m[key]
		if ok && s.isExpired(e) {
			s.purgeEntryLocked(sh, key, e)
			n++
			sh.mu.Unlock()
			continue
		}
		if ok {
			s.purgeEntryLocked(sh, key, e)
			n++
		}
		sh.mu.Unlock()
	}
	return n
}

// Exists counts how many of the given keys exist.
func (s *Store) Exists(keys []string) int {
	n := 0
	for _, key := range keys {
		sh := s.shardFor(key)
		sh.mu.Lock()
		e, ok := sh.m[key]
		if ok && s.isExpired(e) {
			s.purgeEntryLocked(sh, key, e)
			sh.mu.Unlock()
			continue
		}
		if ok {
			n++
		}
		sh.mu.Unlock()
	}
	return n
}

// Type returns the Redis type name or empty if missing.
func (s *Store) Type(key string) string {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok {
		return "none"
	}
	if s.isExpired(e) {
		s.purgeEntryLocked(sh, key, e)
		return "none"
	}
	switch e.dtype {
	case TypeString:
		return "string"
	case TypeHash:
		return "hash"
	case TypeList:
		return "list"
	case TypeSet:
		return "set"
	default:
		return "none"
	}
}

// Rename renames a key to newKey.
func (s *Store) Rename(src, dst string) error {
	if src == dst {
		return nil
	}
	locks := s.acquireShardsInOrderKeys([]string{src, dst})
	for _, mu := range locks {
		mu.Lock()
	}
	defer func() {
		for _, mu := range locks {
			mu.Unlock()
		}
	}()
	shS := s.shardFor(src)
	shD := s.shardFor(dst)
	e, ok := shS.m[src]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(shS, src, e)
		}
		return fmt.Errorf("no such key: %w", errors.New("not found"))
	}
	if oldDst, ok2 := shD.m[dst]; ok2 {
		if s.isExpired(oldDst) {
			s.purgeEntryLocked(shD, dst, oldDst)
		} else {
			s.purgeEntryLocked(shD, dst, oldDst)
		}
	}
	s.removeFromLRU(shS, src, e)
	delete(shS.m, src)
	s.addMem(-estimateMem(src, e))
	ne := cloneEntryForRename(e)
	s.replaceEntry(shD, dst, nil, ne)
	return nil
}

func cloneEntryForRename(e *entry) *entry {
	out := &entry{dtype: e.dtype, expiresAt: e.expiresAt}
	switch e.dtype {
	case TypeString:
		out.value = append([]byte(nil), e.value.([]byte)...)
	case TypeHash:
		h := e.value.(map[string][]byte)
		nh := make(map[string][]byte, len(h))
		for k, v := range h {
			nh[k] = append([]byte(nil), v...)
		}
		out.value = nh
	case TypeList:
		old := e.value.(*list.List)
		nl := list.New()
		for el := old.Front(); el != nil; el = el.Next() {
			nl.PushBack(append([]byte(nil), el.Value.([]byte)...))
		}
		out.value = nl
	case TypeSet:
		st := e.value.(map[string]struct{})
		ns := make(map[string]struct{}, len(st))
		for m := range st {
			ns[m] = struct{}{}
		}
		out.value = ns
	}
	return out
}

// RenameNX renames only if dst does not exist.
func (s *Store) RenameNX(src, dst string) (bool, error) {
	if src == dst {
		return true, nil
	}
	locks := s.acquireShardsInOrderKeys([]string{src, dst})
	for _, mu := range locks {
		mu.Lock()
	}
	defer func() {
		for _, mu := range locks {
			mu.Unlock()
		}
	}()
	shS := s.shardFor(src)
	shD := s.shardFor(dst)
	e, ok := shS.m[src]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(shS, src, e)
		}
		return false, nil
	}
	if d, ok2 := shD.m[dst]; ok2 && !s.isExpired(d) {
		return false, nil
	}
	if d, ok2 := shD.m[dst]; ok2 {
		s.purgeEntryLocked(shD, dst, d)
	}
	s.removeFromLRU(shS, src, e)
	delete(shS.m, src)
	s.addMem(-estimateMem(src, e))
	ne := cloneEntryForRename(e)
	s.replaceEntry(shD, dst, nil, ne)
	return true, nil
}

// Keys returns all keys matching pattern (glob * ? [a-z]).
func (s *Store) Keys(pattern string) []string {
	var out []string
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for k, e := range sh.m {
			if s.isExpired(e) {
				continue
			}
			if matchGlob(pattern, k) {
				out = append(out, k)
			}
		}
		sh.mu.Unlock()
	}
	return out
}

// DBSize returns the total number of keys.
func (s *Store) DBSize() int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for k, e := range sh.m {
			if s.isExpired(e) {
				continue
			}
			_ = k
			n++
		}
		sh.mu.Unlock()
	}
	return n
}

// ExpireKeyCount returns the number of keys that currently have a TTL set.
func (s *Store) ExpireKeyCount() int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for _, e := range sh.m {
			if s.isExpired(e) {
				continue
			}
			if e.hasTTL() {
				n++
			}
		}
		sh.mu.Unlock()
	}
	return n
}

// StoreStats returns the number of keys per shard index. Each shard lock is held at most one at a time.
func (s *Store) StoreStats() map[int]int {
	out := make(map[int]int, NumShards)
	for i := 0; i < NumShards; i++ {
		sh := &s.shards[i]
		sh.mu.RLock()
		n := len(sh.m)
		sh.mu.RUnlock()
		out[i] = n
	}
	return out
}

// FlushDB removes all keys.
func (s *Store) FlushDB() {
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for k, e := range sh.m {
			s.purgeEntryLocked(sh, k, e)
		}
		sh.mu.Unlock()
	}
	s.watchMu.Lock()
	s.watch = make(map[string]uint64)
	s.watchMu.Unlock()
}

// Expire sets a TTL in seconds from now.
func (s *Store) Expire(key string, sec int64) (bool, error) {
	if sec <= 0 {
		return false, fmt.Errorf("invalid expire: %w", errors.New("non-positive"))
	}
	at := time.Now().Add(time.Duration(sec) * time.Second)
	return s.ExpireAt(key, at)
}

// ExpireAt sets absolute expiry.
func (s *Store) ExpireAt(key string, at time.Time) (bool, error) {
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
	old := e
	ne := cloneEntryForRename(old)
	ne.expiresAt = at
	s.replaceEntry(sh, key, old, ne)
	return true, nil
}

// TTL returns seconds to live, -1 if no TTL, -2 if missing.
func (s *Store) TTL(key string) int64 {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[key]
	if !ok || s.isExpired(e) {
		if ok {
			s.purgeEntryLocked(sh, key, e)
		}
		return -2
	}
	if e.expiresAt.IsZero() {
		return -1
	}
	d := time.Until(e.expiresAt)
	if d <= 0 {
		s.purgeEntryLocked(sh, key, e)
		return -2
	}
	return int64(d / time.Second)
}

// Persist removes TTL.
func (s *Store) Persist(key string) (bool, error) {
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
	if e.expiresAt.IsZero() {
		return false, nil
	}
	old := e
	ne := cloneEntryForRename(old)
	ne.expiresAt = time.Time{}
	s.replaceEntry(sh, key, old, ne)
	return true, nil
}

// MemBytes returns approximate memory usage.
func (s *Store) MemBytes() int64 {
	return s.mem.Load()
}
