// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Hash command handlers (HSET, HGET, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

func registerHashCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("HSET", 0, FlagWrite, cmdHSet)
	reg("HMSET", 0, FlagWrite, cmdHMSet)
	reg("HGET", 3, FlagRead, cmdHGet)
	reg("HMGET", -3, FlagRead, cmdHMGet)
	reg("HGETALL", 2, FlagRead, cmdHGetAll)
	reg("HDEL", -3, FlagWrite, cmdHDel)
	reg("HEXISTS", 3, FlagRead, cmdHExists)
	reg("HLEN", 2, FlagRead, cmdHLen)
	reg("HKEYS", 2, FlagRead, cmdHKeys)
	reg("HVALS", 2, FlagRead, cmdHVals)
	reg("HINCRBY", 4, FlagWrite, cmdHIncrBy)
	reg("HINCRBYFLOAT", 4, FlagWrite, cmdHIncrByFloat)
	reg("HSETNX", 4, FlagWrite, cmdHSetNX)
}

func cmdHSet(ctx *CommandContext) error {
	if len(ctx.Args) < 4 || (len(ctx.Args)-2)%2 != 0 {
		return writeErr(ctx.Writer, argErr("HSET"))
	}
	key := bytesToStr(ctx.Args[1])
	pairs := make(map[string][]byte)
	for i := 2; i < len(ctx.Args); i += 2 {
		pairs[bytesToStr(ctx.Args[i])] = append([]byte(nil), ctx.Args[i+1]...)
	}
	n, err := ctx.Store.HSet(key, pairs)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("hset: %w", err)
	}
	ReplicateHash(ctx.Peer, key, pairs)
	return writeInt(ctx.Writer, int64(n))
}

func cmdHMSet(ctx *CommandContext) error {
	if len(ctx.Args) < 4 || (len(ctx.Args)-2)%2 != 0 {
		return writeErr(ctx.Writer, argErr("HMSET"))
	}
	key := bytesToStr(ctx.Args[1])
	pairs := make(map[string][]byte)
	for i := 2; i < len(ctx.Args); i += 2 {
		pairs[bytesToStr(ctx.Args[i])] = append([]byte(nil), ctx.Args[i+1]...)
	}
	_, err := ctx.Store.HSet(key, pairs)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("hmset: %w", err)
	}
	ReplicateHash(ctx.Peer, key, pairs)
	return writeOK(ctx.Writer)
}

func cmdHGet(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("HGET"))
	}
	key := bytesToStr(ctx.Args[1])
	field := bytesToStr(ctx.Args[2])
	v, err := ctx.Store.HGet(key, field)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hget: %w", err)
	}
	if v == nil {
		if ctx.Store.Type(key) == "hash" {
			keyspaceHit(ctx, 1)
		} else {
			keyspaceMiss(ctx, 1)
		}
		return writeBulkNil(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	return writeBulk(ctx.Writer, v)
}

func cmdHMGet(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("HMGET"))
	}
	key := bytesToStr(ctx.Args[1])
	nFields := len(ctx.Args) - 2
	kt := ctx.Store.Type(key)
	if kt != "none" && kt != "hash" {
		return writeErr(ctx.Writer, errWrongType)
	}
	if kt == "none" {
		keyspaceMiss(ctx, int64(nFields))
		arr := make([]resp.Value, nFields)
		for i := range arr {
			arr[i] = resp.Value{Type: '$', IsNull: true}
		}
		if err := ctx.Writer.WriteArray(arr); err != nil {
			return fmt.Errorf("hmget write: %w", err)
		}
		return ctx.Writer.Flush()
	}
	keyspaceHit(ctx, int64(nFields))
	arr := make([]resp.Value, nFields)
	for i := 2; i < len(ctx.Args); i++ {
		field := bytesToStr(ctx.Args[i])
		v, err := ctx.Store.HGet(key, field)
		if err != nil {
			if isWrongType(err) {
				return writeErr(ctx.Writer, errWrongType)
			}
			return fmt.Errorf("hmget: %w", err)
		}
		if v == nil {
			arr[i-2] = resp.Value{Type: '$', IsNull: true}
		} else {
			arr[i-2] = resp.Value{Type: '$', Str: string(v)}
		}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("hmget write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdHGetAll(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("HGETALL"))
	}
	m, err := ctx.Store.HGetAll(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hgetall: %w", err)
	}
	if m == nil {
		keyspaceMiss(ctx, 1)
		return writeArrayEmpty(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	arr := make([]resp.Value, 0, len(keys)*2)
	for _, k := range keys {
		arr = append(arr, resp.Value{Type: '$', Str: k}, resp.Value{Type: '$', Str: string(m[k])})
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("hgetall write: %w", err)
	}
	return ctx.Writer.Flush()
}

func writeArrayEmpty(w *resp.Writer) error {
	if err := w.WriteArray(nil); err != nil {
		return fmt.Errorf("write array: %w", err)
	}
	return w.Flush()
}

func cmdHDel(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("HDEL"))
	}
	key := bytesToStr(ctx.Args[1])
	fields := make([]string, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		fields[i-2] = bytesToStr(ctx.Args[i])
	}
	n, err := ctx.Store.HDel(key, fields)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hdel: %w", err)
	}
	if n > 0 {
		ReplicateHDel(ctx.Peer, key, fields)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdHExists(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("HEXISTS"))
	}
	key := bytesToStr(ctx.Args[1])
	field := bytesToStr(ctx.Args[2])
	ok, err := ctx.Store.HExists(key, field)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hexists: %w", err)
	}
	if ok {
		keyspaceHit(ctx, 1)
		return writeInt(ctx.Writer, 1)
	}
	if ctx.Store.Type(key) == "hash" {
		keyspaceHit(ctx, 1)
	} else {
		keyspaceMiss(ctx, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdHLen(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("HLEN"))
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.HLen(key)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hlen: %w", err)
	}
	if ctx.Store.Type(key) == "none" {
		keyspaceMiss(ctx, 1)
	} else {
		keyspaceHit(ctx, 1)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdHKeys(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("HKEYS"))
	}
	m, err := ctx.Store.HGetAll(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hkeys: %w", err)
	}
	if m == nil {
		keyspaceMiss(ctx, 1)
		return writeArrayEmpty(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	arr := make([]resp.Value, len(keys))
	for i, k := range keys {
		arr[i] = resp.Value{Type: '$', Str: k}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("hkeys write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdHVals(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("HVALS"))
	}
	m, err := ctx.Store.HGetAll(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("hvals: %w", err)
	}
	if m == nil {
		keyspaceMiss(ctx, 1)
		return writeArrayEmpty(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	arr := make([]resp.Value, len(keys))
	for i, k := range keys {
		arr[i] = resp.Value{Type: '$', Str: string(m[k])}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("hvals write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdHIncrBy(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("HINCRBY"))
	}
	delta, err := ParseInt(ctx.Args[3])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	field := bytesToStr(ctx.Args[2])
	n, err := ctx.Store.HIncrBy(key, field, delta)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if strings.Contains(err.Error(), "not an integer") {
			return writeErr(ctx.Writer, errNotInt)
		}
		if strings.Contains(err.Error(), "overflow") {
			return writeErr(ctx.Writer, errIncrOverflow)
		}
		return fmt.Errorf("hincrby: %w", err)
	}
	ReplicateHash(ctx.Peer, key, map[string][]byte{field: []byte(strconv.FormatInt(n, 10))})
	return writeInt(ctx.Writer, n)
}

func cmdHIncrByFloat(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("HINCRBYFLOAT"))
	}
	delta, err := ParseFloat(ctx.Args[3])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	field := bytesToStr(ctx.Args[2])
	f, err := ctx.Store.HIncrByFloat(key, field, delta)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if strings.Contains(err.Error(), "not") {
			return writeErr(ctx.Writer, errNotInt)
		}
		return fmt.Errorf("hincrbyfloat: %w", err)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	ReplicateHash(ctx.Peer, key, map[string][]byte{field: []byte(s)})
	return writeBulk(ctx.Writer, []byte(s))
}

func cmdHSetNX(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("HSETNX"))
	}
	key := bytesToStr(ctx.Args[1])
	field := bytesToStr(ctx.Args[2])
	val := ctx.Args[3]
	n, err := ctx.Store.HSetNX(key, field, val)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("hsetnx: %w", err)
	}
	if n == 1 {
		ReplicateHash(ctx.Peer, key, map[string][]byte{field: append([]byte(nil), val...)})
	}
	return writeInt(ctx.Writer, int64(n))
}
