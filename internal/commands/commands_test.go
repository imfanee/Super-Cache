// Tests for Redis command handlers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"bytes"
	"math"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/client"
	"github.com/supercache/supercache/internal/config"
	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

func TestRegistryLookup(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"GET", "SET", "HSET", "LPUSH", "SADD", "SCAN", "PING", "MULTI", "EXEC", "FLUSHDB", "FLUSHALL"} {
		if _, ok := r.Lookup(name); !ok {
			t.Fatalf("missing command %q", name)
		}
	}
}

func TestDispatchSetGet(t *testing.T) {
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	reg := NewRegistry()
	var buf bytes.Buffer
	w := resp.NewWriter(&buf)
	ctx := &CommandContext{
		Store:  st,
		Writer: w,
		Peer:   peer.NoopReplicator{},
		Config: cfg,
	}

	setVal := resp.Value{Type: '*', IsNull: false, Array: []resp.Value{
		{Type: '$', Str: "SET"},
		{Type: '$', Str: "k"},
		{Type: '$', Str: "v"},
	}}
	if err := reg.Dispatch(ctx, setVal); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("+OK\r\n")) {
		t.Fatalf("expected OK, got %q", buf.String())
	}

	buf.Reset()
	w = resp.NewWriter(&buf)
	ctx.Writer = w
	getVal := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"},
		{Type: '$', Str: "k"},
	}}
	if err := reg.Dispatch(ctx, getVal); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("$1\r\nv\r\n")) {
		t.Fatalf("expected bulk v, got %q", buf.String())
	}
}

type testKeyspace struct {
	hits, misses int64
}

func (t *testKeyspace) RecordHit(n int64)  { t.hits += n }
func (t *testKeyspace) RecordMiss(n int64) { t.misses += n }

func TestKeyspaceHitsMisses(t *testing.T) {
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	reg := NewRegistry()
	var buf bytes.Buffer
	w := resp.NewWriter(&buf)
	ks := &testKeyspace{}
	ctx := &CommandContext{
		Store:    st,
		Writer:   w,
		Peer:     peer.NoopReplicator{},
		Config:   cfg,
		Keyspace: ks,
	}

	miss := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"},
		{Type: '$', Str: "nope"},
	}}
	if err := reg.Dispatch(ctx, miss); err != nil {
		t.Fatal(err)
	}
	if ks.misses != 1 || ks.hits != 0 {
		t.Fatalf("after GET miss: hits=%d misses=%d", ks.hits, ks.misses)
	}

	buf.Reset()
	w = resp.NewWriter(&buf)
	ctx.Writer = w
	setVal := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "SET"},
		{Type: '$', Str: "k2"},
		{Type: '$', Str: "x"},
	}}
	if err := reg.Dispatch(ctx, setVal); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	w = resp.NewWriter(&buf)
	ctx.Writer = w
	hit := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"},
		{Type: '$', Str: "k2"},
	}}
	if err := reg.Dispatch(ctx, hit); err != nil {
		t.Fatal(err)
	}
	if ks.hits != 1 || ks.misses != 1 {
		t.Fatalf("after GET hit: hits=%d misses=%d", ks.hits, ks.misses)
	}
}

func newTestContext() (*CommandContext, *bytes.Buffer) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32), MaxMemory: "0", MaxMemoryPolicy: "noeviction"}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		panic(err)
	}
	buf := &bytes.Buffer{}
	w := resp.NewWriter(buf)
	sess := &client.Session{
		ID:            1,
		Authenticated: true,
		WatchedKeys:   make(map[string]uint64),
		SubChannels:   make(map[string]bool),
		PSubPatterns:  make(map[string]bool),
	}
	reg := NewRegistry()
	ctx := &CommandContext{
		Store:    st,
		Session:  sess,
		Writer:   w,
		Peer:     peer.NoopReplicator{},
		Config:   cfg,
		PubSub:   client.NewSubscriptionManager(),
		Registry: reg,
	}
	return ctx, buf
}

// newTestContextLowMem builds a context with a small maxmemory and noeviction (for OOM reply paths).
func newTestContextLowMem() (*CommandContext, *bytes.Buffer) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32), MaxMemory: "36kb", MaxMemoryPolicy: "noeviction"}
	config.ApplyDefaults(cfg)
	st, err := store.NewStore(cfg)
	if err != nil {
		panic(err)
	}
	buf := &bytes.Buffer{}
	w := resp.NewWriter(buf)
	sess := &client.Session{
		ID:            1,
		Authenticated: true,
		WatchedKeys:   make(map[string]uint64),
		SubChannels:   make(map[string]bool),
		PSubPatterns:  make(map[string]bool),
	}
	reg := NewRegistry()
	ctx := &CommandContext{
		Store:    st,
		Session:  sess,
		Writer:   w,
		Peer:     peer.NoopReplicator{},
		Config:   cfg,
		PubSub:   client.NewSubscriptionManager(),
		Registry: reg,
	}
	return ctx, buf
}

func runCommand(t *testing.T, ctx *CommandContext, buf *bytes.Buffer, args ...string) string {
	t.Helper()
	if len(args) == 0 {
		t.Fatal("args empty")
	}
	raw := make([][]byte, len(args))
	for i := range args {
		raw[i] = []byte(args[i])
	}
	ctx.Args = raw
	meta, ok := ctx.Registry.Lookup(args[0])
	if !ok {
		cmds := ctx.Registry.Commands()
		names := make([]string, 0, len(cmds))
		for _, c := range cmds {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		t.Fatalf("command %q not found; registered: %v", args[0], names)
	}
	if err := meta.Handler(ctx); err != nil {
		t.Fatalf("%s handler error: %v", args[0], err)
	}
	if err := ctx.Writer.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	out := buf.String()
	buf.Reset()
	return out
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("got %q does not contain %q", got, want)
	}
}

func assertSimpleString(t *testing.T, got, want string) {
	t.Helper()
	if !strings.HasPrefix(got, "+") || !strings.HasSuffix(got, "\r\n") || !strings.Contains(got, want) {
		t.Fatalf("expected simple string containing %q, got %q", want, got)
	}
}

func assertError(t *testing.T, got, want string) {
	t.Helper()
	if !strings.HasPrefix(got, "-") || !strings.Contains(got, want) {
		t.Fatalf("expected error containing %q, got %q", want, got)
	}
}

func assertInteger(t *testing.T, got string, want int64) {
	t.Helper()
	v := mustParseOne(t, got)
	if v.Type != ':' || v.Integer != want {
		t.Fatalf("expected integer %d got %+v (%q)", want, v, got)
	}
}

func assertBulkString(t *testing.T, got, want string) {
	t.Helper()
	v := mustParseOne(t, got)
	if v.Type != '$' || v.IsNull || v.Str != want {
		t.Fatalf("expected bulk %q got %+v (%q)", want, v, got)
	}
}

func assertNullBulkString(t *testing.T, got string) {
	t.Helper()
	if got != "$-1\r\n" {
		t.Fatalf("expected null bulk got %q", got)
	}
}

func assertArrayLength(t *testing.T, got string, want int) {
	t.Helper()
	v := mustParseOne(t, got)
	if v.Type != '*' || len(v.Array) != want {
		t.Fatalf("expected array len %d got %+v (%q)", want, v, got)
	}
}

func mustParseOne(t *testing.T, wire string) resp.Value {
	t.Helper()
	p := resp.NewParser(strings.NewReader(wire))
	v, err := p.Parse()
	if err != nil {
		t.Fatalf("parse resp %q: %v", wire, err)
	}
	return v
}

type testInfoProvider struct{}

func (testInfoProvider) TCPPort() int              { return 6379 }
func (testInfoProvider) UptimeSeconds() int64      { return 120 }
func (testInfoProvider) ConnectedClients() int64   { return 3 }
func (testInfoProvider) TotalConnections() int64   { return 9 }
func (testInfoProvider) TotalCommands() int64      { return 17 }
func (testInfoProvider) OpsPerSec() int64          { return 4 }
func (testInfoProvider) KeyspaceHits() int64       { return 7 }
func (testInfoProvider) KeyspaceMisses() int64     { return 8 }
func (testInfoProvider) ConnectedPeers() int       { return 1 }
func (testInfoProvider) PeerAddresses() []string   { return []string{"127.0.0.1:7379"} }
func (testInfoProvider) NodeID() string            { return "node-id" }
func (testInfoProvider) BootstrapState() string    { return "idle" }
func (testInfoProvider) ServerVersion() string     { return "dev" }

func TestCommandHarnessStringAndGenericFlow(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = testInfoProvider{}

	assertNullBulkString(t, runCommand(t, ctx, buf, "GET", "missing"))
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "k", "v"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "GET", "k"), "v")
	assertInteger(t, runCommand(t, ctx, buf, "EXISTS", "k", "k"), 2)
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "k"), "string")
	assertInteger(t, runCommand(t, ctx, buf, "DEL", "k"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "k"), "none")

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "ttl", "v", "EX", "10"), "OK")
	ttl := mustParseOne(t, runCommand(t, ctx, buf, "TTL", "ttl"))
	if ttl.Integer <= 0 {
		t.Fatalf("ttl <= 0: %+v", ttl)
	}
	assertInteger(t, runCommand(t, ctx, buf, "PERSIST", "ttl"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "TTL", "ttl"), -1)
	assertInteger(t, runCommand(t, ctx, buf, "TTL", "nosuch"), -2)

	assertArrayLength(t, runCommand(t, ctx, buf, "MGET", "a", "b"), 2)
	assertSimpleString(t, runCommand(t, ctx, buf, "MSET", "a", "1", "b", "2", "c", "3"), "OK")
	v := mustParseOne(t, runCommand(t, ctx, buf, "MGET", "a", "b", "z"))
	if v.Type != '*' || len(v.Array) != 3 || v.Array[0].Str != "1" || v.Array[1].Str != "2" || !v.Array[2].IsNull {
		t.Fatalf("mget got %+v", v)
	}
	assertInteger(t, runCommand(t, ctx, buf, "MSETNX", "x1", "v1", "x2", "v2"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "MSETNX", "x1", "v9", "x3", "v3"), 0)

	assertInteger(t, runCommand(t, ctx, buf, "INCR", "n1"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "n2", "41"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "INCR", "n2"), 42)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "nonint", "abc"), "OK")
	assertError(t, runCommand(t, ctx, buf, "INCR", "nonint"), "not an integer")
	assertInteger(t, runCommand(t, ctx, buf, "INCRBY", "n3", "10"), 10)
	assertInteger(t, runCommand(t, ctx, buf, "DECRBY", "n3", "3"), 7)
	assertBulkString(t, runCommand(t, ctx, buf, "INCRBYFLOAT", "f1", "10.5"), "10.5")
	assertBulkString(t, runCommand(t, ctx, buf, "INCRBYFLOAT", "f1", "0.1"), "10.6")

	assertInteger(t, runCommand(t, ctx, buf, "APPEND", "ap", "hello"), 5)
	assertInteger(t, runCommand(t, ctx, buf, "APPEND", "ap", "x"), 6)
	assertInteger(t, runCommand(t, ctx, buf, "STRLEN", "ap"), 6)
	assertInteger(t, runCommand(t, ctx, buf, "STRLEN", "none"), 0)
	assertBulkString(t, runCommand(t, ctx, buf, "GETRANGE", "ap", "1", "3"), "ell")
	assertInteger(t, runCommand(t, ctx, buf, "SETRANGE", "ap", "1", "xx"), 6)

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "nxk", "v", "NX"), "OK")
	assertNullBulkString(t, runCommand(t, ctx, buf, "SET", "nxk", "v2", "NX"))
	assertNullBulkString(t, runCommand(t, ctx, buf, "SET", "xxk", "v", "XX"))
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "xxk", "v", "NX"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "xxk", "v2", "XX"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "SET", "xxk", "v3", "GET"), "v2")

	assertSimpleString(t, runCommand(t, ctx, buf, "SETEX", "sx", "10", "v"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "PSETEX", "psx", "1000", "v"), "OK")
	pttl := mustParseOne(t, runCommand(t, ctx, buf, "PTTL", "psx"))
	if pttl.Type != ':' || pttl.Integer < 0 {
		t.Fatalf("pttl %v", pttl)
	}

	assertSimpleString(t, runCommand(t, ctx, buf, "PING"), "PONG")
	assertBulkString(t, runCommand(t, ctx, buf, "PING", "hello"), "hello")
	assertBulkString(t, runCommand(t, ctx, buf, "ECHO", "abc"), "abc")
	assertSimpleString(t, runCommand(t, ctx, buf, "SELECT", "0"), "OK")
	assertError(t, runCommand(t, ctx, buf, "SELECT", "1"), "DB index is out of range")
	assertInteger(t, runCommand(t, ctx, buf, "DBSIZE"), int64(ctx.Store.DBSize()))
	infoAll := runCommand(t, ctx, buf, "INFO")
	mustContain(t, infoAll, "# server")
	mustContain(t, infoAll, "# clients")
	mustContain(t, infoAll, "# memory")
	mustContain(t, infoAll, "# stats")
	mustContain(t, infoAll, "# replication")
	mustContain(t, infoAll, "# keyspace")
	mustContain(t, runCommand(t, ctx, buf, "INFO", "server"), "redis_version:7")
	mustContain(t, runCommand(t, ctx, buf, "INFO", "replication"), "connected_peers")
}

func TestHashListSetAndGenericCommands(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "h", "f1", "v1", "f2", "v2"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "h", "f2", "v9", "f3", "v3"), 1)
	assertBulkString(t, runCommand(t, ctx, buf, "HGET", "h", "f1"), "v1")
	assertNullBulkString(t, runCommand(t, ctx, buf, "HGET", "h", "none"))
	assertSimpleString(t, runCommand(t, ctx, buf, "HMSET", "h", "a", "1", "b", "2"), "OK")
	hmget := mustParseOne(t, runCommand(t, ctx, buf, "HMGET", "h", "a", "b", "x"))
	if hmget.Type != '*' || len(hmget.Array) != 3 || hmget.Array[0].Str != "1" || hmget.Array[1].Str != "2" || !hmget.Array[2].IsNull {
		t.Fatalf("hmget %+v", hmget)
	}
	assertArrayLength(t, runCommand(t, ctx, buf, "HGETALL", "h"), 10)
	assertInteger(t, runCommand(t, ctx, buf, "HDEL", "h", "f1"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "HEXISTS", "h", "f1"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "HEXISTS", "h", "f2"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "HLEN", "h"), 4)
	assertArrayLength(t, runCommand(t, ctx, buf, "HKEYS", "h"), 4)
	assertArrayLength(t, runCommand(t, ctx, buf, "HVALS", "h"), 4)
	assertInteger(t, runCommand(t, ctx, buf, "HINCRBY", "h2", "count", "2"), 2)
	assertBulkString(t, runCommand(t, ctx, buf, "HINCRBYFLOAT", "h2", "f", "1.5"), "1.5")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "skey", "v"), "OK")
	assertError(t, runCommand(t, ctx, buf, "HGET", "skey", "count"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "l", "a", "b", "c"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "RPUSH", "l2", "a", "b", "c"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "LPUSHX", "missing", "x"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "RPUSHX", "missing", "x"), 0)
	assertBulkString(t, runCommand(t, ctx, buf, "LPOP", "l"), "c")
	assertBulkString(t, runCommand(t, ctx, buf, "RPOP", "l2"), "c")
	assertArrayLength(t, runCommand(t, ctx, buf, "LPOP", "l", "2"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "RPUSH", "lr", "a", "b", "c", "d"), 4)
	assertArrayLength(t, runCommand(t, ctx, buf, "RPOP", "lr", "2"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "LLEN", "lr"), 2)
	assertArrayLength(t, runCommand(t, ctx, buf, "LRANGE", "lr", "0", "-1"), 2)
	assertBulkString(t, runCommand(t, ctx, buf, "LINDEX", "lr", "0"), "a")
	assertNullBulkString(t, runCommand(t, ctx, buf, "LINDEX", "lr", "99"))
	assertSimpleString(t, runCommand(t, ctx, buf, "LSET", "lr", "0", "z"), "OK")
	assertError(t, runCommand(t, ctx, buf, "LSET", "lr", "99", "z"), "out of range")
	assertInteger(t, runCommand(t, ctx, buf, "LREM", "lr", "2", "b"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "LTRIM", "lr", "0", "0"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "RPUSH", "lr2", "a", "b", "c"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "LINSERT", "lr2", "BEFORE", "b", "x"), 4)
	assertInteger(t, runCommand(t, ctx, buf, "LINSERT", "lr2", "AFTER", "b", "y"), 5)
	assertInteger(t, runCommand(t, ctx, buf, "LINSERT", "lr2", "AFTER", "nope", "y"), -1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "str", "v"), "OK")
	assertError(t, runCommand(t, ctx, buf, "LPUSH", "str", "x"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "SADD", "s", "x", "y", "z"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "s", "x"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "SREM", "s", "x"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SREM", "s", "x"), 0)
	assertArrayLength(t, runCommand(t, ctx, buf, "SMEMBERS", "s"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "SISMEMBER", "s", "y"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SISMEMBER", "s", "x"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "SCARD", "s"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "SCARD", "noset"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "s2", "y", "q"), 2)
	assertArrayLength(t, runCommand(t, ctx, buf, "SUNION", "s", "s2"), 3)
	assertArrayLength(t, runCommand(t, ctx, buf, "SINTER", "s", "s2"), 1)
	assertArrayLength(t, runCommand(t, ctx, buf, "SDIFF", "s", "s2"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SUNIONSTORE", "dstu", "s", "s2"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "SINTERSTORE", "dsti", "s", "s2"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SDIFFSTORE", "dstd", "s", "s2"), 1)
	assertError(t, runCommand(t, ctx, buf, "SADD", "lr2", "x"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "DEL", "nosuch"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "DEL", "h", "s", "none"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "EXISTS", "h", "h"), 0)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "r1", "v1"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "RENAME", "r1", "r2"), "OK")
	assertError(t, runCommand(t, ctx, buf, "RENAME", "no-src", "x"), "no such key")
	assertInteger(t, runCommand(t, ctx, buf, "RENAMENX", "r2", "r3"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "r4", "v4"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "RENAMENX", "r3", "r4"), 0)
	assertArrayLength(t, runCommand(t, ctx, buf, "KEYS", "*"), ctx.Store.DBSize())
	assertArrayLength(t, runCommand(t, ctx, buf, "SCAN", "0", "COUNT", "100"), 2)
}

func TestServerClientAndTransactions(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = testInfoProvider{}

	assertError(t, runCommand(t, ctx, buf, "AUTH", "anything"), "no password is set")
	ctx.Config.AuthPassword = "secret"
	assertError(t, runCommand(t, ctx, buf, "AUTH", "bad"), "WRONGPASS")
	assertSimpleString(t, runCommand(t, ctx, buf, "AUTH", "secret"), "OK")

	assertSimpleString(t, runCommand(t, ctx, buf, "CLIENT", "SETNAME", "myconn"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "CLIENT", "GETNAME"), "myconn")
	clientID := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "ID"))
	if clientID.Type != ':' || clientID.Integer <= 0 {
		t.Fatalf("expected positive client id, got %+v", clientID)
	}
	clientList := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "LIST"))
	if clientList.Type != '$' || clientList.IsNull || !strings.Contains(clientList.Str, "addr=") {
		t.Fatalf("expected client list with addr, got %+v", clientList)
	}

	assertArrayLength(t, runCommand(t, ctx, buf, "COMMAND"), len(ctx.Registry.Commands()))
	cnt := mustParseOne(t, runCommand(t, ctx, buf, "COMMAND", "COUNT"))
	if cnt.Type != ':' || cnt.Integer < 70 {
		t.Fatalf("command count %+v", cnt)
	}
	ci := mustParseOne(t, runCommand(t, ctx, buf, "COMMAND", "INFO", "ping"))
	if ci.Type != '*' || len(ci.Array) != 1 || len(ci.Array[0].Array) != 6 || !strings.EqualFold(ci.Array[0].Array[0].Str, "ping") {
		t.Fatalf("command info %+v", ci)
	}

	assertSimpleString(t, runCommand(t, ctx, buf, "MULTI"), "OK")
	ctx.Session.MultiQueue = append(ctx.Session.MultiQueue,
		resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "SET"}, {Type: '$', Str: "tx1"}, {Type: '$', Str: "v1"}}},
		resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "SET"}, {Type: '$', Str: "tx2"}, {Type: '$', Str: "v2"}}},
	)
	execOut := mustParseOne(t, runCommand(t, ctx, buf, "EXEC"))
	if execOut.Type != '*' || len(execOut.Array) != 2 {
		t.Fatalf("exec %+v", execOut)
	}
	assertError(t, runCommand(t, ctx, buf, "EXEC"), "EXEC without MULTI")

	assertSimpleString(t, runCommand(t, ctx, buf, "MULTI"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "DISCARD"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "after", "1"), "OK")
	assertError(t, runCommand(t, ctx, buf, "DISCARD"), "DISCARD without MULTI")

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "wk", "1"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "WATCH", "wk"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "MULTI"), "OK")
	ctx.Session.MultiQueue = append(ctx.Session.MultiQueue,
		resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "SET"}, {Type: '$', Str: "wk2"}, {Type: '$', Str: "x"}}},
	)

	other, obuf := newTestContext()
	defer other.Store.Close()
	other.Store = ctx.Store
	other.Writer = resp.NewWriter(obuf)
	other.Registry = ctx.Registry
	_ = runCommand(t, other, obuf, "SET", "wk", "2")

	aborted := runCommand(t, ctx, buf, "EXEC")
	if aborted != "*-1\r\n" {
		t.Fatalf("expected null array exec abort got %q", aborted)
	}
}

func TestClientIDListAndSetGetName(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	idOut := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "ID"))
	if idOut.Type != ':' || idOut.Integer <= 0 {
		t.Fatalf("CLIENT ID expected positive integer, got %+v", idOut)
	}

	listOut := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "LIST"))
	if listOut.Type != '$' || listOut.IsNull || !strings.Contains(listOut.Str, "addr=") {
		t.Fatalf("CLIENT LIST expected bulk with addr=, got %+v", listOut)
	}

	assertSimpleString(t, runCommand(t, ctx, buf, "CLIENT", "SETNAME", "myconn"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "CLIENT", "GETNAME"), "myconn")
}

func TestClientIDFallbackAndListValidation(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	ctx.Session.ID = 0
	idOut := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "ID"))
	if idOut.Type != ':' || idOut.Integer != 1 {
		t.Fatalf("CLIENT ID fallback expected 1, got %+v", idOut)
	}

	listOut := mustParseOne(t, runCommand(t, ctx, buf, "CLIENT", "LIST"))
	if listOut.Type != '$' || listOut.IsNull || !strings.Contains(listOut.Str, "cmd=client") {
		t.Fatalf("CLIENT LIST expected cmd=client marker, got %+v", listOut)
	}

	assertError(t, runCommand(t, ctx, buf, "CLIENT", "ID", "x"), "wrong number")
	assertError(t, runCommand(t, ctx, buf, "CLIENT", "LIST", "x"), "wrong number")
}

func TestCommandErrorCasesRequested(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hh", "f", "v"), 1)
	assertError(t, runCommand(t, ctx, buf, "GET", "hh"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "INCR", "hh"), "WRONGTYPE")
	assertInteger(t, runCommand(t, ctx, buf, "RPUSH", "ll", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "SADD", "ll", "m"), "WRONGTYPE")
}

type captureReplicator struct {
	ps []peer.ReplicatePayload
}

func (c *captureReplicator) Replicate(p peer.ReplicatePayload) error {
	c.ps = append(c.ps, p)
	return nil
}

func TestAdditionalCommandAndHelperCoverage(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	assertInteger(t, runCommand(t, ctx, buf, "SETNX", "knx", "v"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "kgs", "v1"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "GETSET", "kgs", "v2"), "v1")
	assertBulkString(t, runCommand(t, ctx, buf, "GETDEL", "kgs"), "v2")
	assertNullBulkString(t, runCommand(t, ctx, buf, "GETDEL", "kgs"))
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "gex", "x"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "gex", "EX", "5"), "x")
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "gex", "PERSIST"), "x")

	assertInteger(t, runCommand(t, ctx, buf, "DECR", "d1"), -1)
	assertInteger(t, runCommand(t, ctx, buf, "DECRBY", "d1", "2"), -3)

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "exp", "v"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "EXPIRE", "exp", "10"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "PEXPIRE", "exp", "1000"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "EXPIREAT", "exp", strconv.FormatInt(time.Now().Add(10*time.Second).Unix(), 10)), 1)
	assertInteger(t, runCommand(t, ctx, buf, "PEXPIREAT", "exp", strconv.FormatInt(time.Now().Add(10*time.Second).UnixMilli(), 10)), 1)
	_ = runCommand(t, ctx, buf, "RANDOMKEY")

	assertInteger(t, runCommand(t, ctx, buf, "HSETNX", "hhx", "f", "1"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "HSETNX", "hhx", "f", "2"), 0)

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "w", "1"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "WATCH", "w"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "UNWATCH"), "OK")

	assertSimpleString(t, runCommand(t, ctx, buf, "FLUSHDB"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "FLUSHALL"), "OK")
	tv := mustParseOne(t, runCommand(t, ctx, buf, "TIME"))
	if tv.Type != '*' || len(tv.Array) != 2 {
		t.Fatalf("TIME %+v", tv)
	}

	assertError(t, runCommand(t, ctx, buf, "SUBSCRIBE"), "wrong number")
	assertArrayLength(t, runCommand(t, ctx, buf, "SUBSCRIBE", "ch1"), 3)
	assertArrayLength(t, runCommand(t, ctx, buf, "PSUBSCRIBE", "ch*"), 3)
	assertInteger(t, runCommand(t, ctx, buf, "PUBLISH", "ch1", "m1"), 1)
	assertArrayLength(t, runCommand(t, ctx, buf, "UNSUBSCRIBE", "ch1"), 3)
	assertArrayLength(t, runCommand(t, ctx, buf, "PUNSUBSCRIBE", "ch*"), 3)

	rep := &captureReplicator{}
	ReplicateListPush(rep, "l", true, [][]byte{[]byte("a"), []byte("b")})
	ReplicateHDel(rep, "h", []string{"f1"})
	ReplicateSAdd(rep, "s", [][]byte{[]byte("x")})
	ReplicateSRem(rep, "s", [][]byte{[]byte("x")})
	ReplicateSet(rep, "k", []byte("v"), time.Now().Add(time.Second))
	ReplicateDel(rep, "k")
	ReplicateHash(rep, "h", map[string][]byte{"f": []byte("v")})
	ReplicateExpire(rep, "k", 1)
	ReplicateExpireAtUnix(rep, "k", time.Now().Add(time.Second).Unix())
	ReplicatePExpire(rep, "k", 1000)
	ReplicatePExpireAtUnixMs(rep, "k", time.Now().Add(time.Second).UnixMilli())
	ReplicatePersist(rep, "k")
	ReplicateRename(rep, "k1", "k2")
	ReplicateLPop(rep, "k", 1)
	ReplicateRPop(rep, "k", 1)
	ReplicateLRem(rep, "k", 1, []byte("v"))
	ReplicateLSet(rep, "k", 0, []byte("v"))
	ReplicateLTrim(rep, "k", 0, 1)
	ReplicateLInsert(rep, "k", true, []byte("p"), []byte("v"))
	if len(rep.ps) == 0 {
		t.Fatal("expected replication payloads")
	}

	var out bytes.Buffer
	w := resp.NewWriter(&out)
	if err := ReplyOK(w); err != nil {
		t.Fatal(err)
	}
	if err := WriteNoAuth(w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "+OK\r\n") || !strings.Contains(out.String(), "-NOAUTH Authentication required\r\n") {
		t.Fatalf("helper output %q", out.String())
	}
	if _, err := ParseInt([]byte("10")); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseUint([]byte("10")); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFloat([]byte("1.25")); err != nil {
		t.Fatal(err)
	}
	if got := argErr("SET"); !strings.Contains(got, "set") {
		t.Fatalf("argErr %q", got)
	}
	if got := invalidExpireErr("SET"); !strings.Contains(got, "set") {
		t.Fatalf("invalidExpireErr %q", got)
	}
	if got := cmdName([][]byte{[]byte("SET")}); got != "SET" {
		t.Fatalf("cmdName %q", got)
	}
}

func TestMoreBranchesAndArityErrors(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	// Ping subscribe mode branches.
	ctx.Session.SubChannels["ch"] = true
	ps := mustParseOne(t, runCommand(t, ctx, buf, "PING"))
	if ps.Type != '*' || len(ps.Array) != 2 || ps.Array[0].Str != "pong" {
		t.Fatalf("ping subscribe %+v", ps)
	}
	ps2 := mustParseOne(t, runCommand(t, ctx, buf, "PING", "hello"))
	if ps2.Type != '*' || len(ps2.Array) != 2 || ps2.Array[1].Str != "hello" {
		t.Fatalf("ping subscribe msg %+v", ps2)
	}
	assertError(t, runCommand(t, ctx, buf, "PING", "a", "b"), "wrong number")
	delete(ctx.Session.SubChannels, "ch")

	// writeArrayEmpty via missing hash.
	assertArrayLength(t, runCommand(t, ctx, buf, "HGETALL", "nosuch"), 0)

	// Arity and syntax error paths on broad command set.
	errorCases := [][]string{
		{"GET"},
		{"SET"},
		{"SETEX", "k", "1"},
		{"PSETEX", "k", "1"},
		{"GETSET", "k"},
		{"GETDEL"},
		{"GETEX"},
		{"MSET", "k"},
		{"MSETNX", "k"},
		{"INCRBY", "k"},
		{"DECRBY", "k"},
		{"INCRBYFLOAT", "k"},
		{"APPEND", "k"},
		{"GETRANGE", "k", "1"},
		{"SETRANGE", "k", "1"},
		{"HSET", "h", "f"},
		{"HMSET", "h", "f"},
		{"HGET"},
		{"HMGET", "h"},
		{"HDEL", "h"},
		{"HEXISTS", "h"},
		{"HINCRBY", "h"},
		{"HINCRBYFLOAT", "h"},
		{"LPUSH", "l"},
		{"RPUSH", "l"},
		{"LPOP", "l", "bad"},
		{"RPOP", "l", "bad"},
		{"LSET", "l"},
		{"LREM", "l"},
		{"LRANGE", "l"},
		{"LTRIM", "l"},
		{"LINSERT", "l"},
		{"SADD", "s"},
		{"SREM", "s"},
		{"SISMEMBER", "s"},
		{"SUNION"},
		{"SINTER"},
		{"SDIFF"},
		{"SUNIONSTORE", "d"},
		{"SINTERSTORE", "d"},
		{"SDIFFSTORE", "d"},
		{"EXISTS"},
		{"TYPE"},
		{"TTL"},
		{"PTTL"},
		{"EXPIRE", "k"},
		{"EXPIREAT", "k"},
		{"PEXPIRE", "k"},
		{"PEXPIREAT", "k"},
		{"PERSIST"},
		{"KEYS"},
		{"SCAN"},
		{"RENAME", "k"},
		{"RENAMENX", "k"},
		{"SELECT"},
		{"CLIENT"},
		{"COMMAND", "COUNT", "x"},
		{"TIME", "x"},
		{"SUBSCRIBE"},
		{"PSUBSCRIBE"},
		{"PUBLISH", "ch"},
	}
	for _, tc := range errorCases {
		out := runCommand(t, ctx, buf, tc...)
		if !strings.HasPrefix(out, "-") {
			t.Fatalf("%v expected error reply got %q", tc, out)
		}
	}
}

func TestCheckArity(t *testing.T) {
	fixed := CommandMeta{Name: "GET", Arity: 2}
	if err := CheckArity(fixed, [][]byte{[]byte("GET")}); err == nil {
		t.Fatal("expected arity error for GET with 1 arg")
	}
	if err := CheckArity(fixed, [][]byte{[]byte("GET"), []byte("k")}); err != nil {
		t.Fatalf("GET k: %v", err)
	}
	// Variable arity: DEL needs at least 2 tokens (name + >=1 key).
	vari := CommandMeta{Name: "DEL", Arity: -2}
	if err := CheckArity(vari, [][]byte{[]byte("DEL")}); err == nil {
		t.Fatal("expected arity error for DEL with only command name")
	}
	if err := CheckArity(vari, [][]byte{[]byte("DEL"), []byte("a")}); err != nil {
		t.Fatalf("DEL a: %v", err)
	}
}

func TestValueToArgsNestedTypes(t *testing.T) {
	_, err := ValueToArgs(resp.Value{Type: '$', Str: "x"})
	if err == nil {
		t.Fatal("expected error for non-array")
	}
	got, err := ValueToArgs(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"},
		{Type: ':', Integer: 7},
		{Type: '+', Str: "simple"},
		{Type: '-', Str: "err"},
		{Type: '$', IsNull: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || string(got[1]) != "7" || string(got[2]) != "simple" || string(got[3]) != "err" || got[4] != nil {
		t.Fatalf("unexpected args: %#v", got)
	}
	_, err = ValueToArgs(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '*', Array: []resp.Value{}},
	}})
	if err == nil {
		t.Fatal("expected unsupported nested type error")
	}
}

func TestCoverageLowBranchesMisc(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = testInfoProvider{}

	// cmdEcho arity error (non-2).
	assertError(t, runCommand(t, ctx, buf, "ECHO"), "wrong number")

	// CLIENT GETNAME when unset → null bulk.
	assertNullBulkString(t, runCommand(t, ctx, buf, "CLIENT", "GETNAME"))

	// writeArrayEmpty via HKEYS/HVALS on missing hash (nil map), in addition to HGETALL.
	assertArrayLength(t, runCommand(t, ctx, buf, "HKEYS", "emptyhash_nope"), 0)
	assertArrayLength(t, runCommand(t, ctx, buf, "HVALS", "emptyhash_nope"), 0)

	// GETEX: two-arg read-only; PX / EXAT / PXAT; PERSIST miss; invalid option / expire.
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "getex_plain", "plain"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "getex_plain"), "plain")
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "PX", "60000"), "plain")
	exAt := time.Now().Add(time.Hour).Unix()
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "EXAT", strconv.FormatInt(exAt, 10)), "plain")
	pxAt := time.Now().Add(time.Hour).UnixMilli()
	assertBulkString(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "PXAT", strconv.FormatInt(pxAt, 10)), "plain")
	assertNullBulkString(t, runCommand(t, ctx, buf, "GETEX", "nosuch_getex", "PERSIST"))
	assertError(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "EX"), "getex")
	assertError(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "EX", "0"), "invalid expire")
	assertError(t, runCommand(t, ctx, buf, "GETEX", "getex_plain", "GARBAGE"), "getex")

	// SET: EXAT/PXAT, KEEPTTL, invalid option, NX+XX conflict.
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "set_at", "v", "EXAT", strconv.FormatInt(exAt, 10)), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "set_pxat", "v", "PXAT", strconv.FormatInt(pxAt, 10)), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "set_kt", "a", "EX", "3600"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "set_kt", "b", "KEEPTTL"), "OK")
	assertError(t, runCommand(t, ctx, buf, "SET", "x", "y", "NOT_AN_OPT"), "set")
	assertError(t, runCommand(t, ctx, buf, "SET", "x", "y", "NX", "XX"), "syntax")

	// INCR/DECR overflow and INCRBY/DECRBY non-integer delta / overflow paths.
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "ov", strconv.FormatInt(math.MaxInt64, 10)), "OK")
	assertError(t, runCommand(t, ctx, buf, "INCR", "ov"), "overflow")
	assertError(t, runCommand(t, ctx, buf, "INCRBY", "n", "notint"), "not an integer")
	assertError(t, runCommand(t, ctx, buf, "DECRBY", "n", "x"), "not an integer")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "ov2", strconv.FormatInt(math.MinInt64, 10)), "OK")
	assertError(t, runCommand(t, ctx, buf, "DECR", "ov2"), "overflow")

	// SCAN: MATCH, COUNT, TYPE options (and default scan still works).
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "scan_k", "1"), "OK")
	scanOut := mustParseOne(t, runCommand(t, ctx, buf, "SCAN", "0", "MATCH", "scan_*", "COUNT", "50", "TYPE", "string"))
	if scanOut.Type != '*' || len(scanOut.Array) != 2 {
		t.Fatalf("scan %+v", scanOut)
	}
	assertError(t, runCommand(t, ctx, buf, "SCAN", "0", "MATCH"), "wrong number")
	assertError(t, runCommand(t, ctx, buf, "SCAN", "0", "COUNT", "0"), "not an integer")
	assertError(t, runCommand(t, ctx, buf, "SCAN", "0", "NOSUCH"), "scan")

	// Sets/lists: SCARD / SMEMBERS / LLEN wrong-type branches.
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "wrong_scard", "s"), "OK")
	assertError(t, runCommand(t, ctx, buf, "SCARD", "wrong_scard"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SMEMBERS", "wrong_scard"), "WRONGTYPE")
	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "llen_ok", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "LLEN", "wrong_scard"), "WRONGTYPE")

	// humanBytes tiers via INFO memory (maxmemory + used memory growth).
	ctx.Config.MaxMemory = "100mb"
	infoMem := runCommand(t, ctx, buf, "INFO", "memory")
	mustContain(t, infoMem, "maxmemory_human:")
	if !strings.Contains(infoMem, ".") || (!strings.Contains(infoMem, "M") && !strings.Contains(infoMem, "K")) {
		t.Fatalf("expected maxmemory_human scale (K/M), got: %q", infoMem)
	}
	bigVal := bytes.Repeat([]byte("x"), 2048)
	for i := 0; i < 600; i++ {
		_ = runCommand(t, ctx, buf, "SET", "bulkmem_"+strconv.Itoa(i), string(bigVal))
	}
	infoMem2 := runCommand(t, ctx, buf, "INFO", "memory")
	mustContain(t, infoMem2, "used_memory_human:")
	if !strings.Contains(infoMem2, "K") && !strings.Contains(infoMem2, "M") && !strings.Contains(infoMem2, "G") {
		t.Fatalf("expected K/M/G in used_memory_human, got snippet: %q", infoMem2[:min(200, len(infoMem2))])
	}

	ctx.Config.MaxMemory = "2gb"
	infoG := runCommand(t, ctx, buf, "INFO", "memory")
	mustContain(t, infoG, "maxmemory_human:")
	mustContain(t, infoG, "G")

	ctx.Config.MaxMemory = "4096gb"
	infoT := runCommand(t, ctx, buf, "INFO", "memory")
	mustContain(t, infoT, "maxmemory_human:")
	mustContain(t, infoT, "T")

	// Pub/sub: UNSUBSCRIBE / PUNSUBSCRIBE with no args (unsubscribe all).
	assertArrayLength(t, runCommand(t, ctx, buf, "SUBSCRIBE", "uca", "ucb"), 3)
	_ = runCommand(t, ctx, buf, "UNSUBSCRIBE")
	assertArrayLength(t, runCommand(t, ctx, buf, "PSUBSCRIBE", "pa*", "pb*"), 3)
	_ = runCommand(t, ctx, buf, "PUNSUBSCRIBE")

	// PUBLISH when PubSub manager is nil → 0 subscribers.
	ctx.PubSub = nil
	assertInteger(t, runCommand(t, ctx, buf, "PUBLISH", "ch", "m"), 0)
}

func TestInfoNilProvider(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = nil
	mustContain(t, runCommand(t, ctx, buf, "INFO"), "no info provider")
}

func TestDispatchUnknownCommand(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	reg := NewRegistry()
	ctx.Registry = reg
	v := resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "NOSUCHCMD"}}}
	if err := reg.Dispatch(ctx, v); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	out := buf.String()
	buf.Reset()
	if !strings.Contains(out, "unknown") {
		t.Fatalf("expected unknown command error, got %q", out)
	}
}

func TestRandomKeyEmptyAndPresent(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	assertNullBulkString(t, runCommand(t, ctx, buf, "RANDOMKEY"))
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "rkonly", "v"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "RANDOMKEY"), "rkonly")
}

func TestCoverageMoreBranches2(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = testInfoProvider{}

	ctx.Registry.Register(CommandMeta{
		Name: "CUSTOMKV", Arity: 5, FirstKey: 1, LastKey: 3, Step: 2,
		Flags: FlagRead, Handler: func(c *CommandContext) error { return writeOK(c.Writer) },
	})
	ci := mustParseOne(t, runCommand(t, ctx, buf, "COMMAND", "INFO", "customkv"))
	if ci.Type != '*' || len(ci.Array) != 1 || len(ci.Array[0].Array) < 6 {
		t.Fatalf("COMMAND INFO customkv: %+v", ci)
	}

	assertInteger(t, runCommand(t, ctx, buf, "EXPIRE", "expire_no_such", "10"), 0)
	assertInteger(t, runCommand(t, ctx, buf, "PTTL", "pttl_absent"), -2)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "pttl_none", "z"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "PTTL", "pttl_none"), -1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "pttl_ex", "z", "EX", "600"), "OK")
	pttlLive := mustParseOne(t, runCommand(t, ctx, buf, "PTTL", "pttl_ex"))
	if pttlLive.Type != ':' || pttlLive.Integer <= 0 {
		t.Fatalf("pttl live %+v", pttlLive)
	}

	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "lpx_ok", "a"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "LPUSHX", "lpx_ok", "b"), 2)
	assertInteger(t, runCommand(t, ctx, buf, "RPUSH", "rpx_ok", "a"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "RPUSHX", "rpx_ok", "b"), 2)

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hincr", "mx", strconv.FormatInt(math.MaxInt64, 10)), 1)
	assertError(t, runCommand(t, ctx, buf, "HINCRBY", "hincr", "mx", "1"), "overflow")
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hincr", "badf", "n"), 1)
	assertError(t, runCommand(t, ctx, buf, "HINCRBY", "hincr", "badf", "x"), "not an integer")
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hflt", "nf", "x"), 1)
	assertError(t, runCommand(t, ctx, buf, "HINCRBYFLOAT", "hflt", "nf", "1"), "not an integer")

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "gr", "hello"), "OK")
	assertBulkString(t, runCommand(t, ctx, buf, "GETRANGE", "gr", "-1", "-1"), "o")
	assertBulkString(t, runCommand(t, ctx, buf, "GETRANGE", "gr", "4", "2"), "")
	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "grlist", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "GETRANGE", "grlist", "0", "0"), "WRONGTYPE")

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "srpad", "ab"), "OK")
	assertInteger(t, runCommand(t, ctx, buf, "SETRANGE", "srpad", "5", "Z"), 6)
	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "srwrong", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "SETRANGE", "srwrong", "0", "x"), "WRONGTYPE")

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "ibmax", strconv.FormatInt(math.MaxInt64, 10)), "OK")
	assertError(t, runCommand(t, ctx, buf, "INCRBY", "ibmax", "1"), "overflow")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "dbmin", strconv.FormatInt(math.MinInt64, 10)), "OK")
	assertError(t, runCommand(t, ctx, buf, "DECRBY", "dbmin", "1"), "overflow")

	assertInteger(t, runCommand(t, ctx, buf, "SADD", "sia", "1"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "sib", "2"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "SINTERSTORE", "sidst", "sia", "sib"), 0)

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hlenmiss", "f", "v"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "DEL", "hlenmiss"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "HLEN", "hlenmiss"), 0)

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "gh_getset", "f", "v"), 1)
	assertError(t, runCommand(t, ctx, buf, "GETSET", "gh_getset", "x"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "appwt", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "APPEND", "appwt", "b"), "WRONGTYPE")
}

func TestDispatchEmptyAndInvalidArgs(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	reg := NewRegistry()
	ctx.Registry = reg
	ctx.Writer = resp.NewWriter(buf)

	empty := resp.Value{Type: '*', Array: []resp.Value{}}
	if err := reg.Dispatch(ctx, empty); err == nil || !strings.Contains(err.Error(), "empty command") {
		t.Fatalf("expected empty command error, got %v", err)
	}

	badNest := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"},
		{Type: '*', Array: []resp.Value{}},
	}}
	if err := reg.Dispatch(ctx, badNest); err == nil || !strings.Contains(err.Error(), "dispatch") {
		t.Fatalf("expected dispatch wrap error, got %v", err)
	}
}

func TestTransactionSessionNilBranches(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Session = nil
	assertError(t, runCommand(t, ctx, buf, "MULTI"), "syntax")
	assertSimpleString(t, runCommand(t, ctx, buf, "WATCH", "wk_nil"), "OK")
}

func TestCoverageMoreBranches3(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	// HMGET: missing key (none) → all nulls; wrong type; partial field hits.
	hmNone := mustParseOne(t, runCommand(t, ctx, buf, "HMGET", "hm_none", "a", "b"))
	if hmNone.Type != '*' || len(hmNone.Array) != 2 || !hmNone.Array[0].IsNull || !hmNone.Array[1].IsNull {
		t.Fatalf("hmget none %+v", hmNone)
	}
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "hm_wrong", "x"), "OK")
	assertError(t, runCommand(t, ctx, buf, "HMGET", "hm_wrong", "f"), "WRONGTYPE")
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hm_mix", "a", "1"), 1)
	hmMix := mustParseOne(t, runCommand(t, ctx, buf, "HMGET", "hm_mix", "a", "missing"))
	if hmMix.Type != '*' || len(hmMix.Array) != 2 || hmMix.Array[0].Str != "1" || !hmMix.Array[1].IsNull {
		t.Fatalf("hmget mix %+v", hmMix)
	}

	// Set ops: SUNION/SDIFF/SINTER with wrong-type key among sets.
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "su_a", "1"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "su_bad", "x"), "OK")
	assertError(t, runCommand(t, ctx, buf, "SUNION", "su_a", "su_bad"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SINTER", "su_a", "su_bad"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SDIFF", "su_a", "su_bad"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SUNIONSTORE", "dst", "su_a", "su_bad"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SINTERSTORE", "dst", "su_a", "su_bad"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "SDIFFSTORE", "dst", "su_a", "su_bad"), "WRONGTYPE")

	// Single-key SUNION / SDIFF.
	assertArrayLength(t, runCommand(t, ctx, buf, "SUNION", "su_a"), 1)
	assertArrayLength(t, runCommand(t, ctx, buf, "SDIFF", "su_a"), 1)

	// DBSIZE arity; STRLEN / SISMEMBER / INCRBYFLOAT wrong type.
	assertError(t, runCommand(t, ctx, buf, "DBSIZE", "x"), "wrong number")
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "sis", "m"), 1)
	assertError(t, runCommand(t, ctx, buf, "STRLEN", "sis"), "WRONGTYPE")
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "str_sis", "v"), "OK")
	assertError(t, runCommand(t, ctx, buf, "SISMEMBER", "str_sis", "x"), "WRONGTYPE")
	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "list_if", "a"), 1)
	assertError(t, runCommand(t, ctx, buf, "INCRBYFLOAT", "list_if", "1"), "WRONGTYPE")

	// Nested MULTI error path.
	assertSimpleString(t, runCommand(t, ctx, buf, "MULTI"), "OK")
	assertError(t, runCommand(t, ctx, buf, "MULTI"), "MULTI calls can not be nested")
}

func TestCoverageMoreBranches4(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	// Hash commands on a string key → WRONGTYPE across cmd* surface.
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "hstr", "v"), "OK")
	assertError(t, runCommand(t, ctx, buf, "HSET", "hstr", "f", "x"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HMSET", "hstr", "f", "x"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HGETALL", "hstr"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HKEYS", "hstr"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HVALS", "hstr"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HLEN", "hstr"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HDEL", "hstr", "f"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "HEXISTS", "hstr", "f"), "WRONGTYPE")

	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	futureMs := strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
	assertInteger(t, runCommand(t, ctx, buf, "EXPIREAT", "ea_absent", future), 0)
	assertInteger(t, runCommand(t, ctx, buf, "PEXPIREAT", "pea_absent", futureMs), 0)
	assertInteger(t, runCommand(t, ctx, buf, "PERSIST", "pers_absent"), 0)

	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "ltr", "a", "b"), 2)
	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "ltr_bad", "x"), "OK")
	assertError(t, runCommand(t, ctx, buf, "LTRIM", "ltr_bad", "0", "-1"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "LRANGE", "ltr_bad", "0", "-1"), "WRONGTYPE")
	assertError(t, runCommand(t, ctx, buf, "LREM", "ltr_bad", "0", "a"), "WRONGTYPE")

	assertError(t, runCommand(t, ctx, buf, "LSET", "no_list", "0", "z"), "no such key")
}

func TestCoverageMoreBranches5(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	ctx.Info = testInfoProvider{}

	assertNullBulkString(t, runCommand(t, ctx, buf, "LPOP", "lpop_empty_key"))
	assertNullBulkString(t, runCommand(t, ctx, buf, "RPOP", "rpop_empty_key"))

	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "lpc", "solo"), 1)
	lpc2 := mustParseOne(t, runCommand(t, ctx, buf, "LPOP", "lpc", "2"))
	if lpc2.Type != '*' || len(lpc2.Array) != 1 || lpc2.Array[0].Str != "solo" {
		t.Fatalf("lpop count %+v", lpc2)
	}

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "lis_bad", "v"), "OK")
	assertError(t, runCommand(t, ctx, buf, "LINSERT", "lis_bad", "BEFORE", "a", "b"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "hsnx_ok", "f", "1"), 1)
	assertInteger(t, runCommand(t, ctx, buf, "HSETNX", "hsnx_ok", "f", "2"), 0)
	assertError(t, runCommand(t, ctx, buf, "HSETNX", "lis_bad", "f", "1"), "WRONGTYPE")

	assertError(t, runCommand(t, ctx, buf, "CLIENT", "SETNAME"), "wrong number")
	assertError(t, runCommand(t, ctx, buf, "SELECT"), "wrong number")
	assertError(t, runCommand(t, ctx, buf, "COMMAND", "UNKNOWN"), "wrong number")

	_ = runCommand(t, ctx, buf, "INFO", "cpu")

	assertError(t, runCommand(t, ctx, buf, "INCRBY", "hsnx_ok", "1"), "WRONGTYPE")
}

func TestExecQueueContainsErrorReplyForUnknownCommand(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	assertSimpleString(t, runCommand(t, ctx, buf, "MULTI"), "OK")
	ctx.Session.MultiQueue = append(ctx.Session.MultiQueue,
		resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "NOTACMDXYZ"}}},
	)
	out := runCommand(t, ctx, buf, "EXEC")
	if !strings.Contains(out, "unknown") {
		t.Fatalf("expected unknown command in EXEC result, got %q", out)
	}
}

func TestCoverageMoreBranches6(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()

	assertInteger(t, runCommand(t, ctx, buf, "HSET", "gh_get", "f", "v"), 1)
	assertError(t, runCommand(t, ctx, buf, "GET", "gh_get"), "WRONGTYPE")

	assertInteger(t, runCommand(t, ctx, buf, "PEXPIRE", "pex_nope", "100"), 0)

	assertSimpleString(t, runCommand(t, ctx, buf, "SET", "t_str", "x"), "OK")
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "t_str"), "string")
	assertInteger(t, runCommand(t, ctx, buf, "HSET", "t_hash", "a", "b"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "t_hash"), "hash")
	assertInteger(t, runCommand(t, ctx, buf, "LPUSH", "t_list", "a"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "t_list"), "list")
	assertInteger(t, runCommand(t, ctx, buf, "SADD", "t_set", "m"), 1)
	assertSimpleString(t, runCommand(t, ctx, buf, "TYPE", "t_set"), "set")
}

func TestOOMNoEvictionReplies(t *testing.T) {
	val := strings.Repeat("x", 1024)

	ctx, buf := newTestContextLowMem()
	defer ctx.Store.Close()
	var setOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx, buf, "SET", "oom_"+strconv.Itoa(i), val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			setOOM = true
			break
		}
	}
	if !setOOM {
		t.Fatal("expected SET to return OOM under low maxmemory")
	}

	ctx2, buf2 := newTestContextLowMem()
	defer ctx2.Store.Close()
	var msetOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx2, buf2, "MSET", "a"+strconv.Itoa(i), val, "b"+strconv.Itoa(i), val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			msetOOM = true
			break
		}
	}
	if !msetOOM {
		t.Fatal("expected MSET to return OOM under low maxmemory")
	}

	ctx3, buf3 := newTestContextLowMem()
	defer ctx3.Store.Close()
	assertInteger(t, runCommand(t, ctx3, buf3, "SETNX", "nxseed", "1"), 1)
	var setnxOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx3, buf3, "SETNX", "nx_"+strconv.Itoa(i), val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			setnxOOM = true
			break
		}
	}
	if !setnxOOM {
		t.Fatal("expected SETNX to return OOM under low maxmemory")
	}

	ctx4, buf4 := newTestContextLowMem()
	defer ctx4.Store.Close()
	var msetnxOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx4, buf4, "MSETNX", "mxa"+strconv.Itoa(i), val, "mxb"+strconv.Itoa(i), val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			msetnxOOM = true
			break
		}
	}
	if !msetnxOOM {
		t.Fatal("expected MSETNX to return OOM under low maxmemory")
	}

	ctx5, buf5 := newTestContextLowMem()
	defer ctx5.Store.Close()
	assertSimpleString(t, runCommand(t, ctx5, buf5, "SET", "app0", "x"), "OK")
	var appendOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx5, buf5, "APPEND", "app0", val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			appendOOM = true
			break
		}
	}
	if !appendOOM {
		t.Fatal("expected APPEND to return OOM under low maxmemory")
	}

	ctx6, buf6 := newTestContextLowMem()
	defer ctx6.Store.Close()
	var setexOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx6, buf6, "SETEX", "sx"+strconv.Itoa(i), "3600", val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			setexOOM = true
			break
		}
	}
	if !setexOOM {
		t.Fatal("expected SETEX to return OOM under low maxmemory")
	}

	ctx7, buf7 := newTestContextLowMem()
	defer ctx7.Store.Close()
	var psetexOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx7, buf7, "PSETEX", "ps"+strconv.Itoa(i), "600000", val)
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			psetexOOM = true
			break
		}
	}
	if !psetexOOM {
		t.Fatal("expected PSETEX to return OOM under low maxmemory")
	}

	ctx10, buf10 := newTestContextLowMem()
	defer ctx10.Store.Close()
	var ibfOOM bool
	for i := 0; i < 500; i++ {
		out := runCommand(t, ctx10, buf10, "INCRBYFLOAT", "ibf"+strconv.Itoa(i), "1.5")
		if strings.HasPrefix(out, "-") && strings.Contains(out, "OOM") {
			ibfOOM = true
			break
		}
	}
	if !ibfOOM {
		t.Fatal("expected INCRBYFLOAT to return OOM under low maxmemory")
	}
}

func TestDispatchCheckArityAndValueToArgsErrors(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	reg := NewRegistry()
	ctx.Registry = reg
	ctx.Writer = resp.NewWriter(buf)

	// GET with wrong arity (Dispatch applies CheckArity before handler).
	getBad := resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "GET"}}}
	if err := reg.Dispatch(ctx, getBad); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if s := buf.String(); !strings.HasPrefix(s, "-") {
		t.Fatalf("expected arity error for GET, got %q", s)
	}
	buf.Reset()

	// SET with too few arguments (arity 0 in registry — handler enforces shape).
	setShort := resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "SET"}, {Type: '$', Str: "k"}}}
	if err := reg.Dispatch(ctx, setShort); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if s := buf.String(); !strings.HasPrefix(s, "-") {
		t.Fatalf("expected error for short SET, got %q", s)
	}
	buf.Reset()

	// DEL with only command name (variable arity -2 needs ≥2 tokens).
	delShort := resp.Value{Type: '*', Array: []resp.Value{{Type: '$', Str: "DEL"}}}
	if err := reg.Dispatch(ctx, delShort); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if s := buf.String(); !strings.HasPrefix(s, "-") {
		t.Fatalf("expected arity error for DEL, got %q", s)
	}
}

func TestCmdNameBytesToStrSubscriptionCount(t *testing.T) {
	if cmdName([][]byte{}) != "" {
		t.Fatal("cmdName empty args")
	}
	if bytesToStr(nil) != "" {
		t.Fatal("bytesToStr nil")
	}
	if subscriptionCount(nil) != 0 {
		t.Fatalf("subscriptionCount nil = %d", subscriptionCount(nil))
	}
}

func TestReplicateHelpersBranches(t *testing.T) {
	rep := &captureReplicator{}
	// ReplicateSet: TTL in the past → ttlms clamped to 0.
	ReplicateSet(rep, "k", []byte("v"), time.Now().Add(-time.Hour))
	// ReplicateLPop/RPop: n < 1 is a no-op.
	ReplicateLPop(rep, "k", 0)
	ReplicateRPop(rep, "k", 0)
	if len(rep.ps) < 1 {
		t.Fatal("expected at least ReplicateSet payload")
	}
}

func TestDispatchCoversHandlers(t *testing.T) {
	ctx, buf := newTestContext()
	defer ctx.Store.Close()
	reg := NewRegistry()
	ctx.Registry = reg
	ctx.Writer = resp.NewWriter(buf)
	ctx.Info = testInfoProvider{}

	dispatch := func(v resp.Value) {
		t.Helper()
		buf.Reset()
		if err := reg.Dispatch(ctx, v); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		_ = buf.String()
	}

	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "MGET"}, {Type: '$', Str: "a"}, {Type: '$', Str: "b"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "EXISTS"}, {Type: '$', Str: "x"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "TYPE"}, {Type: '$', Str: "y"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "KEYS"}, {Type: '$', Str: "*"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "DBSIZE"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "INFO"}, {Type: '$', Str: "stats"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "SET"}, {Type: '$', Str: "dk"}, {Type: '$', Str: "v"},
	}})
	dispatch(resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "GET"}, {Type: '$', Str: "dk"},
	}})
}
