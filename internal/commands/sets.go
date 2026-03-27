// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Set command handlers (SADD, SMEMBERS, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

func registerSetCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("SADD", -3, FlagWrite, cmdSAdd)
	reg("SREM", -3, FlagWrite, cmdSRem)
	reg("SMEMBERS", 2, FlagRead, cmdSMembers)
	reg("SISMEMBER", 3, FlagRead, cmdSIsMember)
	reg("SCARD", 2, FlagRead, cmdSCard)
	reg("SUNION", -2, FlagRead, cmdSUnion)
	reg("SUNIONSTORE", -3, FlagWrite, cmdSUnionStore)
	reg("SINTER", -2, FlagRead, cmdSInter)
	reg("SINTERSTORE", -3, FlagWrite, cmdSInterStore)
	reg("SDIFF", -2, FlagRead, cmdSDiff)
	reg("SDIFFSTORE", -3, FlagWrite, cmdSDiffStore)
}

func sortMemberBytes(members [][]byte) {
	sort.Slice(members, func(i, j int) bool {
		return bytes.Compare(members[i], members[j]) < 0
	})
}

func cmdSAdd(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SADD"))
	}
	key := bytesToStr(ctx.Args[1])
	members := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		members[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.SAdd(key, members)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("sadd: %w", err)
	}
	if n > 0 {
		ReplicateSAdd(ctx.Peer, key, members)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdSRem(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SREM"))
	}
	key := bytesToStr(ctx.Args[1])
	members := make([][]byte, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		members[i-2] = ctx.Args[i]
	}
	n, err := ctx.Store.SRem(key, members)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("srem: %w", err)
	}
	if n > 0 {
		ReplicateSRem(ctx.Peer, key, members)
	}
	return writeInt(ctx.Writer, int64(n))
}

func cmdSMembers(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("SMEMBERS"))
	}
	members, err := ctx.Store.SMembers(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("smembers: %w", err)
	}
	if members == nil {
		members = [][]byte{}
	}
	sortMemberBytes(members)
	arr := make([]resp.Value, len(members))
	for i, m := range members {
		arr[i] = resp.Value{Type: '$', Str: string(m)}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("smembers write: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdSIsMember(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("SISMEMBER"))
	}
	ok, err := ctx.Store.SIsMember(bytesToStr(ctx.Args[1]), ctx.Args[2])
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sismember: %w", err)
	}
	if ok {
		return writeInt(ctx.Writer, 1)
	}
	return writeInt(ctx.Writer, 0)
}

func cmdSCard(ctx *CommandContext) error {
	if len(ctx.Args) != 2 {
		return writeErr(ctx.Writer, argErr("SCARD"))
	}
	n, err := ctx.Store.SCard(bytesToStr(ctx.Args[1]))
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("scard: %w", err)
	}
	return writeInt(ctx.Writer, int64(n))
}

func unionMembers(ctx *CommandContext, keys []string) ([][]byte, error) {
	set := make(map[string]struct{})
	for _, k := range keys {
		members, err := ctx.Store.SMembers(k)
		if err != nil {
			return nil, err
		}
		if members == nil {
			continue
		}
		for _, m := range members {
			set[string(m)] = struct{}{}
		}
	}
	out := make([][]byte, 0, len(set))
	for m := range set {
		out = append(out, []byte(m))
	}
	sortMemberBytes(out)
	return out, nil
}

func interMembers(ctx *CommandContext, keys []string) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	members, err := ctx.Store.SMembers(keys[0])
	if err != nil {
		return nil, err
	}
	if members == nil {
		return nil, nil
	}
	cand := make(map[string]struct{})
	for _, m := range members {
		cand[string(m)] = struct{}{}
	}
	for _, k := range keys[1:] {
		mems, err := ctx.Store.SMembers(k)
		if err != nil {
			return nil, err
		}
		if mems == nil {
			return nil, nil
		}
		next := make(map[string]struct{})
		for _, m := range mems {
			ms := string(m)
			if _, ok := cand[ms]; ok {
				next[ms] = struct{}{}
			}
		}
		cand = next
		if len(cand) == 0 {
			return nil, nil
		}
	}
	out := make([][]byte, 0, len(cand))
	for m := range cand {
		out = append(out, []byte(m))
	}
	sortMemberBytes(out)
	return out, nil
}

func diffMembers(ctx *CommandContext, keys []string) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	first, err := ctx.Store.SMembers(keys[0])
	if err != nil {
		return nil, err
	}
	if first == nil {
		return nil, nil
	}
	set := make(map[string]struct{})
	for _, m := range first {
		set[string(m)] = struct{}{}
	}
	for _, k := range keys[1:] {
		mems, err := ctx.Store.SMembers(k)
		if err != nil {
			return nil, err
		}
		if mems == nil {
			continue
		}
		for _, m := range mems {
			delete(set, string(m))
		}
	}
	out := make([][]byte, 0, len(set))
	for m := range set {
		out = append(out, []byte(m))
	}
	sortMemberBytes(out)
	return out, nil
}

func writeMembers(ctx *CommandContext, members [][]byte) error {
	arr := make([]resp.Value, len(members))
	for i, m := range members {
		arr[i] = resp.Value{Type: '$', Str: string(m)}
	}
	if err := ctx.Writer.WriteArray(arr); err != nil {
		return fmt.Errorf("write members: %w", err)
	}
	return ctx.Writer.Flush()
}

func cmdSUnion(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("SUNION"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	members, err := unionMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sunion: %w", err)
	}
	return writeMembers(ctx, members)
}

func cmdSUnionStore(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SUNIONSTORE"))
	}
	dest := bytesToStr(ctx.Args[1])
	keys := make([]string, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		keys[i-2] = bytesToStr(ctx.Args[i])
	}
	members, err := unionMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sunionstore: %w", err)
	}
	if err := ctx.Store.ReplaceSet(dest, members); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("sunionstore: %w", err)
	}
	ReplicateSAdd(ctx.Peer, dest, members)
	return writeInt(ctx.Writer, int64(len(members)))
}

func cmdSInter(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("SINTER"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	members, err := interMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sinter: %w", err)
	}
	return writeMembers(ctx, members)
}

func cmdSInterStore(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SINTERSTORE"))
	}
	dest := bytesToStr(ctx.Args[1])
	keys := make([]string, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		keys[i-2] = bytesToStr(ctx.Args[i])
	}
	members, err := interMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sinterstore: %w", err)
	}
	if err := ctx.Store.ReplaceSet(dest, members); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("sinterstore: %w", err)
	}
	ReplicateSAdd(ctx.Peer, dest, members)
	return writeInt(ctx.Writer, int64(len(members)))
}

func cmdSDiff(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("SDIFF"))
	}
	keys := make([]string, len(ctx.Args)-1)
	for i := 1; i < len(ctx.Args); i++ {
		keys[i-1] = bytesToStr(ctx.Args[i])
	}
	members, err := diffMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sdiff: %w", err)
	}
	return writeMembers(ctx, members)
}

func cmdSDiffStore(ctx *CommandContext) error {
	if len(ctx.Args) < 3 {
		return writeErr(ctx.Writer, argErr("SDIFFSTORE"))
	}
	dest := bytesToStr(ctx.Args[1])
	keys := make([]string, len(ctx.Args)-2)
	for i := 2; i < len(ctx.Args); i++ {
		keys[i-2] = bytesToStr(ctx.Args[i])
	}
	members, err := diffMembers(ctx, keys)
	if err != nil {
		if isWrongType(err) {
			return writeErr(ctx.Writer, errWrongType)
		}
		return fmt.Errorf("sdiffstore: %w", err)
	}
	if err := ctx.Store.ReplaceSet(dest, members); err != nil {
		if errors.Is(err, store.ErrOOM) {
			return writeErr(ctx.Writer, errOOM)
		}
		return fmt.Errorf("sdiffstore: %w", err)
	}
	ReplicateSAdd(ctx.Peer, dest, members)
	return writeInt(ctx.Writer, int64(len(members)))
}
