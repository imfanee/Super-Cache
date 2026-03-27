// Table-driven coverage for applyReplicatePayload (all switch branches).
package peer

import (
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/store"
)

func TestApplyReplicatePayloadAllOps(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("q", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Set("ex", []byte("1"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.Set("pk", []byte("v"), time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LPush("lk", [][]byte{[]byte("a"), []byte("b")}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.HSet("hk", map[string][]byte{"f": []byte("1")}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SAdd("sk", [][]byte{[]byte("m1")}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		p    ReplicatePayload
	}{
		{"set", ReplicatePayload{Op: "set", Key: "k1", Value: []byte("v")}},
		{"set_ttl", ReplicatePayload{Op: "SET", Key: "k2", Value: []byte("v"), TTLms: 3600000}},
		{"del", ReplicatePayload{Op: "DEL", Key: "k1"}},
		{"hset", ReplicatePayload{Op: "HSET", Key: "h2", Fields: map[string][]byte{"a": []byte("b")}}},
		{"hset_empty", ReplicatePayload{Op: "HSET", Key: "h2", Fields: map[string][]byte{}}},
		{"hdel", ReplicatePayload{Op: "HDEL", Key: "hk", Members: [][]byte{[]byte("f")}}},
		{"sadd", ReplicatePayload{Op: "SADD", Key: "s2", Members: [][]byte{[]byte("x")}}},
		{"sadd_empty", ReplicatePayload{Op: "SADD", Key: "s2", Members: nil}},
		{"srem", ReplicatePayload{Op: "SREM", Key: "sk", Members: [][]byte{[]byte("m1")}}},
		{"lpush", ReplicatePayload{Op: "LPUSH", Key: "lk", Members: [][]byte{[]byte("z")}}},
		{"lpush_empty", ReplicatePayload{Op: "LPUSH", Key: "lk", Members: nil}},
		{"rpush", ReplicatePayload{Op: "RPUSH", Key: "lk", Members: [][]byte{[]byte("y")}}},
		{"expire_skip", ReplicatePayload{Op: "EXPIRE", Key: "pk", Cnt: 0}},
		{"expire", ReplicatePayload{Op: "EXPIRE", Key: "pk", Cnt: 120}},
		{"pexpire_skip", ReplicatePayload{Op: "PEXPIRE", Key: "pk", Cnt: 0}},
		{"pexpire", ReplicatePayload{Op: "PEXPIRE", Key: "pk", Cnt: 5000}},
		{"expireat", ReplicatePayload{Op: "EXPIREAT", Key: "pk", Cnt: time.Now().Add(time.Hour).Unix()}},
		{"pexpireat", ReplicatePayload{Op: "PEXPIREAT", Key: "pk", TTLms: time.Now().Add(time.Hour).UnixMilli()}},
		{"persist", ReplicatePayload{Op: "PERSIST", Key: "ex"}},
		{"rename", ReplicatePayload{Op: "RENAME", Key: "pk", Dest: "pk2"}},
		{"lset", ReplicatePayload{Op: "LSET", Key: "lk", Cnt: 0, Value: []byte("c")}},
		{"lrem", ReplicatePayload{Op: "LREM", Key: "lk", Cnt: 0, Value: []byte("c")}},
		{"ltrim_short", ReplicatePayload{Op: "LTRIM", Key: "lk", Members: [][]byte{[]byte("0")}}},
		{"ltrim", ReplicatePayload{Op: "LTRIM", Key: "lk", Members: [][]byte{[]byte("0"), []byte("-1")}}},
		{"linsert_before_short", ReplicatePayload{Op: "LINSERT_BEFORE", Key: "lk", Members: [][]byte{[]byte("x")}}},
		{"linsert_after_short", ReplicatePayload{Op: "LINSERT_AFTER", Key: "lk", Members: [][]byte{[]byte("x")}}},
		{"lpop", ReplicatePayload{Op: "LPOP", Key: "lk", Cnt: 1}},
		{"lpop_default", ReplicatePayload{Op: "LPOP", Key: "lk", Cnt: 0}},
		{"rpop", ReplicatePayload{Op: "RPOP", Key: "lk", Cnt: 1}},
		{"flushall", ReplicatePayload{Op: "FLUSHALL"}},
		{"flushdb", ReplicatePayload{Op: "FLUSHDB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ApplyReplicatePayload(st, tc.p); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("ltrim_bad_start", func(t *testing.T) {
		cfg2 := &config.Config{SharedSecret: strings.Repeat("q", 32)}
		config.ApplyDefaults(cfg2)
		st2, err := store.NewStore(cfg2)
		if err != nil {
			t.Fatal(err)
		}
		defer st2.Close()
		if _, err := st2.LPush("lk", [][]byte{[]byte("a")}); err != nil {
			t.Fatal(err)
		}
		if err := ApplyReplicatePayload(st2, ReplicatePayload{Op: "LTRIM", Key: "lk", Members: [][]byte{[]byte("x"), []byte("1")}}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("ltrim_bad_stop", func(t *testing.T) {
		cfg2 := &config.Config{SharedSecret: strings.Repeat("q", 32)}
		config.ApplyDefaults(cfg2)
		st2, err := store.NewStore(cfg2)
		if err != nil {
			t.Fatal(err)
		}
		defer st2.Close()
		if _, err := st2.LPush("lk", [][]byte{[]byte("a")}); err != nil {
			t.Fatal(err)
		}
		if err := ApplyReplicatePayload(st2, ReplicatePayload{Op: "LTRIM", Key: "lk", Members: [][]byte{[]byte("0"), []byte("x")}}); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestApplyLinsertBeforeAfterValid(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.LPush("ik", [][]byte{[]byte("pivot"), []byte("tail")}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyReplicatePayload(st, ReplicatePayload{Op: "LINSERT_BEFORE", Key: "ik", Members: [][]byte{[]byte("pivot"), []byte("n1")}}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyReplicatePayload(st, ReplicatePayload{Op: "LINSERT_AFTER", Key: "ik", Members: [][]byte{[]byte("pivot"), []byte("n2")}}); err != nil {
		t.Fatal(err)
	}
}

func TestApplyWireReplDelegates(t *testing.T) {
	cfg := &config.Config{SharedSecret: strings.Repeat("r", 32)}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := wireRepl{Op: "SET", Key: "wk", Value: []byte("1")}
	if err := applyWireRepl(st, w); err != nil {
		t.Fatal(err)
	}
}
