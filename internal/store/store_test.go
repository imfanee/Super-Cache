// Tests for the in-memory store.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

func testCfg(t *testing.T) *config.Config {
	t.Helper()
	c := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(c)
	c.MaxMemory = "0"
	return c
}

// newTestStore returns a Store with unlimited memory, noeviction policy, and t.Cleanup Close.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewStoreClose(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestSetGetDelete(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Set("a", []byte("v"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get("a")
	if err != nil || string(v) != "v" {
		t.Fatalf("get %q err %v", v, err)
	}
	if s.Del([]string{"a"}) != 1 {
		t.Fatal("del")
	}
	if s.Exists([]string{"a"}) != 0 {
		t.Fatal("exists")
	}
}

func TestWrongType(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.LPush("k", [][]byte{[]byte("x")})
	_, err = s.Get("k")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatalf("expected wrong type, got %v", err)
	}
}

func TestShardingStable(t *testing.T) {
	k := "hello"
	i1 := shardIndex(k)
	i2 := shardIndex(k)
	if i1 != i2 {
		t.Fatal("shard unstable")
	}
}

func TestExpireTTL(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().Add(2 * time.Second)
	_ = s.Set("e", []byte("1"), at)
	if s.TTL("e") <= 0 {
		t.Fatal("ttl")
	}
	ok, err := s.Persist("e")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if s.TTL("e") != -1 {
		t.Fatal("persist")
	}
}

func TestOOMNoEviction(t *testing.T) {
	c := &config.Config{SharedSecret: strings.Repeat("x", 32)}
	config.ApplyDefaults(c)
	c.MaxMemory = "4kb"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Set("a", []byte(strings.Repeat("x", 3000)), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", []byte(strings.Repeat("y", 3000)), time.Time{}); err == nil {
		t.Fatal("expected OOM")
	}
}

func TestEvictAllKeysLRU(t *testing.T) {
	c := &config.Config{SharedSecret: strings.Repeat("y", 32)}
	config.ApplyDefaults(c)
	c.MaxMemory = "256b"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 20; i++ {
		k := string(rune('a' + i%26))
		if err := s.Set(k, []byte("1"), time.Time{}); err != nil {
			t.Fatalf("set %s: %v", k, err)
		}
	}
}

func TestHashSetGet(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.HSet("h", map[string][]byte{"f": []byte("v")})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	v, err := s.HGet("h", "f")
	if err != nil || string(v) != "v" {
		t.Fatal(v, err)
	}
}

func TestListPushPop(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.LPush("l", [][]byte{[]byte("a"), []byte("b")}); err != nil {
		t.Fatal(err)
	}
	out, err := s.LPop("l", 1)
	if err != nil || len(out) != 1 || string(out[0]) != "b" {
		t.Fatalf("%v %v", out, err)
	}
}

func TestSetOps(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.SAdd("s", [][]byte{[]byte("m")}); err != nil {
		t.Fatal(err)
	}
	ok, err := s.SIsMember("s", []byte("m"))
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("a", []byte("1"), time.Time{})
	_, _ = s.HSet("h", map[string][]byte{"x": []byte("y")})
	var entries []SnapshotEntry
	for ch := s.Snapshot(); ; {
		e, ok := <-ch
		if !ok {
			break
		}
		entries = append(entries, e)
	}
	s2, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.ApplySnapshot(entries); err != nil {
		t.Fatal(err)
	}
	v, _ := s2.Get("a")
	if string(v) != "1" {
		t.Fatal(v)
	}
}

func TestConcurrentRead(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("c", []byte("v"), time.Time{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = s.Get("c")
		}()
	}
	wg.Wait()
}

func TestKeysGlob(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("pref:1", []byte("a"), time.Time{})
	_ = s.Set("pref:2", []byte("b"), time.Time{})
	ks := s.Keys("pref:*")
	if len(ks) != 2 {
		t.Fatalf("keys %v", ks)
	}
}

func TestStoreShardDistribution(t *testing.T) {
	s, err := NewStore(testCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("%036x", i)
		if err := s.Set(key, []byte("v"), time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	stats := s.StoreStats()
	if len(stats) != NumShards {
		t.Fatalf("got %d shards", len(stats))
	}
	var sum int
	for _, c := range stats {
		sum += c
	}
	if sum != 10000 {
		t.Fatalf("sum keys %d", sum)
	}
	mean := float64(sum) / float64(NumShards)
	var sq float64
	for _, c := range stats {
		d := float64(c) - mean
		sq += d * d
	}
	std := math.Sqrt(sq / float64(NumShards))
	cv := std / mean
	if cv >= 0.20 {
		t.Fatalf("coefficient of variation %f (mean=%f std=%f) too high", cv, mean, std)
	}
}
