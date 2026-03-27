// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// List command handlers (LPUSH, RPUSH, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

func registerListCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("LPUSH", -3, FlagWrite, cmdLPush)
	reg("RPUSH", -3, FlagWrite, cmdRPush)
	reg("LPUSHX", -3, FlagWrite, cmdLPushX)
	reg("RPUSHX", -3, FlagWrite, cmdRPushX)
	reg("LPOP", 0, FlagWrite, cmdLPop)
	reg("RPOP", 0, FlagWrite, cmdRPop)
	reg("LLEN", 2, FlagRead, cmdLLen)
	reg("LINDEX", 3, FlagRead, cmdLIndex)
	reg("LSET", 4, FlagWrite, cmdLSet)
	reg("LREM", 4, FlagWrite, cmdLRem)
	reg("LRANGE", 4, FlagRead, cmdLRange)
	reg("LTRIM", 4, FlagWrite, cmdLTrim)
	reg("LINSERT", 5, FlagWrite, cmdLInsert)
}

func cmdLPush(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("LPUSH"))
	}
	key := bytesToStr(ctx.Args[1])
	vals := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		vals[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.LPush(key, vals)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("lpush: %w", err)
	}
	ReplicateListPush(ctx.Peer, key, true, vals)
	return writeInt(ctx.Writer, int64(n))
}

func cmdRPush(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("RPUSH"))
	}
	key := bytesToStr(ctx.Args[1])
	vals := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		vals[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.RPush(key, vals)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("rpush: %w", err)
	}
	ReplicateListPush(ctx.Peer, key, false, vals)
	return writeInt(ctx.Writer, int64(n))
}

func cmdLPushX(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("LPUSHX"))
	}
	key := bytesToStr(ctx.Args[1])
	vals := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		vals[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.LPushX(key, vals)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("lpushx: %w", err)
	}
	if n > 0 {
		ReplicateListPush(ctx.Peer, key, true, vals)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdRPushX(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("RPUSHX"))
	}
	key := bytesToStr(ctx.Args[1])
	vals := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		vals[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.RPushX(key, vals)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("rpushx: %w", err)
	}
	if n > 0 {
		ReplicateListPush(ctx.Peer, key, false, vals)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdLPop(ctx *CommandContext) error {
	if len(ctx.Args) < 2 || len(ctx.Args) > 3 {
		return writeErr(ctx.Writer, argErr("LPOP"))
	}
	key := bytesToStr(ctx.Args[1])
	count := 1
	if len(ctx.Args) == 3 {
		c, err := ParseInt(ctx.Args[2])
		if err != nil || c <= 0 {
			return writeErr(ctx.Writer, errNotInt)
		}
		count = int(c)
	}
	out, err := ctx.Store.LPop(key, count)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("lpop: %w", err)
	}
	if len(out) > 0 {
		ReplicateLPop(ctx.Peer, key, len(out))
	}
	if len(ctx.Args) == 2 {
		if len(out) == 0 {
			return writeBulkNil(ctx.Writer)
		}
		return writeBulk(ctx.Writer, out[0])
	}
	arr := make([]resp.Value, len(out))
	for i, b := range out {
		arr[i] = resp.Value{Type: '$', Str: string(b)}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("lpop write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdRPop(ctx *CommandContext) error {
	if len(ctx.Args) < 2 || len(ctx.Args) > 3 {
		return writeErr(ctx.Writer, argErr("RPOP"))
	}
	key := bytesToStr(ctx.Args[1])
	count := 1
	if len(ctx.Args) == 3 {
		c, err := ParseInt(ctx.Args[2])
		if err != nil || c <= 0 {
			return writeErr(ctx.Writer, errNotInt)
		}
		count = int(c)
	}
	out, err := ctx.Store.RPop(key, count)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("rpop: %w", err)
	}
	if len(out) > 0 {
		ReplicateRPop(ctx.Peer, key, len(out))
	}
	if len(ctx.Args) == 2 {
		if len(out) == 0 {
			return writeBulkNil(ctx.Writer)
		}
		return writeBulk(ctx.Writer, out[0])
	}
	arr := make([]resp.Value, len(out))
	for i, b := range out {
		arr[i] = resp.Value{Type: '$', Str: string(b)}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("rpop write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdLLen(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("LLEN"))
	}
	n, err := ctx.Store.LLen(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("llen: %w", err)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdLIndex(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("LINDEX"))
	}
	idx, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	v, err := ctx.Store.LIndex(bytesToStr(ctx.Args[1]), idx)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("lindex: %w", err)
	}
	if v == nil {
		return writeBulkNil(ctx.Writer)
	}
	return writeBulk(ctx.Writer, v)
}

func cmdLSet(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("LSET"))
	}
	idx, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	err = ctx.Store.LSet(key, idx, ctx.Args[3])
	if err != nil {
		if errors.Is(err, store.ErrKeyNotFound) {
			return writeErr(ctx.Writer, errNoSuchKey)
		}
		if errors.Is(err, store.ErrListIndex) {
			return writeErr(ctx.Writer, "ERR index out of range")
		}
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("lset: %w", err)
	}
	ReplicateLSet(ctx.Peer, key, idx, ctx.Args[3])
	return writeOK(ctx.Writer)
}

func cmdLRem(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("LREM"))
	}
	count, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.LRem(key, int(count), ctx.Args[3])
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("lrem: %w", err)
	}
	if n > 0 {
		ReplicateLRem(ctx.Peer, key, int(count), ctx.Args[3])
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdLRange(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("LRANGE"))
	}
	start, err1 := ParseInt(ctx.Args[2])
	stop, err2 := ParseInt(ctx.Args[3])
	if err1 != nil || err2 != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	out, err := ctx.Store.LRange(bytesToStr(ctx.Args[1]), start, stop)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("lrange: %w", err)
	}
	if out == nil {
		out = [][]byte{}
	}
	arr := make([]resp.Value, len(out))
	for i, b := range out {
		arr[i] = resp.Value{Type: '$', Str: string(b)}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("lrange write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdLTrim(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("LTRIM"))
	}
	start, err1 := ParseInt(ctx.Args[2])
	stop, err2 := ParseInt(ctx.Args[3])
	if err1 != nil || err2 != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	err := ctx.Store.LTrim(key, start, stop)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("ltrim: %w", err)
	}
	ReplicateLTrim(ctx.Peer, key, start, stop)
	return writeOK(ctx.Writer)
}

func cmdLInsert(ctx *CommandContext) error {
	if len(ctx.Args) != 5 {
		return writeErr(ctx.Writer, argErr("LINSERT"))
	}
	where := strings.ToUpper(bytesToStr(ctx.Args[2]))
	var before bool
	switch where {
	case "BEFORE":
		before = true
	case "AFTER":
		before = false
	default:
		return writeErr(ctx.Writer, errSyntax)
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.LInsert(key, before, ctx.Args[3], ctx.Args[4])
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("linsert: %w", err)
	}
	if n < 0 {
		return writeInt(ctx.Writer, -1)
	}
	ReplicateLInsert(ctx.Peer, key, before, ctx.Args[3], ctx.Args[4])
	return writeInt(ctx.Writer, int64(n))
}
