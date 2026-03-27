// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Server command handlers (PING, INFO, FLUSHDB, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/resp"
)

func registerServerCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("PING", 0, FlagRead, cmdPing)
	reg("ECHO", 2, FlagRead, cmdEcho)
	reg("AUTH", 0, FlagWrite|FlagAdmin, cmdAuth)
	reg("INFO", 0, FlagRead, cmdInfo)
	reg("FLUSHDB", 1, FlagWrite|FlagDangerous, cmdFlushDB)
	reg("FLUSHALL", 1, FlagWrite|FlagDangerous, cmdFlushAll)
	reg("TIME", 1, FlagRead, cmdTime)
	reg("CLIENT", 0, FlagRead|FlagWrite, cmdClient)
	r.Register(CommandMeta{Name: "COMMAND", Arity: -1, Flags: FlagRead, Handler: cmdCommand})
}

func cmdPing(ctx *CommandContext) error {
	if ctx.Session != nil && ctx.Session.InSubscribeMode() {
		if len(ctx.Args) == 1 {
			arr := []resp.Value{
				{Type: '$', Str: "pong"},
				{Type: '$', Str: ""},
			}
			if err := ctx.Writer.WriteArray(arr); err != nil {
				return fmt.Errorf("ping subscribe: %w", err)
			}
			return ctx.Writer.Flush()
		}
		if len(ctx.Args) == 2 {
			arr := []resp.Value{
				{Type: '$', Str: "pong"},
				{Type: '$', Str: string(ctx.Args[1])},
			}
			if err := ctx.Writer.WriteArray(arr); err != nil {
				return fmt.Errorf("ping subscribe: %w", err)
			}
			return ctx.Writer.Flush()
		}
		return writeErr(ctx.Writer, argErr("PING"))
	}
	if len(ctx.Args) == 1 {
		return writeSimple(ctx.Writer, "PONG")
	}
	if len(ctx.Args) == 2 {
		return writeBulk(ctx.Writer, ctx.Args[1])
	}
	return writeErr(ctx.Writer, argErr("PING"))
}

func cmdEcho(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("ECHO"))
	}
	return writeBulk(ctx.Writer, ctx.Args[1])
}

func cmdAuth(ctx *CommandContext) error {
	if ctx.Config == nil {
		return writeErr(ctx.Writer, errAuthNoPass)
	}
	want := ctx.Config.AuthPassword
	if want == "" {
		return writeErr(ctx.Writer, errAuthNoPass)
	}
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("AUTH"))
	}
	if string(ctx.Args[1]) != want {
		return writeErr(ctx.Writer, errWrongPass)
	}
	if ctx.Session != nil {
		ctx.Session.Authenticated = true
	}
	return writeOK(ctx.Writer)
}

func cmdInfo(ctx *CommandContext) error {
	var sec string
	if len(ctx.Args) > 1 {
		sec = strings.ToLower(bytesToStr(ctx.Args[1]))
	}
	if ctx.Info == nil {
		return writeBulk(ctx.Writer, []byte("# no info provider\r\n"))
	}
	var b strings.Builder
	writeSec := func(name string, body func()) {
		if sec != "" && sec != "all" && sec != name {
			return
		}
		fmt.Fprintf(&b, "# %s\r\n", name)
		body()
	}
	writeSec("server", func() {
		fmt.Fprintf(&b, "redis_version:7.0.0-supercache\r\n")
		fmt.Fprintf(&b, "redis_mode:cluster\r\n")
		fmt.Fprintf(&b, "supercache_version:%s\r\n", ctx.Info.ServerVersion())
		fmt.Fprintf(&b, "os:%s %s\r\n", runtime.GOOS, runtime.GOARCH)
		fmt.Fprintf(&b, "arch_bits:64\r\n")
		fmt.Fprintf(&b, "process_id:%d\r\n", os.Getpid())
		exe, _ := os.Executable()
		if exe != "" {
			fmt.Fprintf(&b, "executable:%s\r\n", filepath.Clean(exe))
		}
		fmt.Fprintf(&b, "tcp_port:%d\r\n", ctx.Info.TCPPort())
		fmt.Fprintf(&b, "uptime_in_seconds:%d\r\n", ctx.Info.UptimeSeconds())
		up := ctx.Info.UptimeSeconds()
		fmt.Fprintf(&b, "uptime_in_days:%d\r\n", up/86400)
		fmt.Fprintf(&b, "hz:10\r\n")
		fmt.Fprintf(&b, "lru_clock:%d\r\n", time.Now().Unix()%(1<<24))
		fmt.Fprintf(&b, "supercache_node_id:%s\r\n", ctx.Info.NodeID())
		fmt.Fprintf(&b, "supercache_bootstrap:%s\r\n", ctx.Info.BootstrapState())
		if ctx.Config != nil {
			fmt.Fprintf(&b, "supercache_client_tls:%d\r\n", boolInt(ctx.Config.ClientTLSEnabled()))
			fmt.Fprintf(&b, "supercache_peer_tls:%d\r\n", boolInt(ctx.Config.PeerTLSEnabled()))
		}
	})
	writeSec("clients", func() {
		fmt.Fprintf(&b, "connected_clients:%d\r\n", ctx.Info.ConnectedClients())
	})
	writeSec("stats", func() {
		fmt.Fprintf(&b, "total_connections_received:%d\r\n", ctx.Info.TotalConnections())
		fmt.Fprintf(&b, "total_commands_processed:%d\r\n", ctx.Info.TotalCommands())
		fmt.Fprintf(&b, "instantaneous_ops_per_sec:%d\r\n", ctx.Info.OpsPerSec())
		fmt.Fprintf(&b, "keyspace_hits:%d\r\n", ctx.Info.KeyspaceHits())
		fmt.Fprintf(&b, "keyspace_misses:%d\r\n", ctx.Info.KeyspaceMisses())
	})
	writeSec("replication", func() {
		fmt.Fprintf(&b, "role:master\r\n")
		n := ctx.Info.ConnectedPeers()
		fmt.Fprintf(&b, "connected_peers:%d\r\n", n)
		addrs := ctx.Info.PeerAddresses()
		fmt.Fprintf(&b, "peer_addresses:%s\r\n", strings.Join(addrs, ","))
		for i, a := range addrs {
			fmt.Fprintf(&b, "slave%d:ip=%s,state=online\r\n", i, a)
		}
	})
	writeSec("memory", func() {
		mb := ctx.Store.MemBytes()
		fmt.Fprintf(&b, "used_memory:%d\r\n", mb)
		fmt.Fprintf(&b, "used_memory_human:%s\r\n", humanBytes(mb))
		if ctx.Config != nil {
			if maxB, err := ctx.Config.MaxMemoryBytes(); err == nil && maxB > 0 {
				fmt.Fprintf(&b, "maxmemory:%d\r\n", maxB)
				fmt.Fprintf(&b, "maxmemory_human:%s\r\n", humanBytes(int64(maxB)))
			}
			pol := strings.TrimSpace(ctx.Config.MaxMemoryPolicy)
			if pol == "" {
				pol = "noeviction"
			}
			fmt.Fprintf(&b, "maxmemory_policy:%s\r\n", pol)
		}
	})
	writeSec("keyspace", func() {
		fmt.Fprintf(&b, "db0:keys=%d,expires=%d\r\n", ctx.Store.DBSize(), ctx.Store.ExpireKeyCount())
	})
	return writeBulk(ctx.Writer, []byte(b.String()))
}

func cmdFlushDB(ctx *CommandContext) error {
	return flushClusterDB(ctx, "FLUSHDB")
}

func cmdFlushAll(ctx *CommandContext) error {
	return flushClusterDB(ctx, "FLUSHALL")
}

// flushClusterDB clears the only logical database. Super-Cache is single-DB (SELECT 0);
// FLUSHALL and FLUSHDB both map to Store.FlushDB but replicate under distinct ops for peers.
func flushClusterDB(ctx *CommandContext, replOp string) error {
	ctx.Store.FlushDB()
	if ctx.Peer != nil {
		_ = ctx.Peer.Replicate(peer.ReplicatePayload{Op: replOp})
	}
	return writeOK(ctx.Writer)
}

func cmdTime(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("TIME"))
	}
	now := time.Now()
	sec := now.Unix()
	usec := int64(now.Nanosecond() / 1000)
	arr := []resp.Value{
		{Type: ':', Integer: sec},
		{Type: ':', Integer: usec},
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("time write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdClient(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("CLIENT"))
	}
	sub := strings.ToUpper(bytesToStr(ctx.Args[1]))
	switch sub {
	case "ID":
		if len(ctx.Args) != 2 {
			return writeErr(ctx.Writer, argErr("CLIENT"))
		}
		id := int64(1)
		if ctx.Session != nil && ctx.Session.ID > 0 {
			id = ctx.Session.ID
		}
		return writeInt(ctx.Writer, id)
	case "LIST":
		if len(ctx.Args) != 2 {
			return writeErr(ctx.Writer, argErr("CLIENT"))
		}
		id := int64(1)
		addr := "unknown"
		name := ""
		cmd := "client"
		if ctx.Session != nil {
			if ctx.Session.ID > 0 {
				id = ctx.Session.ID
			}
			name = ctx.Session.Name
			if ctx.Session.Conn != nil && ctx.Session.Conn.RemoteAddr() != nil {
				addr = ctx.Session.Conn.RemoteAddr().String()
			}
		}
		line := fmt.Sprintf("addr=%s id=%d name=%s cmd=%s\n", addr, id, name, cmd)
		return writeBulk(ctx.Writer, []byte(line))
	case "SETNAME":
		if len(ctx.Args) != 3 {
			return writeErr(ctx.Writer, argErr("CLIENT"))
		}
		if ctx.Session != nil {
			ctx.Session.Name = bytesToStr(ctx.Args[2])
		}
		return writeOK(ctx.Writer)
	case "GETNAME":
		if len(ctx.Args) != 2 {
			return writeErr(ctx.Writer, argErr("CLIENT"))
		}
		if ctx.Session == nil || ctx.Session.Name == "" {
			return writeBulkNil(ctx.Writer)
		}
		return writeBulk(ctx.Writer, []byte(ctx.Session.Name))
	default:
		return writeErr(ctx.Writer, errSyntax)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const u = 1024
	f := float64(n)
	switch {
	case n < u:
		return fmt.Sprintf("%dB", n)
	case n < u*u:
		return fmt.Sprintf("%.2fK", f/u)
	case n < u*u*u:
		return fmt.Sprintf("%.2fM", f/(u*u))
	case n < u*u*u*u:
		return fmt.Sprintf("%.2fG", f/(u*u*u))
	default:
		return fmt.Sprintf("%.2fT", f/(u*u*u*u))
	}
}

func cmdCommand(ctx *CommandContext) error {
	if ctx.Registry == nil {
		return writeErr(ctx.Writer, "ERR internal error")
	}
	args := ctx.Args
	if len(args) < 1 {
		return writeErr(ctx.Writer, argErr("COMMAND"))
	}
	if len(args) == 1 {
		return writeCommandFullList(ctx)
	}
	sub := strings.ToUpper(bytesToStr(args[1]))
	switch sub {
	case "COUNT":
		if len(args) != 2 {
			return writeErr(ctx.Writer, argErr("COMMAND"))
		}
		return writeInt(ctx.Writer, int64(len(ctx.Registry.Commands())))
	case "INFO":
		if len(args) < 3 {
			return writeErr(ctx.Writer, argErr("COMMAND"))
		}
		return writeCommandInfoNames(ctx, args[2:])
	default:
		return writeErr(ctx.Writer, argErr("COMMAND"))
	}
}

func writeCommandFullList(ctx *CommandContext) error {
	cmds := ctx.Registry.Commands()
	out := make([]resp.Value, 0, len(cmds))
	for _, m := range cmds {
		v, err := commandMetaToRESP(m)
		if err != nil {
			return fmt.Errorf("command list: %w", err)
		}
		out = append(out, v)
	}
	if err := ctx.Writer.WriteArray(out); err != nil {
		return fmt.Errorf("command list: %w", err)
	}
	return ctx.Writer.Flush()
}

func commandMetaToRESP(m CommandMeta) (resp.Value, error) {
	fk, lk, st := commandKeyIndices(m)
	flags := redisFlagList(m)
	flagVals := make([]resp.Value, len(flags))
	for i, f := range flags {
		flagVals[i] = resp.Value{Type: '$', Str: f}
	}
	nameLower := strings.ToLower(m.Name)
	inner := []resp.Value{
		{Type: '$', Str: nameLower},
		{Type: ':', Integer: int64(m.Arity)},
		{Type: '*', Array: flagVals},
		{Type: ':', Integer: fk},
		{Type: ':', Integer: lk},
		{Type: ':', Integer: st},
	}
	return resp.Value{Type: '*', Array: inner}, nil
}

func commandKeyIndices(m CommandMeta) (fk, lk, st int64) {
	if m.FirstKey != 0 || m.LastKey != 0 {
		step := int64(m.Step)
		if step == 0 {
			step = 1
		}
		return int64(m.FirstKey), int64(m.LastKey), step
	}
	if m.Arity == 1 {
		return 0, 0, 0
	}
	step := int64(m.Step)
	if step == 0 {
		step = 1
	}
	return 2, 2, step
}

func redisFlagList(m CommandMeta) []string {
	var fs []string
	if m.Flags&FlagRead != 0 && m.Flags&FlagWrite == 0 {
		fs = append(fs, "readonly")
	}
	if m.Flags&FlagWrite != 0 {
		fs = append(fs, "write")
	}
	if m.Flags&FlagAdmin != 0 {
		fs = append(fs, "admin")
	}
	return fs
}

func writeCommandInfoNames(ctx *CommandContext, names [][]byte) error {
	out := make([]resp.Value, 0, len(names))
	for _, nb := range names {
		n := strings.ToLower(bytesToStr(nb))
		meta, ok := ctx.Registry.Lookup(n)
		if !ok {
			out = append(out, resp.Value{Type: '$', IsNull: true})
			continue
		}
		v, err := commandMetaToRESP(meta)
		if err != nil {
			return fmt.Errorf("command info: %w", err)
		}
		out = append(out, v)
	}
	if err := ctx.Writer.WriteArray(out); err != nil {
		return fmt.Errorf("command info: %w", err)
	}
	return ctx.Writer.Flush()
}
