// Additional coverage tests for eviction, SCAN, MSETNX, rename, snapshots, and hash incr.
package store

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

func secretCfg() *config.Config {
	c := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(c)
	return c
}

func TestNewStoreNilConfig(t *testing.T) {
	_, err := NewStore(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMemBytesMonotonic(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.MemBytes() != 0 {
		t.Fatal("empty store")
	}
	_ = s.Set("a", []byte("hello"), time.Time{})
	m1 := s.MemBytes()
	if m1 <= 0 {
		t.Fatal(m1)
	}
	_ = s.Set("b", []byte("world"), time.Time{})
	if s.MemBytes() <= m1 {
		t.Fatal("expected growth")
	}
	s.Del([]string{"a", "b"})
	if s.MemBytes() >= m1 {
		t.Fatal("expected drop after del")
	}
}

func TestWatchVersionIncrements(t *testing.T) {
	s := newTestStore(t)
	if s.WatchVersion("k") != 0 {
		t.Fatal()
	}
	_ = s.Set("k", []byte("1"), time.Time{})
	if s.WatchVersion("k") == 0 {
		t.Fatal()
	}
	v1 := s.WatchVersion("k")
	_ = s.Set("k", []byte("2"), time.Time{})
	if s.WatchVersion("k") <= v1 {
		t.Fatal()
	}
}

func TestScanPaginationAndDefaults(t *testing.T) {
	s := newTestStore(t)
	next, keys := s.Scan(0, "", 0, "")
	if next != 0 || keys != nil {
		t.Fatalf("empty: next=%v keys=%v", next, keys)
	}
	_ = s.Set("a", []byte("1"), time.Time{})
	_ = s.Set("b", []byte("2"), time.Time{})
	_ = s.Set("c", []byte("3"), time.Time{})
	next, keys = s.Scan(0, "*", 2, "")
	if next != 2 || len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("page1: next=%d keys=%v", next, keys)
	}
	next, keys = s.Scan(next, "*", 10, "")
	if next != 0 || len(keys) != 1 || keys[0] != "c" {
		t.Fatalf("page2: next=%d keys=%v", next, keys)
	}
	next, keys = s.Scan(99, "*", 10, "")
	if next != 0 || keys != nil {
		t.Fatalf("past end: next=%d keys=%v", next, keys)
	}
	_ = s.Set("pref_x", []byte("z"), time.Time{})
	next, keys = s.Scan(0, "pref_*", 10, "")
	if len(keys) != 1 || keys[0] != "pref_x" {
		t.Fatal(keys)
	}
}

func TestScanFilterByType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("str1", []byte("v"), time.Time{})
	_, _ = s.HSet("h1", map[string][]byte{"f": []byte("v")})
	var next uint64
	var all []string
	for {
		var batch []string
		next, batch = s.Scan(next, "*", 5, "string")
		all = append(all, batch...)
		if next == 0 {
			break
		}
	}
	if len(all) != 1 || all[0] != "str1" {
		t.Fatalf("string filter: %v", all)
	}
	next = 0
	all = all[:0]
	for {
		var batch []string
		next, batch = s.Scan(next, "*", 5, "HASH")
		all = append(all, batch...)
		if next == 0 {
			break
		}
	}
	if len(all) != 1 || all[0] != "h1" {
		t.Fatalf("hash filter: %v", all)
	}
}

func TestSampleKeysForDebug(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("a", []byte("1"), time.Time{})
	_ = s.Set("b", []byte("2"), time.Now().Add(time.Hour))
	out := s.SampleKeysForDebug(0)
	if len(out) < 2 {
		t.Fatal(len(out))
	}
	for _, m := range out {
		if m["key"] == nil || m["type"] == nil {
			t.Fatalf("%v", m)
		}
	}
	out2 := s.SampleKeysForDebug(1)
	if len(out2) != 1 {
		t.Fatal(len(out2))
	}
}

func twoKeysSameShard(t *testing.T) (a, b string) {
	t.Helper()
	seen := make(map[int]string)
	for i := 0; i < 10000; i++ {
		k := fmt.Sprintf("shardq-%d", i)
		si := shardIndex(k)
		if first, ok := seen[si]; ok {
			return first, k
		}
		seen[si] = k
	}
	t.Fatal("could not find two keys in same shard")
	return "", ""
}

func TestMSetNXTwoKeysSameShard(t *testing.T) {
	a, b := twoKeysSameShard(t)
	s := newTestStore(t)
	ok, err := s.MSetNX(map[string][]byte{a: []byte("1"), b: []byte("2")})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestRenameSameShard(t *testing.T) {
	a, b := twoKeysSameShard(t)
	s := newTestStore(t)
	_ = s.Set(a, []byte("v"), time.Time{})
	if err := s.Rename(a, b); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get(b)
	if string(v) != "v" {
		t.Fatal(string(v))
	}
}

func TestRenameNXSameShard(t *testing.T) {
	a, b := twoKeysSameShard(t)
	s := newTestStore(t)
	_ = s.Set(a, []byte("v"), time.Time{})
	ok, err := s.RenameNX(a, b)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestMSetNXSemantics(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.MSetNX(map[string][]byte{})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = s.MSetNX(map[string][]byte{"x": []byte("1"), "y": []byte("2")})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	v, _ := s.Get("x")
	if string(v) != "1" {
		t.Fatal(v)
	}
	ok, err = s.MSetNX(map[string][]byte{"x": []byte("9"), "z": []byte("3")})
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("exp", []byte("old"), time.Now().Add(-time.Second))
	ok, err = s.MSetNX(map[string][]byte{"exp": []byte("new"), "fresh": []byte("v")})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	v, _ = s.Get("exp")
	if string(v) != "new" {
		t.Fatal(string(v))
	}
}

func TestMSetNXMultiShardAndSortedMSet(t *testing.T) {
	s := newTestStore(t)
	pairs := map[string][]byte{}
	for i := 0; i < 50; i++ {
		pairs[fmt.Sprintf("mk-%d", i)] = []byte("v")
	}
	if err := s.MSet(pairs); err != nil {
		t.Fatal(err)
	}
	if s.DBSize() != 50 {
		t.Fatal(s.DBSize())
	}
}

func TestMSetNXOOMNoEviction(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	big := []byte(strings.Repeat("x", 400))
	_ = s.Set("fill", big, time.Time{})
	ok, err := s.MSetNX(map[string][]byte{"a": big, "b": big})
	if err == nil || ok {
		t.Fatalf("expected OOM: ok=%v err=%v", ok, err)
	}
}

func TestRenameNX(t *testing.T) {
	s := newTestStore(t)
	ok, err := s.RenameNX("a", "a")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("src", []byte("v"), time.Time{})
	ok, err = s.RenameNX("src", "dst")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	v, _ := s.Get("dst")
	if string(v) != "v" {
		t.Fatal(v)
	}
	ok, err = s.RenameNX("nosrc", "x")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("keep", []byte("1"), time.Time{})
	ok, err = s.RenameNX("src2", "keep")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("src3", []byte("mv"), time.Time{})
	_ = s.Set("occupied", []byte("block"), time.Time{})
	ok, err = s.RenameNX("src3", "occupied")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("src4", []byte("z"), time.Time{})
	_ = s.Set("gone", []byte("e"), time.Now().Add(-time.Hour))
	ok, err = s.RenameNX("src4", "gone")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	v, _ = s.Get("gone")
	if string(v) != "z" {
		t.Fatal(string(v))
	}
}

func TestRenameNXCloneHash(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("hsrc", map[string][]byte{"f": []byte("1")})
	ok, err := s.RenameNX("hsrc", "hdst")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	m, err := s.HGetAll("hdst")
	if err != nil || string(m["f"]) != "1" {
		t.Fatal(m, err)
	}
}

func TestHIncrByAndFloat(t *testing.T) {
	s := newTestStore(t)
	n, err := s.HIncrBy("nh", "f", 3)
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	n, err = s.HIncrBy("nh", "f", 2)
	if err != nil || n != 5 {
		t.Fatal(n, err)
	}
	_, _ = s.HSet("hx", map[string][]byte{"f": []byte("notnum")})
	_, err = s.HIncrBy("hx", "f", 1)
	if err == nil {
		t.Fatal("expected parse error")
	}
	_ = s.Set("str", []byte("x"), time.Time{})
	_, err = s.HIncrBy("str", "f", 1)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	f, err := s.HIncrByFloat("hf", "x", 0.5)
	if err != nil || f != 0.5 {
		t.Fatal(f, err)
	}
	f, err = s.HIncrByFloat("hf", "x", 0.25)
	if err != nil || f != 0.75 {
		t.Fatal(f, err)
	}
	_, _ = s.HSet("badf", map[string][]byte{"f": []byte("nope")})
	_, err = s.HIncrByFloat("badf", "f", 1)
	if err == nil {
		t.Fatal("expected float parse error")
	}
	_, err = s.HIncrByFloat("str", "f", 1)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, _ = s.HSet("ov", map[string][]byte{"f": []byte("9223372036854775807")})
	_, err = s.HIncrBy("ov", "f", 1)
	if err == nil {
		t.Fatal("overflow")
	}
}

func TestHSetNXAndReplaceSet(t *testing.T) {
	s := newTestStore(t)
	n, err := s.HSetNX("hnx", "a", []byte("1"))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	n, err = s.HSetNX("hnx", "a", []byte("2"))
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err := s.ReplaceSet("rs", [][]byte{[]byte("a"), []byte("b")}); err != nil {
		t.Fatal(err)
	}
	mem, err := s.SMembers("rs")
	if err != nil || len(mem) != 2 {
		t.Fatal(len(mem), err)
	}
}

func TestApplySnapshotEntryErrors(t *testing.T) {
	s := newTestStore(t)
	err := s.ApplySnapshotEntry(SnapshotEntry{Key: "k", Type: "unknown"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestApplySnapshotReplace(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("old", []byte("1"), time.Time{})
	entries := []SnapshotEntry{
		{Key: "a", Type: "string", Value: []byte("x")},
		{Key: "h", Type: "hash", Fields: map[string][]byte{"f": []byte("y")}},
		{Key: "l", Type: "list", Elements: [][]byte{[]byte("1"), []byte("2")}},
		{Key: "st", Type: "set", Members: [][]byte{[]byte("m")}},
	}
	if err := s.ApplySnapshot(entries); err != nil {
		t.Fatal(err)
	}
	if s.Exists([]string{"old"}) != 0 {
		t.Fatal("flush")
	}
	if s.DBSize() != 4 {
		t.Fatal(s.DBSize())
	}
}

func TestEvictFromStoreUnknownPolicy(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "1024b"
	c.MaxMemoryPolicy = "not-a-listed-policy"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if evictFromStore(s, "") {
		t.Fatal("default policy should not evict")
	}
}

func TestVolatileLRUEvictionPath(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "4096b"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	val := []byte(strings.Repeat("v", 80))
	for i := 0; i < 40; i++ {
		k := fmt.Sprintf("vl%d", i)
		if err := s.Set(k, val, time.Now().Add(30*time.Second)); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if err := s.Set("push", make([]byte, 2000), time.Now().Add(30*time.Second)); err != nil {
		t.Logf("eviction may vary: %v", err)
	}
}

func TestEvictVolatileLRUDirect(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "2048b"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 30; i++ {
		_ = s.Set(fmt.Sprintf("ev%d", i), []byte("x"), time.Now().Add(time.Hour))
	}
	_ = evictFromStore(s, "")
}

func TestAllKeysRandomEviction(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "4096b"
	c.MaxMemoryPolicy = "allkeys-random"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for round := 0; round < 200; round++ {
		if evictFromStore(s, "") {
			break
		}
		_ = s.Set(fmt.Sprintf("r%d", round), []byte(strings.Repeat("z", 100)), time.Time{})
	}
}

func TestVolatileRandomEviction(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "4096b"
	c.MaxMemoryPolicy = "volatile-random"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for round := 0; round < 200; round++ {
		if evictFromStore(s, "") {
			break
		}
		_ = s.Set(fmt.Sprintf("vr%d", round), []byte(strings.Repeat("z", 80)), time.Now().Add(time.Minute))
	}
}

func TestVolatileTTLEviction(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "8192b"
	c.MaxMemoryPolicy = "volatile-ttl"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	base := time.Now().Add(time.Hour)
	_ = s.Set("early", []byte(strings.Repeat("a", 200)), base.Add(-time.Minute))
	_ = s.Set("late", []byte(strings.Repeat("b", 200)), base.Add(time.Minute))
	for i := 0; i < 50; i++ {
		_ = evictFromStore(s, "")
	}
}

func TestTouchVolatileLRUOnGet(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("ttl", []byte("1"), time.Now().Add(time.Hour))
	_, _ = s.Get("ttl")
	_ = s.Set("persist", []byte("2"), time.Time{})
	_, _ = s.Get("persist")
}

func TestGetProbabilisticTouchLRU(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("p", []byte("v"), time.Time{})
	for i := 0; i < 32; i++ {
		_, _ = s.Get("p")
	}
}

func TestLPushXRPushXExistingList(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lx", [][]byte{[]byte("a")})
	n, err := s.LPushX("lx", [][]byte{[]byte("b")})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	_, _ = s.RPush("rx", [][]byte{[]byte("1")})
	n, err = s.RPushX("rx", [][]byte{[]byte("2")})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestActiveExpiryHighExpiredRatio(t *testing.T) {
	s := newTestStore(t)
	keys := keysForShard(0, 30)
	for _, k := range keys {
		_ = s.Set(k, []byte("x"), time.Now().Add(20*time.Millisecond))
	}
	time.Sleep(150 * time.Millisecond)
	for i := 0; i < 10; i++ {
		sampleAllShards(s)
	}
}

func TestRandomSampleKeysLargeShard(t *testing.T) {
	s := newTestStore(t)
	keys := keysForShard(0, 30)
	for _, k := range keys {
		_ = s.Set(k, []byte("v"), time.Time{})
	}
	sampleAllShards(s)
}

func keysForShard(want int, need int) []string {
	var out []string
	for i := 0; len(out) < need; i++ {
		k := fmt.Sprintf("sk-%d", i)
		if shardIndex(k) == want {
			out = append(out, k)
		}
	}
	return out
}

func TestConcurrentMSetNX(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				k := fmt.Sprintf("c%d-%d", id, i)
				_, _ = s.MSetNX(map[string][]byte{k: []byte("v")})
			}
		}(g)
	}
	wg.Wait()
}

func TestSnapshotChannelDrain(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("a"), time.Time{})
	ch := s.Snapshot()
	n := 0
	for range ch {
		n++
	}
	if n != 1 {
		t.Fatal(n)
	}
}

func TestRenameListAndSetClonePaths(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lsrc", [][]byte{[]byte("a"), []byte("b")})
	if err := s.Rename("lsrc", "ldst"); err != nil {
		t.Fatal(err)
	}
	out, err := s.LRange("ldst", 0, -1)
	if err != nil || len(out) != 2 {
		t.Fatal(len(out), err)
	}
	_, _ = s.SAdd("ssrc", [][]byte{[]byte("x"), []byte("y")})
	if err := s.Rename("ssrc", "sdst"); err != nil {
		t.Fatal(err)
	}
	mem, err := s.SMembers("sdst")
	if err != nil || len(mem) != 2 {
		t.Fatal(len(mem), err)
	}
}

func TestLRemAllAndFromTail(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lr", [][]byte{[]byte("a"), []byte("b"), []byte("a"), []byte("a")})
	n, err := s.LRem("lr", 0, []byte("a"))
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	_, _ = s.LPush("lr2", [][]byte{[]byte("x"), []byte("y"), []byte("x")})
	n, err = s.LRem("lr2", -1, []byte("x"))
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestLTrimPurgeBranches(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lt", [][]byte{[]byte("1"), []byte("2"), []byte("3")})
	if err := s.LTrim("lt", 2, 1); err != nil {
		t.Fatal(err)
	}
	if s.Exists([]string{"lt"}) != 0 {
		t.Fatal("expected empty key removed")
	}
	_, _ = s.LPush("lt2", [][]byte{[]byte("a")})
	if err := s.LTrim("lt2", 0, 10); err != nil {
		t.Fatal(err)
	}
}

func TestLIndexEmptyAndOOR(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("emp", [][]byte{[]byte("only")})
	_, _ = s.LPop("emp", 1)
	v, err := s.LIndex("emp", 0)
	if err != nil || v != nil {
		t.Fatal(v, err)
	}
	_, _ = s.LPush("idx", [][]byte{[]byte("a"), []byte("b")})
	v, err = s.LIndex("idx", 99)
	if err != nil || v != nil {
		t.Fatal(v, err)
	}
}

func TestReplaceSetPreservesTTL(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("rs", []byte("x"), time.Now().Add(time.Hour))
	if err := s.ReplaceSet("rs", [][]byte{[]byte("m1"), []byte("m2")}); err != nil {
		t.Fatal(err)
	}
	if s.TTL("rs") <= 0 {
		t.Fatal("ttl lost")
	}
}

func TestHDelRemovesKeyWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("hx", map[string][]byte{"f": []byte("v")})
	n, err := s.HDel("hx", []string{"f"})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if s.Exists([]string{"hx"}) != 0 {
		t.Fatal("hash should be gone")
	}
}

func TestExpireInvalidSec(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("e", []byte("1"), time.Time{})
	ok, err := s.Expire("e", 0)
	if err == nil || ok {
		t.Fatal(ok, err)
	}
}

func TestTTLBranches(t *testing.T) {
	s := newTestStore(t)
	if s.TTL("missing") != -2 {
		t.Fatal()
	}
	_ = s.Set("p", []byte("1"), time.Time{})
	if s.TTL("p") != -1 {
		t.Fatal()
	}
	_ = s.Set("x", []byte("1"), time.Now().Add(-time.Second))
	if s.TTL("x") != -2 {
		t.Fatal(s.TTL("x"))
	}
}

func TestGetSetAppendWrongType(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("h", map[string][]byte{"f": []byte("v")})
	_, _, err := s.GetSet("h", []byte("x"), time.Time{})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.Append("h", []byte("z"))
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestDelAndExistsExpired(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("ex", []byte("1"), time.Now().Add(-time.Second))
	// Expired keys are logically gone; Del should not count them (Redis semantics).
	if s.Del([]string{"ex"}) != 0 {
		t.Fatal()
	}
	_ = s.Set("ex2", []byte("1"), time.Now().Add(-time.Second))
	if s.Exists([]string{"ex2"}) != 0 {
		t.Fatal()
	}
}

func TestEvictAllKeysLRUAvoidVictim(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "1024b"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("solo", []byte(strings.Repeat("x", 600)), time.Time{})
	if evictFromStore(s, "solo") {
		t.Fatal("only key is avoided")
	}
}

func TestEvictVolatileLRUAndRandomAvoid(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "2048b"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("v1", []byte(strings.Repeat("y", 400)), time.Now().Add(time.Hour))
	if evictFromStore(s, "v1") {
		t.Fatal("avoid lone volatile key")
	}
	c2 := secretCfg()
	c2.MaxMemory = "2048b"
	c2.MaxMemoryPolicy = "allkeys-random"
	s2, err := NewStore(c2)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	_ = s2.Set("r1", []byte(strings.Repeat("z", 400)), time.Time{})
	if evictFromStore(s2, "r1") {
		t.Fatal("avoid only random candidate")
	}
}

func TestEvictVolatileTTLAvoid(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "4096b"
	c.MaxMemoryPolicy = "volatile-ttl"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("t1", []byte(strings.Repeat("a", 300)), time.Now().Add(time.Hour))
	if evictFromStore(s, "t1") {
		t.Fatal("avoid only ttl key")
	}
}

func TestTouchAllKeysLRUMoveToFront(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("a", []byte("1"), time.Time{})
	_ = s.Set("b", []byte("2"), time.Time{})
	_, _ = s.Get("a")
	_, _ = s.Get("a")
}

func TestLruAfterSetVolatileClearsVolElem(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("sw", []byte("1"), time.Now().Add(time.Hour))
	_ = s.Set("sw", []byte("2"), time.Time{})
}

func TestListPushEmptyValsUsesListLen(t *testing.T) {
	s := newTestStore(t)
	n, err := s.LPush("e", [][]byte{})
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	_, _ = s.LPush("e2", [][]byte{[]byte("a"), []byte("b")})
	n, err = s.LPush("e2", [][]byte{})
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	_ = s.Set("notlist", []byte("x"), time.Time{})
	_, err = s.LPush("notlist", [][]byte{})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestLLenWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.LLen("s")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestHExistsSIsMemberSMembersWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.HExists("s", "f")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	ok, err := s.SIsMember("s", []byte("m"))
	if err == nil || ok {
		t.Fatal(err)
	}
	_, err = s.SMembers("s")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestRPopMultiCount(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.RPush("rp", [][]byte{[]byte("a"), []byte("b"), []byte("c")})
	out, err := s.RPop("rp", 2)
	if err != nil || len(out) != 2 {
		t.Fatal(len(out), err)
	}
}

func TestLRangeInvertedRange(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lr", [][]byte{[]byte("a"), []byte("b"), []byte("c")})
	out, err := s.LRange("lr", 2, 1)
	if err != nil || len(out) != 0 {
		t.Fatal(out, err)
	}
	out, err = s.LRange("lr", 10, 20)
	if err != nil || len(out) != 0 {
		t.Fatal(out, err)
	}
}

func TestMGetWrongTypeError(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("h", map[string][]byte{"f": []byte("v")})
	_, err := s.MGet([]string{"h"})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestSRemUntilEmpty(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.SAdd("sx", [][]byte{[]byte("only")})
	n, err := s.SRem("sx", [][]byte{[]byte("only")})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if s.Exists([]string{"sx"}) != 0 {
		t.Fatal()
	}
}

func TestVolatileLRUTouchMoveToFront(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("tv", []byte("1"), time.Now().Add(time.Hour))
	_, _ = s.Get("tv")
	_, _ = s.Get("tv")
}

func TestHSetNXWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("str", []byte("x"), time.Time{})
	_, err := s.HSetNX("str", "f", []byte("y"))
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestReplaceSetExpiredKey(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("ex", []byte("old"), time.Now().Add(-time.Hour))
	if err := s.ReplaceSet("ex", [][]byte{[]byte("m")}); err != nil {
		t.Fatal(err)
	}
	mem, err := s.SMembers("ex")
	if err != nil || len(mem) != 1 {
		t.Fatal(len(mem), err)
	}
}

func TestRenameDstExpiredBranch(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("src", []byte("v"), time.Time{})
	_ = s.Set("dst", []byte("old"), time.Now().Add(-time.Hour))
	if err := s.Rename("src", "dst"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get("dst")
	if string(v) != "v" {
		t.Fatal(string(v))
	}
}

func TestSetNXOOM(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("big", []byte(strings.Repeat("x", 400)), time.Time{})
	_, err = s.SetNX("n2", []byte(strings.Repeat("y", 400)), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "OOM") {
		t.Fatal(err)
	}
}

func TestApplySnapshotOOMPath(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "256b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("fill", []byte(strings.Repeat("x", 200)), time.Time{})
	err = s.ApplySnapshot([]SnapshotEntry{{Key: "big", Type: "string", Value: []byte(strings.Repeat("y", 200))}})
	if err == nil {
		t.Fatal("expected apply error")
	}
}

func TestFlushDBResetsWatchVersions(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("w", []byte("1"), time.Time{})
	if s.WatchVersion("w") == 0 {
		t.Fatal()
	}
	s.FlushDB()
	if s.WatchVersion("w") != 0 {
		t.Fatal(s.WatchVersion("w"))
	}
}

func TestMSetNXEvictUntilFitsLoop(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "1200b"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 30; i++ {
		_ = s.Set(fmt.Sprintf("fill-%d", i), []byte("zz"), time.Time{})
	}
	ok, err := s.MSetNX(map[string][]byte{
		"newa": []byte(strings.Repeat("p", 200)),
		"newb": []byte(strings.Repeat("q", 200)),
	})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestSetNXSetXXAfterExpirePurge(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("ex", []byte("1"), time.Now().Add(-time.Hour))
	ok, err := s.SetNX("ex", []byte("2"), time.Time{})
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	_ = s.Set("xx", []byte("a"), time.Now().Add(-time.Hour))
	ok2, err := s.SetXX("xx", []byte("b"), time.Time{})
	if err != nil || ok2 {
		t.Fatal(ok2, err)
	}
}

func TestDecrByOverflowLow(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("lo", []byte("-9223372036854775808"), time.Time{})
	_, err := s.DecrBy("lo", 1)
	if err == nil {
		t.Fatal("expected overflow")
	}
}

func TestLPopRPopExhaustList(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lp", [][]byte{[]byte("a"), []byte("b")})
	out, err := s.LPop("lp", 10)
	if err != nil || len(out) != 2 {
		t.Fatal(len(out), err)
	}
	_, _ = s.RPush("rp", [][]byte{[]byte("1"), []byte("2")})
	out, err = s.RPop("rp", 10)
	if err != nil || len(out) != 2 {
		t.Fatal(len(out), err)
	}
}

func TestHGetHGetAllExpired(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("hh", map[string][]byte{"f": []byte("v")})
	_, _ = s.ExpireAt("hh", time.Now().Add(-time.Second))
	v, err := s.HGet("hh", "f")
	if err != nil || v != nil {
		t.Fatal(v, err)
	}
	m, err := s.HGetAll("hh")
	if err != nil || m != nil {
		t.Fatal(m, err)
	}
}

func TestSAddWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.SAdd("s", [][]byte{[]byte("m")})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestHSetEmptyMap(t *testing.T) {
	s := newTestStore(t)
	n, err := s.HSet("h", map[string][]byte{})
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestRenameSameKeyNoOp(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("k", []byte("v"), time.Time{})
	if err := s.Rename("k", "k"); err != nil {
		t.Fatal(err)
	}
}

func TestHSetWrongTypeOverwrite(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("hk", []byte("s"), time.Time{})
	_, err := s.HSet("hk", map[string][]byte{"f": []byte("v")})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestHSetOnExpiredHash(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("hh", map[string][]byte{"a": []byte("1")})
	_, _ = s.ExpireAt("hh", time.Now().Add(-time.Second))
	n, err := s.HSet("hh", map[string][]byte{"b": []byte("2")})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestHDelHExistsHLenWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("x", []byte("1"), time.Time{})
	_, err := s.HDel("x", []string{"f"})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.HExists("x", "f")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.HLen("x")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestPersistOnHash(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("h", map[string][]byte{"f": []byte("v")})
	ok, err := s.Persist("h")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	_, _ = s.Expire("h", 60)
	ok, err = s.Persist("h")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestLPopRPopWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.LPop("s", 1)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.RPop("s", 1)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestLRangeWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.LRange("s", 0, 1)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestSRemSCardWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	_, err := s.SRem("s", [][]byte{[]byte("m")})
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.SCard("s")
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestLSetMissingKey(t *testing.T) {
	s := newTestStore(t)
	err := s.LSet("nope", 0, []byte("x"))
	if err == nil {
		t.Fatal()
	}
}

func TestEvictionLruBranchesSecondInsert(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("k", []byte("1"), time.Time{})
	_ = s.Set("k", []byte("2"), time.Time{})
}

func TestVolatileLRUTouchVolatileRemoveNonTTL(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "volatile-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("mix", []byte("a"), time.Now().Add(time.Hour))
	_, _ = s.Get("mix")
	_ = s.Set("mix", []byte("b"), time.Time{})
	_, _ = s.Get("mix")
}

func TestMSetOOMOnSecondKey(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "600b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.MSet(map[string][]byte{
		"a": []byte(strings.Repeat("x", 280)),
		"b": []byte(strings.Repeat("y", 280)),
	})
	if err == nil || !strings.Contains(err.Error(), "mset") {
		t.Fatal(err)
	}
}

func TestLPushAfterExpiredList(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.LPush("lst", [][]byte{[]byte("a")})
	_, _ = s.ExpireAt("lst", time.Now().Add(-time.Second))
	n, err := s.LPush("lst", [][]byte{[]byte("b")})
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

func TestHSetNXOOM(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("fill", []byte(strings.Repeat("x", 400)), time.Time{})
	_, err = s.HSetNX("big", "f", []byte(strings.Repeat("y", 400)))
	if err == nil || !strings.Contains(err.Error(), "OOM") {
		t.Fatal(err)
	}
}

func TestGetSetOOM(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("a", []byte(strings.Repeat("x", 400)), time.Time{})
	_, _, err = s.GetSet("b", []byte(strings.Repeat("y", 400)), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "OOM") {
		t.Fatal(err)
	}
}

func TestAppendOOM(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("a", []byte(strings.Repeat("x", 400)), time.Time{})
	_, err = s.Append("b", []byte(strings.Repeat("y", 400)))
	if err == nil || !strings.Contains(err.Error(), "OOM") {
		t.Fatal(err)
	}
}

func TestRenameNotFoundAndExpiredSrc(t *testing.T) {
	s := newTestStore(t)
	if err := s.Rename("missing", "dst"); err == nil {
		t.Fatal()
	}
	_ = s.Set("badsrc", []byte("x"), time.Now().Add(-time.Hour))
	if err := s.Rename("badsrc", "dst"); err == nil {
		t.Fatal()
	}
}

func TestKeysInvalidGlobPattern(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("k", []byte("v"), time.Time{})
	_ = s.Keys("[")
}

func TestRenameOverwritesLiveDestination(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("src", []byte("from-src"), time.Time{})
	_ = s.Set("dst", []byte("old-dst"), time.Time{})
	if err := s.Rename("src", "dst"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get("dst")
	if string(v) != "from-src" {
		t.Fatal(string(v))
	}
}

func TestHDelZeroFields(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.HSet("h", map[string][]byte{"f": []byte("v")})
	n, err := s.HDel("h", []string{})
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestSMembersWithLRUAllKeys(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "0"
	c.MaxMemoryPolicy = "allkeys-lru"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _ = s.SAdd("s", [][]byte{[]byte("a"), []byte("b")})
	_, _ = s.SMembers("s")
}

func TestReplaceSetEmptyAndSMembers(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplaceSet("es", [][]byte{}); err != nil {
		t.Fatal(err)
	}
	mem, err := s.SMembers("es")
	if err != nil || len(mem) != 0 {
		t.Fatal(len(mem), err)
	}
	n, err := s.SCard("es")
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
	ok, err := s.SIsMember("es", []byte("x"))
	if err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestPersistExpiredString(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("e", []byte("1"), time.Now().Add(-time.Second))
	ok, err := s.Persist("e")
	if err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestExpireAtAllCollectionTypes(t *testing.T) {
	s := newTestStore(t)
	at := time.Now().Add(time.Hour)
	_, _ = s.HSet("h", map[string][]byte{"f": []byte("1")})
	ok, err := s.ExpireAt("h", at)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, _ = s.LPush("l", [][]byte{[]byte("a")})
	ok, err = s.ExpireAt("l", at)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, _ = s.SAdd("st", [][]byte{[]byte("m")})
	ok, err = s.ExpireAt("st", at)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestTypeExpireAtOnExpiredKey(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("ex", []byte("1"), time.Now().Add(-time.Second))
	if s.Type("ex") != "none" {
		t.Fatal(s.Type("ex"))
	}
	ok, err := s.ExpireAt("ex", time.Now().Add(time.Hour))
	if err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestKeysDBSizeExpireCountSkipExpiredEntries(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("live", []byte("1"), time.Time{})
	_ = s.Set("dead", []byte("1"), time.Now().Add(-time.Hour))
	if len(s.Keys("*")) != 1 {
		t.Fatal(len(s.Keys("*")))
	}
	if s.DBSize() != 1 {
		t.Fatal(s.DBSize())
	}
	_ = s.Set("ttl", []byte("1"), time.Now().Add(time.Hour))
	if s.ExpireKeyCount() != 1 {
		t.Fatal(s.ExpireKeyCount())
	}
}

func TestListExtraWrongType(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("s", []byte("x"), time.Time{})
	if err := s.LTrim("s", 0, 1); err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err := s.LRem("s", 1, []byte("x"))
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.LIndex("s", 0)
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	err = s.LSet("s", 0, []byte("z"))
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
	_, err = s.LInsert("s", true, []byte("p"), []byte("i"))
	if err == nil || !errors.Is(err, ErrWrongType) {
		t.Fatal(err)
	}
}

func TestApplySnapshotEntryAfterFlush(t *testing.T) {
	s := newTestStore(t)
	_ = s.Set("gone", []byte("x"), time.Time{})
	s.FlushDB()
	if err := s.ApplySnapshotEntry(SnapshotEntry{Key: "solo", Type: "string", Value: []byte("y")}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get("solo")
	if string(v) != "y" {
		t.Fatal(string(v))
	}
}

func TestApplySnapshotLongList(t *testing.T) {
	s := newTestStore(t)
	els := make([][]byte, 50)
	for i := range els {
		els[i] = []byte(fmt.Sprintf("elem-%d", i))
	}
	err := s.ApplySnapshot([]SnapshotEntry{{Key: "longlist", Type: "list", Elements: els}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.LRange("longlist", 0, -1)
	if err != nil || len(out) != 50 {
		t.Fatal(len(out), err)
	}
}

func TestReplaceConfigInvalidMaxMemoryActsUnlimited(t *testing.T) {
	s := newTestStore(t)
	cfg := testCfg(t)
	cfg.MaxMemory = "64xb"
	s.ReplaceConfig(cfg)
	if err := s.Set("big", bytesRepeat(100000), time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceSetOOM(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "512b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Set("fill", []byte(strings.Repeat("x", 400)), time.Time{})
	err = s.ReplaceSet("rs", [][]byte{[]byte(strings.Repeat("m", 200)), []byte(strings.Repeat("n", 200))})
	if err == nil || !strings.Contains(err.Error(), "OOM") {
		t.Fatal(err)
	}
}

func TestApplySnapshotSecondEntryFails(t *testing.T) {
	c := secretCfg()
	c.MaxMemory = "400b"
	c.MaxMemoryPolicy = "noeviction"
	s, err := NewStore(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ApplySnapshot([]SnapshotEntry{
		{Key: "ok", Type: "string", Value: []byte("a")},
		{Key: "bad", Type: "unknown", Value: []byte("b")},
	})
	if err == nil {
		t.Fatal("expected error on second entry")
	}
}

