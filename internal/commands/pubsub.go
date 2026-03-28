// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Pub/sub command handlers (SUBSCRIBE, PUBLISH, ...).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"fmt"

	"github.com/supercache/supercache/internal/client"
	"github.com/supercache/supercache/internal/resp"
)

func registerPubSubCommands(r *Registry) {
	reg := func(name string, arity int, flags int, h CmdFunc) {
		r.Register(CommandMeta{Name: name, Arity: arity, Flags: flags, Handler: h})
	}
	reg("SUBSCRIBE", -2, FlagRead, cmdSubscribe)
	reg("PSUBSCRIBE", -2, FlagRead, cmdPSubscribe)
	reg("UNSUBSCRIBE", 0, FlagRead, cmdUnsubscribe)
	reg("PUNSUBSCRIBE", 0, FlagRead, cmdPUnsubscribe)
	reg("PUBLISH", 3, FlagWrite, cmdPublish)
}

func subscriptionCount(s *client.Session) int {
	if s == nil {
		return 0
	}
	return len(s.SubChannels) + len(s.PSubPatterns)
}

func writeSubscribeAck(w *resp.Writer, kind, channel string, n int64) error {
	arr := []resp.Value{
		{Type: '$', Str: kind},
		{Type: '$', Str: channel},
		{Type: ':', Integer: n},
	}
	if err := w.WriteArray(arr); err != nil {
		return fmt.Errorf("subscribe ack: %w", err)
	}
	return w.Flush()
}

func cmdSubscribe(ctx *CommandContext) error {
	if ctx.PubSub == nil || ctx.Session == nil {
		return writeErr(ctx.Writer, errSyntax)
	}
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("SUBSCRIBE"))
	}
	s := ctx.Session
	for i := 1; i < len(ctx.Args); i++ {
		ch := bytesToStr(ctx.Args[i])
		s.SubChannels[ch] = true
		ctx.PubSub.Subscribe(ch, s)
		n := subscriptionCount(s)
		if err := writeSubscribeAck(ctx.Writer, "subscribe", ch, int64(n)); err != nil {
			return err
		}
	}
	return nil
}

func cmdPSubscribe(ctx *CommandContext) error {
	if ctx.PubSub == nil || ctx.Session == nil {
		return writeErr(ctx.Writer, errSyntax)
	}
	if len(ctx.Args) < 2 {
		return writeErr(ctx.Writer, argErr("PSUBSCRIBE"))
	}
	s := ctx.Session
	for i := 1; i < len(ctx.Args); i++ {
		pat := bytesToStr(ctx.Args[i])
		s.PSubPatterns[pat] = true
		ctx.PubSub.PSubscribe(pat, s)
		n := subscriptionCount(s)
		if err := writeSubscribeAck(ctx.Writer, "psubscribe", pat, int64(n)); err != nil {
			return err
		}
	}
	return nil
}

func cmdUnsubscribe(ctx *CommandContext) error {
	if ctx.PubSub == nil || ctx.Session == nil {
		return writeErr(ctx.Writer, errSyntax)
	}
	s := ctx.Session
	if len(ctx.Args) == 1 {
		for ch := range s.SubChannels {
			ctx.PubSub.Unsubscribe(ch, s)
			delete(s.SubChannels, ch)
			n := subscriptionCount(s)
			if err := writeSubscribeAck(ctx.Writer, "unsubscribe", ch, int64(n)); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 1; i < len(ctx.Args); i++ {
		ch := bytesToStr(ctx.Args[i])
		delete(s.SubChannels, ch)
		ctx.PubSub.Unsubscribe(ch, s)
		n := subscriptionCount(s)
		if err := writeSubscribeAck(ctx.Writer, "unsubscribe", ch, int64(n)); err != nil {
			return err
		}
	}
	return nil
}

func cmdPUnsubscribe(ctx *CommandContext) error {
	if ctx.PubSub == nil || ctx.Session == nil {
		return writeErr(ctx.Writer, errSyntax)
	}
	s := ctx.Session
	if len(ctx.Args) == 1 {
		for pat := range s.PSubPatterns {
			ctx.PubSub.PUnsubscribe(pat, s)
			delete(s.PSubPatterns, pat)
			n := subscriptionCount(s)
			if err := writeSubscribeAck(ctx.Writer, "punsubscribe", pat, int64(n)); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 1; i < len(ctx.Args); i++ {
		pat := bytesToStr(ctx.Args[i])
		delete(s.PSubPatterns, pat)
		ctx.PubSub.PUnsubscribe(pat, s)
		n := subscriptionCount(s)
		if err := writeSubscribeAck(ctx.Writer, "punsubscribe", pat, int64(n)); err != nil {
			return err
		}
	}
	return nil
}

func cmdPublish(ctx *CommandContext) error {
	if len(ctx.Args) != 3 {
		return writeErr(ctx.Writer, argErr("PUBLISH"))
	}
	if ctx.PubSub == nil {
		return writeInt(ctx.Writer, 0)
	}
	ch := bytesToStr(ctx.Args[1])
	msg := ctx.Args[2]
	n := ctx.PubSub.Publish(ch, msg)
	// Replicate PUBLISH to peers so subscribers on other nodes receive the message.
	ReplicatePublish(ctx.Peer, ch, msg)
	return writeInt(ctx.Writer, int64(n))
}
