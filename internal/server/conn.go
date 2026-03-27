// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Per-connection RESP dispatch (AUTH, MULTI, subscribe mode).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/client"
	"github.com/supercache/supercache/internal/commands"
	"github.com/supercache/supercache/internal/resp"
)

const errWatchInsideMulti = "ERR WATCH inside MULTI is not allowed"

var subscribeAllowed = map[string]struct{}{
	"SUBSCRIBE":    {},
	"PSUBSCRIBE":   {},
	"UNSUBSCRIBE":  {},
	"PUNSUBSCRIBE": {},
	"PING":         {},
	"QUIT":         {},
}

func (s *Server) serveConn(ctx context.Context, c net.Conn) {
	s.stats.clientConnected()
	defer s.stats.clientDisconnected()
	defer gracefulCloseConn(c)

	pr := resp.NewParser(c)
	wr := resp.NewWriter(c)
	sid := s.sessionSeq.Add(1)
	sess := client.NewSession(sid, c, pr, wr)

	cmdCtx := &commands.CommandContext{
		Store:    s.store,
		Session:  sess,
		Writer:   wr,
		Peer:     s.peer,
		Config:   s.config(),
		Info:     s.stats,
		Keyspace: s.stats,
		PubSub:   s.pubsub,
		Registry: s.registry,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		v, err := pr.Parse()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			return
		}
		if !s.clientReady.Load() {
			_ = wr.WriteError("LOADING Super-Cache is loading the dataset in memory")
			_ = wr.Flush()
			continue
		}
		if err := s.dispatch(cmdCtx, v); err != nil {
			if errors.Is(err, client.ErrQuit) {
				return
			}
			return
		}
		sess.LastCmdAt = time.Now()
		sess.CmdCount++
		s.stats.commandExecuted()
		if s.shuttingDown.Load() {
			_ = wr.WriteError("LOADING server is shutting down")
			_ = wr.Flush()
			return
		}
	}
}

func gracefulCloseConn(c net.Conn) {
	if c == nil {
		return
	}
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		_ = c.Close()
		return
	}
	// Close write side first and briefly drain unread inbound data to avoid TCP RST
	// on close when clients have already sent the next command.
	_ = tcp.CloseWrite()
	_ = tcp.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 512)
	for {
		if _, err := tcp.Read(buf); err != nil {
			break
		}
	}
	_ = tcp.Close()
}

func (s *Server) dispatch(ctx *commands.CommandContext, v resp.Value) error {
	args, err := commands.ValueToArgs(v)
	if err != nil {
		return fmt.Errorf("value to args: %w", err)
	}
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}
	cmd := strings.ToUpper(string(args[0]))

	if cmd == "QUIT" {
		if err := commands.ReplyOK(ctx.Writer); err != nil {
			return fmt.Errorf("quit ok: %w", err)
		}
		return client.ErrQuit
	}

	if s.needAuth(ctx.Session) && cmd != "AUTH" {
		return commands.WriteNoAuth(ctx.Writer)
	}

	if ctx.Session.InMulti {
		switch cmd {
		case "WATCH":
			return writeErrStr(ctx.Writer, errWatchInsideMulti)
		case "UNWATCH":
			return s.registry.Dispatch(ctx, v)
		case "MULTI":
			return s.registry.Dispatch(ctx, v)
		case "EXEC", "DISCARD":
			return s.registry.Dispatch(ctx, v)
		default:
			ctx.Session.MultiQueue = append(ctx.Session.MultiQueue, v)
			return writeQueued(ctx.Writer)
		}
	}

	if ctx.Session.InSubscribeMode() {
		if _, ok := subscribeAllowed[cmd]; !ok {
			return writeErrStr(ctx.Writer, "ERR only (P)SUBSCRIBE / (P)UNSUBSCRIBE / PING / QUIT are allowed in this context")
		}
	}

	return s.registry.Dispatch(ctx, v)
}

func (s *Server) needAuth(sess *client.Session) bool {
	cfg := s.config()
	if cfg == nil || strings.TrimSpace(cfg.AuthPassword) == "" {
		return false
	}
	return !sess.Authenticated
}

func writeQueued(w *resp.Writer) error {
	if err := w.WriteSimpleString("QUEUED"); err != nil {
		return fmt.Errorf("write queued: %w", err)
	}
	return w.Flush()
}

func writeErrStr(w *resp.Writer, msg string) error {
	if err := w.WriteError(msg); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	return w.Flush()
}
