// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// String command handlers (GET, SET, APPEND, INCR, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

func registerStringCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("GET", 2, FlagRead, cmdGet)
	reg("SET", 0, FlagWrite, cmdSet)
	reg("SETNX", 3, FlagWrite, cmdSetNX)
	reg("SETEX", 4, FlagWrite, cmdSetEX)
	reg("PSETEX", 4, FlagWrite, cmdPSetEX)
	reg("GETSET", 3, FlagWrite, cmdGetSet)
	reg("GETDEL", 2, FlagWrite, cmdGetDel)
	reg("GETEX", 0, FlagRead|FlagWrite, cmdGetEx)
	reg("MGET", -2, FlagRead, cmdMGet)
	reg("MSET", 0, FlagWrite, cmdMSet)
	reg("MSETNX", 0, FlagWrite, cmdMSetNX)
	reg("DEL", -2, FlagWrite, cmdDel)
	reg("INCR", 2, FlagWrite, cmdIncr)
	reg("DECR", 2, FlagWrite, cmdDecr)
	reg("INCRBY", 3, FlagWrite, cmdIncrBy)
	reg("DECRBY", 3, FlagWrite, cmdDecrBy)
	reg("INCRBYFLOAT", 3, FlagWrite, cmdIncrByFloat)
	reg("APPEND", 3, FlagWrite, cmdAppend)
	reg("STRLEN", 2, FlagRead, cmdStrLen)
	reg("GETRANGE", 4, FlagRead, cmdGetRange)
	reg("SETRANGE", 4, FlagWrite, cmdSetRange)
}

func cmdGet(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("GET"))
	}
	key := bytesToStr(ctx.Args[1])
	v, err := ctx.Store.Get(key)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("get: %w", err)
	}
	if v == nil {
		keyspaceMiss(ctx, 1)
		return writeBulkNil(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	return writeBulk(ctx.Writer, v)
}

func cmdSet(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SET"))
	}
	key := bytesToStr(ctx.Args[1])
	val := ctx.Args[2]
	var nx, xx, get, keepttl bool
	var exMs int64 = -1
	var at time.Time
	var haveAt bool
	i := 3
	for i < len(ctx.Args) {
		opt := strings.ToUpper(bytesToStr(ctx.Args[i]))
		switch opt {
		case "NX":
			nx = true
			i++
		case "XX":
			xx = true
			i++
		case "GET":
			get = true
			i++
		case "KEEPTTL":
			keepttl = true
			i++
		case "EX":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SET"))
			}
			sec, err := ParseInt(ctx.Args[i+1])
			if err != nil || sec <= 0 {
				return writeErr(ctx.Writer, invalidExpireErr("SET"))
			}
			exMs = sec * 1000
			i += 2
		case "PX":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SET"))
			}
			ms, err := ParseInt(ctx.Args[i+1])
			if err != nil || ms <= 0 {
				return writeErr(ctx.Writer, invalidExpireErr("SET"))
			}
			exMs = ms
			i += 2
		case "EXAT":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SET"))
			}
			ts, err := ParseInt(ctx.Args[i+1])
			if err != nil {
				return writeErr(ctx.Writer, invalidExpireErr("SET"))
			}
			at = time.Unix(ts, 0)
			haveAt = true
			i += 2
		case "PXAT":
			if i+1 >= len(ctx.Args) {
				return writeErr(ctx.Writer, argErr("SET"))
			}
			ms, err := ParseInt(ctx.Args[i+1])
			if err != nil {
				return writeErr(ctx.Writer, invalidExpireErr("SET"))
			}
			at = time.UnixMilli(ms)
			haveAt = true
			i += 2
		default:
			return writeErr(ctx.Writer, argErr("SET"))
		}
	}
	var exp time.Time
	if haveAt {
		exp = at
	} else if exMs >= 0 {
		exp = time.Now().Add(time.Duration(exMs) * time.Millisecond)
	}
	if keepttl {
		ms := ctx.Store.TTLMs(key)
		if ms > 0 {
			exp = time.Now().Add(time.Duration(ms) * time.Millisecond)
		}
	}
	if nx && xx {
		return writeErr(ctx.Writer, errSyntax)
	}
	if get {
		prev, err := ctx.Store.Get(key)
		if err != nil && !isWrongType(err) {
			return fmt.Errorf("get in set: %w", err)
		}
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		ok, err := applySet(ctx, key, val, exp, nx, xx)
		if err != nil {
			return fmt.Errorf("set: %w", err)
		}
		if !ok {
			return writeBulkNil(ctx.Writer)
		}
		if prev == nil {
			return writeBulkNil(ctx.Writer)
		}
		return writeBulk(ctx.Writer, prev)
	}
	ok, err := applySet(ctx, key, val, exp, nx, xx)
	if err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("set: %w", err)
	}
	if !ok {
		return writeBulkNil(ctx.Writer)
	}
	return writeOK(ctx.Writer)
}

func applySet(ctx *CommandContext, key string, val []byte, exp time.Time, nx, xx bool) (bool, error) {
	var err error
	var ok bool
	switch {
	case nx && !xx:
		ok, err = ctx.Store.SetNX(key, val, exp)
	case xx && !nx:
		ok, err = ctx.Store.SetXX(key, val, exp)
	default:
		err = ctx.Store.Set(key, val, exp)
		ok = true
	}
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	var ttlms int64
	if !exp.IsZero() {
		ttlms = time.Until(exp).Milliseconds()
		if ttlms < 0 {
			ttlms = 0
		}
	}
	ReplicateSet(ctx.Peer, key, val, exp)
	return true, nil
}

func cmdSetNX(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("SETNX"))
	}
	key := bytesToStr(ctx.Args[1])
	ok, err := ctx.Store.SetNX(key, ctx.Args[2], time.Time{})
	if err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("setnx: %w", err)
	}
	if ok {
		ReplicateSet(ctx.Peer, key, ctx.Args[2], time.Time{})
	}
	if ok {
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdSetEX(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("SETEX"))
	}
	sec, err := ParseInt(ctx.Args[2])
	if err != nil || sec <= 0 {
		return writeErr(ctx.Writer, invalidExpireErr("SETEX"))
	}
	key := bytesToStr(ctx.Args[1])
	exp := time.Now().Add(time.Duration(sec) * time.Second)
	if err := ctx.Store.Set(key, ctx.Args[3], exp); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("setex: %w", err)
	}
	ReplicateSet(ctx.Peer, key, ctx.Args[3], exp)
	return writeOK(ctx.Writer)
}

func cmdPSetEX(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("PSETEX"))
	}
	ms, err := ParseInt(ctx.Args[2])
	if err != nil || ms <= 0 {
		return writeErr(ctx.Writer, invalidExpireErr("PSETEX"))
	}
	key := bytesToStr(ctx.Args[1])
	exp := time.Now().Add(time.Duration(ms) * time.Millisecond)
	if err := ctx.Store.Set(key, ctx.Args[3], exp); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("psetex: %w", err)
	}
	ReplicateSet(ctx.Peer, key, ctx.Args[3], exp)
	return writeOK(ctx.Writer)
}

func cmdGetSet(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("GETSET"))
	}
	key := bytesToStr(ctx.Args[1])
	prev, ok, err := ctx.Store.GetSet(key, ctx.Args[2], time.Time{})
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("getset: %w", err)
	}
	if !ok {
		return writeBulkNil(ctx.Writer)
	}
	ReplicateSet(ctx.Peer, key, ctx.Args[2], time.Time{})
	if prev == nil {
		return writeBulkNil(ctx.Writer)
	}
	return writeBulk(ctx.Writer, prev)
}

func cmdGetDel(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("GETDEL"))
	}
	key := bytesToStr(ctx.Args[1])
	prev, err := ctx.Store.Get(key)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("getdel: %w", err)
	}
	if prev == nil {
		return writeBulkNil(ctx.Writer)
	}
	ctx.Store.Del([]string{key})
	ReplicateDel(ctx.Peer, key)
	return writeBulk(ctx.Writer, prev)
}

func cmdGetEx(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("GETEX"))
	}
	key := bytesToStr(ctx.Args[1])
	if len(ctx.Args) == 2 {
		v, err := ctx.Store.Get(key)
		if err != nil {
			if isWrongType(err) {
				return writeErr(ctx.Writer, errWrongType)
			}
			return fmt.Errorf("getex: %w", err)
		}
		if v == nil {
			keyspaceMiss(ctx, 1)
			return writeBulkNil(ctx.Writer)
		}
		keyspaceHit(ctx, 1)
		return writeBulk(ctx.Writer, v)
	}
	opts := ctx.Args[2:]
	i := 0
	var (
		exp     time.Time
		haveExp bool
	)
	for i < len(opts) {
		op := strings.ToUpper(bytesToStr(opts[i]))
		switch op {
		case "EX":
			if i+1 >= len(opts) {
				return writeErr(ctx.Writer, argErr("GETEX"))
			}
			sec, err := ParseInt(opts[i+1])
			if err != nil || sec <= 0 {
				return writeErr(ctx.Writer, invalidExpireErr("GETEX"))
			}
			exp = time.Now().Add(time.Duration(sec) * time.Second)
			haveExp = true
			i += 2
		case "PX":
			if i+1 >= len(opts) {
				return writeErr(ctx.Writer, argErr("GETEX"))
			}
			ms, err := ParseInt(opts[i+1])
			if err != nil || ms <= 0 {
				return writeErr(ctx.Writer, invalidExpireErr("GETEX"))
			}
			exp = time.Now().Add(time.Duration(ms) * time.Millisecond)
			haveExp = true
			i += 2
		case "EXAT":
			if i+1 >= len(opts) {
				return writeErr(ctx.Writer, argErr("GETEX"))
			}
			ts, err := ParseInt(opts[i+1])
			if err != nil {
				return writeErr(ctx.Writer, invalidExpireErr("GETEX"))
			}
			exp = time.Unix(ts, 0)
			haveExp = true
			i += 2
		case "PXAT":
			if i+1 >= len(opts) {
				return writeErr(ctx.Writer, argErr("GETEX"))
			}
			ms, err := ParseInt(opts[i+1])
			if err != nil {
				return writeErr(ctx.Writer, invalidExpireErr("GETEX"))
			}
			exp = time.UnixMilli(ms)
			haveExp = true
			i += 2
		case "PERSIST":
			_, err := ctx.Store.Persist(key)
			if err != nil {
				return fmt.Errorf("getex persist: %w", err)
			}
			ReplicatePersist(ctx.Peer, key)
			i++
		default:
			return writeErr(ctx.Writer, argErr("GETEX"))
		}
	}
	v, err := ctx.Store.Get(key)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("getex: %w", err)
	}
	if v == nil {
		keyspaceMiss(ctx, 1)
		return writeBulkNil(ctx.Writer)
	}
	keyspaceHit(ctx, 1)
	if haveExp {
		_, _ = ctx.Store.ExpireAt(key, exp)
		ReplicatePExpireAtUnixMs(ctx.Peer, key, exp.UnixMilli())
	}
	return writeBulk(ctx.Writer, v)
}

func cmdMGet(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("MGET"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	vals, err := ctx.Store.MGet(keys)
	if err != nil {
		return fmt.Errorf("mget: %w", err)
	}
	for _, v := range vals {
		if v == nil {
			keyspaceMiss(ctx, 1)
		} else {
			keyspaceHit(ctx, 1)
		}
	}
	arr := make([]resp.Value, len(vals))
	for i, v := range vals {
		if v == nil {
			arr[i] = resp.Value{Type: '$', IsNull: true}
		} else {
			arr[i] = resp.Value{Type: '$', Str: string(v)}
		}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("mget write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdMSet(ctx *CommandContext) error {
	if len(ctx.Args) < 3 || (len(ctx.Args)-1)%2 != 0 {
		return writeErr(ctx.Writer, argErr("MSET"))
	}
	pairs := make(map[string][]byte)
	for i := 1; i < len(ctx.Args); i += 2 {
		pairs[bytesToStr(ctx.Args[i])] = append([]byte(nil), ctx.Args[i+1]...)
	}
	if err := ctx.Store.MSet(pairs); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("mset: %w", err)
	}
	for k, v := range pairs {
		ReplicateSet(ctx.Peer, k, v, time.Time{})
	}
	return writeOK(ctx.Writer)
}

func cmdMSetNX(ctx *CommandContext) error {
	if len(ctx.Args) < 3 || (len(ctx.Args)-1)%2 != 0 {
		return writeErr(ctx.Writer, argErr("MSETNX"))
	}
	pairs := make(map[string][]byte)
	for i := 1; i < len(ctx.Args); i += 2 {
		pairs[bytesToStr(ctx.Args[i])] = append([]byte(nil), ctx.Args[i+1]...)
	}
	ok, err := ctx.Store.MSetNX(pairs)
	if err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("msetnx: %w", err)
	}
	if ok {
		for k, v := range pairs {
			ReplicateSet(ctx.Peer, k, v, time.Time{})
		}
	}
	if ok {
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdDel(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("DEL"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	// Check existence before delete so we only replicate keys that were actually removed.
	existing := make(map[string]bool, len(keys))
	for _, k := range keys {
		if ctx.Store.Exists([]string{k}) > 0 {
			existing[k] = true
		}
	}
	n := ctx.Store.Del(keys)
	for k := range existing {
		ReplicateDel(ctx.Peer, k)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdIncr(ctx *CommandContext) error { return doIncr(ctx, 1) }
func cmdDecr(ctx *CommandContext) error { return doIncr(ctx, -1) }

func doIncr(ctx *CommandContext, delta int64) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr(cmdName(ctx.Args)))
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.IncrBy(key, delta)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if strings.Contains(err.Error(), "overflow") {
			return writeErr(ctx.Writer, errIncrOverflow)
		}
		if strings.Contains(err.Error(), "not an integer") {
			return writeErr(ctx.Writer, errNotInt)
		}
		return fmt.Errorf("incr: %w", err)
	}
	ReplicateSet(ctx.Peer, key, []byte(strconv.FormatInt(n, 10)), time.Time{})
	return writeInt(ctx.Writer, n)
}

func cmdIncrBy(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("INCRBY"))
	}
	delta, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.IncrBy(key, delta)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if strings.Contains(err.Error(), "overflow") {
			return writeErr(ctx.Writer, errIncrOverflow)
		}
		if strings.Contains(err.Error(), "not an integer") {
			return writeErr(ctx.Writer, errNotInt)
		}
		return fmt.Errorf("incrby: %w", err)
	}
	ReplicateSet(ctx.Peer, key, []byte(strconv.FormatInt(n, 10)), time.Time{})
	return writeInt(ctx.Writer, n)
}

func cmdDecrBy(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("DECRBY"))
	}
	delta, err := ParseInt(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.DecrBy(key, delta)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if strings.Contains(err.Error(), "overflow") {
			return writeErr(ctx.Writer, errIncrOverflow)
		}
		if strings.Contains(err.Error(), "not an integer") {
			return writeErr(ctx.Writer, errNotInt)
		}
		return fmt.Errorf("decrby: %w", err)
	}
	ReplicateSet(ctx.Peer, key, []byte(strconv.FormatInt(n, 10)), time.Time{})
	return writeInt(ctx.Writer, n)
}

func cmdIncrByFloat(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("INCRBYFLOAT"))
	}
	delta, err := ParseFloat(ctx.Args[2])
	if err != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	v, err := ctx.Store.Get(key)
	if err != nil && !isWrongType(err) {
		return fmt.Errorf("incrbyfloat: %w", err)
	}
	if isWrongType(err) {
		return writeErr(ctx.Writer, errWrongType)
	}
	var f float64
	if v != nil {
		f, err = strconv.ParseFloat(strings.TrimSpace(string(v)), 64)
		if err != nil {
			return writeErr(ctx.Writer, errNotInt)
		}
	}
	out := f + delta
	s := strconv.FormatFloat(out, 'f', -1, 64)
	if err := ctx.Store.Set(key, []byte(s), time.Time{}); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("incrbyfloat: %w", err)
	}
	ReplicateSet(ctx.Peer, key, []byte(s), time.Time{})
	return writeBulk(ctx.Writer, []byte(s))
}

func cmdAppend(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("APPEND"))
	}
	key := bytesToStr(ctx.Args[1])
	n, err := ctx.Store.Append(key, ctx.Args[2])
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("append: %w", err)
	}
	// replicate full value
	v, _ := ctx.Store.Get(key)
	if v != nil {
		ReplicateSet(ctx.Peer, key, v, time.Time{})
	}
	return writeInt(ctx.Writer, n)
}

func cmdStrLen(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("STRLEN"))
	}
	v, err := ctx.Store.Get(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("strlen: %w", err)
	}
	if v == nil {
		keyspaceMiss(ctx, 1)
		return writeInt(ctx.Writer, 0)
	}
	keyspaceHit(ctx, 1)
	return writeInt(ctx.Writer, int64(len(v)))
}

func cmdGetRange(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("GETRANGE"))
	}
	start, err1 := ParseInt(ctx.Args[2])
	end, err2 := ParseInt(ctx.Args[3])
	if err1 != nil || err2 != nil {
		return writeErr(ctx.Writer, errNotInt)
	}
	v, err := ctx.Store.Get(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("getrange: %w", err)
	}
	if v == nil {
		keyspaceMiss(ctx, 1)
		return writeBulk(ctx.Writer, []byte{})
	}
	keyspaceHit(ctx, 1)
	s := string(v)
	n := len(s)
	st := normRange(start, n)
	en := normRange(end, n)
	if st < 0 {
		st = 0
	}
	if en >= n {
		en = n - 1
	}
	if st > en {
		return writeBulk(ctx.Writer, []byte{})
	}
	return writeBulk(ctx.Writer, []byte(s[st:en+1]))
}

func normRange(idx int64, n int) int {
	i := int(idx)
	if idx < 0 {
		i = n + int(idx)
	}
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func cmdSetRange(ctx *CommandContext) error {
	if len(ctx.Args) != 4 {
		return writeErr(ctx.Writer, argErr("SETRANGE"))
	}
	offset, err := ParseInt(ctx.Args[2])
	if err != nil || offset < 0 {
		return writeErr(ctx.Writer, errNotInt)
	}
	key := bytesToStr(ctx.Args[1])
	val := ctx.Args[3]
	v, err := ctx.Store.Get(key)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("setrange: %w", err)
	}
	var base []byte
	if v != nil {
		base = append([]byte(nil), v...)
	}
	if int(offset) > len(base) {
		pad := int(offset) - len(base)
		base = append(base, bytes.Repeat([]byte{'\x00'}, pad)...)
	}
	end := int(offset) + len(val)
	if end > len(base) {
		nbase := make([]byte, end)
		copy(nbase, base)
		base = nbase
	}
	copy(base[int(offset):], val)
	if err := ctx.Store.Set(key, base, time.Time{}); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("setrange: %w", err)
	}
	ReplicateSet(ctx.Peer, key, base, time.Time{})
	return writeInt(ctx.Writer, int64(len(base)))
}
