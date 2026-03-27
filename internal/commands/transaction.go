// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Transaction command handlers (MULTI, EXEC, WATCH, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"bytes"
	"fmt"

	"github.com/supercache/supercache/internal/resp"
)

func registerTransactionCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("MULTI", 1, FlagWrite, cmdMulti)
	reg("EXEC", 1, FlagRead|FlagWrite, cmdExec)
	reg("DISCARD", 1, FlagWrite, cmdDiscard)
	reg("WATCH", -2, FlagWrite, cmdWatch)
	reg("UNWATCH", 1, FlagWrite, cmdUnwatch)
}

func cmdMulti(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("MULTI"))
	}
	if ctx.Session == nil {
		return writeErr(ctx.Writer, errSyntax)
	}
	if ctx.Session.InMulti {
		return writeErr(ctx.Writer, errMultiNested)
	}
	ctx.Session.InMulti = true
	ctx.Session.MultiQueue = nil
	return writeOK(ctx.Writer)
}

func cmdDiscard(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("DISCARD"))
	}
	if ctx.Session == nil || !ctx.Session.InMulti {
		return writeErr(ctx.Writer, errDiscardNoMulti)
	}
	ctx.Session.InMulti = false
	ctx.Session.MultiQueue = nil
	return writeOK(ctx.Writer)
}

func cmdWatch(ctx *CommandContext) error {
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("WATCH"))
	}
	if ctx.Session == nil {
		return writeOK(ctx.Writer)
	}
	for i := 1; i < len(ctx.Args); i++ {
		k := bytesToStr(ctx.Args[i])
		ctx.Session.WatchedKeys[k] = ctx.Store.WatchVersion(k)
	}
	return writeOK(ctx.Writer)
}

func cmdUnwatch(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("UNWATCH"))
	}
	if ctx.Session != nil {
		ctx.Session.WatchedKeys = make(map[string]uint64)
	}
	return writeOK(ctx.Writer)
}

func cmdExec(ctx *CommandContext) error {
	if len(ctx.Args) != 1 {
		return writeErr(ctx.Writer, argErr("EXEC"))
	}
	s := ctx.Session
	if s == nil || !s.InMulti {
		return writeErr(ctx.Writer, errExecNoMulti)
	}
	queue := s.MultiQueue
	s.InMulti = false
	s.MultiQueue = nil

	aborted := false
	for k, ver := range s.WatchedKeys {
		if ctx.Store.WatchVersion(k) != ver {
			aborted = true
			break
		}
	}
	if aborted {
		s.WatchedKeys = make(map[string]uint64)
		if err := ctx.Writer.WriteNull(); err != nil {
			return fmt.Errorf("exec write null: %w", err)
		}
		return ctx.Writer.Flush()
	}
	s.WatchedKeys = make(map[string]uint64)

	if ctx.Registry == nil {
		return fmt.Errorf("exec: registry not set on context")
	}

	elems := make([]resp.Value, 0, len(queue))
	var buf bytes.Buffer
	subCtx := *ctx
	for _, v := range queue {
		buf.Reset()
		subCtx.Writer = resp.NewWriter(&buf)
		if err := ctx.Registry.Dispatch(&subCtx, v); err != nil {
			return err
		}
		pr := resp.NewParser(bytes.NewReader(buf.Bytes()))
		val, err := pr.Parse()
		if err != nil {
			return fmt.Errorf("exec parse reply: %w", err)
		}
		elems = append(elems, val)
	}
	if err := ctx.Writer.WriteArray(elems); err != nil {
		return fmt.Errorf("exec write array: %w", err)
	}
	return ctx.Writer.Flush()
}
