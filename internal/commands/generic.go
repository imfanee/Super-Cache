// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Generic key command handlers (KEYS, TYPE, EXPIRE, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/resp"
)

func registerGenericCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("EXISTS", -2, FlagRead, cmdExists)
	reg("TYPE", 2, FlagRead, cmdType)
	reg("TTL", 2, FlagRead, cmdTTL)
	reg("PTTL", 2, FlagRead, cmdPTTL)
	reg("EXPIRE", 3, FlagWrite, cmdExpire)
	reg("EXPIREAT", 3, FlagWrite, cmdExpireAt)
	reg("PEXPIRE", 3, FlagWrite, cmdPExpire)
	reg("PEXPIREAT", 3, FlagWrite, cmdPExpireAt)
	reg("PERSIST", 2, FlagWrite, cmdPersist)
	reg("KEYS", 2, FlagRead, cmdKeys)
	reg("SCAN", 0, FlagRead, cmdScan)
	reg("RENAME", 3, FlagWrite, cmdRename)
	reg("RENAMENX", 3, FlagWrite, cmdRenameNX)
	reg("RANDOMKEY", 1, FlagRead, cmdRandomKey)
	reg("SELECT", 2, FlagWrite, cmdSelect)
	reg("DBSIZE", 1, FlagRead, cmdDBSize)
}

func cmdExists(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("EXISTS"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	n := ctx.Store.Exists(keys)
	keyspaceHit(ctx, int64(n))
	keyspaceMiss(ctx, int64(len(keys)-n))
	return writeInt(ctx.Writer, int64(n))
}

func cmdType(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("TYPE"))
	}
	t := ctx.Store.Type(bytesToStr(ctx.Args[1]))
	return writeSimple(ctx.Writer, t)
}

func cmdTTL(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("TTL"))
	}
	sec := ctx.Store.TTL(bytesToStr(ctx.Args[1]))
	return writeInt(ctx.Writer, sec)
}

func cmdPTTL(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("PTTL"))
	}
	sec := ctx.Store.TTL(bytesToStr(ctx.Args[1]))
	if sec == -2 {
		return writeInt(ctx.Writer, -2)
	}
	if sec == -1 {
		return writeInt(ctx.Writer, -1)
	}
	return writeInt(ctx.Writer, sec*1000)
}

func cmdExpire(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("EXPIRE"))
	}
	sec, err := ParseInt(ctx.Args[2])
	if err != nil || sec <= 0 {
		return writeErr(ctx.Writer, invalidExpireErr("EXPIRE"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.Expire(key, sec)
	if err != nil {
		return fmt.Errorf("expire: %w", err)
	}
	if ok {
		ReplicateExpire(ctx.Peer, key, sec)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdExpireAt(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("EXPIREAT"))
	}
	ts, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, invalidExpireErr("EXPIREAT"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.ExpireAt(key, time.Unix(ts, 0))
	if err != nil {
		return fmt.Errorf("expireat: %w", err)
	}
	if ok {
		ReplicateExpireAtUnix(ctx.Peer, key, ts)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdPExpire(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("PEXPIRE"))
	}
	ms, err := ParseInt(ctx.Args[2])
	if err != nil || ms <= 0 {
		return writeErr(ctx.Writer, invalidExpireErr("PEXPIRE"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.ExpireAt(key, time.Now().Add(time.Duration(ms)*time.Millisecond))
	if err != nil {
		return fmt.Errorf("pexpire: %w", err)
	}
	if ok {
		ReplicatePExpire(ctx.Peer, key, ms)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdPExpireAt(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("PEXPIREAT"))
	}
	ms, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, invalidExpireErr("PEXPIREAT"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.ExpireAt(key, time.UnixMilli(ms))
	if err != nil {
		return fmt.Errorf("pexpireat: %w", err)
	}
	if ok {
		ReplicatePExpireAtUnixMs(ctx.Peer, key, ms)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdPersist(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("PERSIST"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.Persist(key)
	if err != nil {
		return fmt.Errorf("persist: %w", err)
	}
	if ok {
		ReplicatePersist(ctx.Peer, key)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdKeys(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("KEYS"))
	}
	pat := bytesToStr(ctx.Args[1])
	keys := ctx.Store.Keys(pat)
	sort.Strings(keys)
	arr := make([]resp.Value, len(keys))
	for i, k := range keys {
		arr[i] = resp.Value{Type: '$', Str: k}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("keys write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdScan(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("SCAN"))
	}
	cur, err := ParseUint(ctx.Args[1])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	match := "*"
	count := 10
	wantType := ""
	i := 2
	for i < len(ctx.Args) {
		opt := strings.ToUpper(bytesToStr(ctx.Args[i]))
		switch opt {
		case "MATCH":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SCAN"))
			}
			match = bytesToStr(ctx.Args[i+1])
			i += 2
		case "COUNT":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SCAN"))
			}
			c, err := ParseInt(ctx.Args[i+1])
			if err != nil || c < 1 {
				return writeErr(ctx.Writer, errNotInt)
			}
			count = int(c)
			i += 2
		case "TYPE":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SCAN"))
			}
			wantType = bytesToStr(ctx.Args[i+1])
			i += 2
		default:
			return writeErr(ctx.Writer, argErr("SCAN"))
		}
	}
	next, keys := ctx.Store.Scan(uint64(cur), match, count, wantType)
	arr := make([]resp.Value, 2)
	arr[0] = resp.Value{Type: '$', Str: strconv.FormatUint(next, 10)}
	keyArr := make([]resp.Value, len(keys))
	for j, k := range keys {
		keyArr[j] = resp.Value{Type: '$', Str: k}
	}
	arr[1] = resp.Value{Type: '*', Array: keyArr}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("scan write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdRename(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("RENAME"))
	}
	src := bytesToStr(ctx.Args[1])
	dst := bytesToStr(ctx.Args[2])
	err := ctx.Store.Rename(src, dst)
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return writeErr(ctx.Writer, errNoSuchKey)
		}
		return fmt.Errorf("rename: %w", err)
	}
	ReplicateRename(ctx.Peer, src, dst)
	return writeOK(ctx.Writer)
}

func cmdRenameNX(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("RENAMENX"))
	}
	src := bytesToStr(ctx.Args[1])
	dst := bytesToStr(ctx.Args[2])
	if ctx.Store.Exists([]string{src}) == 0 {
		return writeErr(ctx.Writer, errNoSuchKey)
	}
	ok, err := ctx.Store.RenameNX(src, dst)
	if err != nil {
		return fmt.Errorf("renamenx: %w", err)
	}
	if ok {
		ReplicateRename(ctx.Peer, src, dst)
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdRandomKey(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("RANDOMKEY"))
	}
	keys := ctx.Store.Keys("*")
	if len(keys) == 0 {
		return writeBulkNil(ctx.Writer)
	}
	k := keys[rand.Intn(len(keys))]
	return writeBulk(ctx.Writer, []byte(k))
}

func cmdSelect(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("SELECT"))
	}
	db, err := ParseInt(ctx.Args[1])
	if err != nil || db != 0 {
		return writeErr(ctx.Writer, errDBRange)
	}
	if ctx.Session != nil {
		ctx.Session.DB = int(db)
	}
	return writeOK(ctx.Writer)
}

func cmdDBSize(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("DBSIZE"))
	}
	return writeInt(ctx.Writer, int64(ctx.Store.DBSize()))
}
