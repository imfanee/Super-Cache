// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Per-client session state for Redis connections.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package client

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/supercache/supercache/internal/resp"
)

// Session holds per-connection state for a single Redis client.
type Session struct {
	// ID is a monotonically assigned connection identifier.
	ID int64
	// Conn is the underlying TCP connection.
	Conn net.Conn
	// Parser decodes RESP from Conn.
	Parser *resp.Parser
	// Writer encodes RESP to Conn.
	Writer *resp.Writer
	// Authenticated is true after successful AUTH when a password is configured.
	Authenticated bool
	// Name is the value set by CLIENT SETNAME.
	Name string
	// DB is the logical database index (v1.0 supports 0 only).
	DB int
	// MultiQueue holds queued RESP values when inside MULTI.
	MultiQueue []resp.Value
	// WatchedKeys maps keys to store watch versions at WATCH time.
	WatchedKeys map[string]uint64
	// InMulti is true between MULTI and EXEC/DISCARD.
	InMulti bool
	// SubChannels tracks exact channel subscriptions when in subscribe mode.
	SubChannels map[string]bool
	// PSubPatterns tracks pattern subscriptions.
	PSubPatterns map[string]bool
	// CreatedAt is when the session was opened.
	CreatedAt time.Time
	// LastCmdAt is updated after each command.
	LastCmdAt time.Time
	// CmdCount is the number of commands executed on this session.
	CmdCount int64
	// AuthAttempts tracks AUTH attempts for rate limiting.
	AuthAttempts int
	// AuthWindowStart is the start of the current rate-limit window.
	AuthWindowStart time.Time

	outMu sync.Mutex
}

// Lock serializes writes to the RESP writer, protecting against concurrent pub/sub pushes.
func (s *Session) Lock()   { s.outMu.Lock() }
func (s *Session) Unlock() { s.outMu.Unlock() }

// NewSession constructs a session with empty subscription maps.
func NewSession(id int64, c net.Conn, p *resp.Parser, w *resp.Writer) *Session {
	return &Session{
		ID:           id,
		Conn:         c,
		Parser:       p,
		Writer:       w,
		WatchedKeys:  make(map[string]uint64),
		SubChannels:  make(map[string]bool),
		PSubPatterns: make(map[string]bool),
		CreatedAt:    time.Now(),
		LastCmdAt:    time.Now(),
	}
}

// pushPubSubMessage writes a Redis pub/sub "message" push (three bulk strings).
func (s *Session) pushPubSubMessage(channel string, message []byte) error {
	if s.Writer == nil {
		return nil
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	v := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "message"},
		{Type: '$', Str: channel},
		{Type: '$', Str: string(message)},
	}}
	if err := s.Writer.WriteValue(v); err != nil {
		return fmt.Errorf("write pubsub: %w", err)
	}
	return s.Writer.Flush()
}

// pushPMessage writes a Redis "pmessage" push (four bulk strings).
func (s *Session) pushPMessage(pattern, channel string, message []byte) error {
	if s.Writer == nil {
		return nil
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	v := resp.Value{Type: '*', Array: []resp.Value{
		{Type: '$', Str: "pmessage"},
		{Type: '$', Str: pattern},
		{Type: '$', Str: channel},
		{Type: '$', Str: string(message)},
	}}
	if err := s.Writer.WriteValue(v); err != nil {
		return fmt.Errorf("write pmessage: %w", err)
	}
	return s.Writer.Flush()
}

// InSubscribeMode reports whether the client may only run subscribe-mode commands.
func (s *Session) InSubscribeMode() bool {
	return len(s.SubChannels) > 0 || len(s.PSubPatterns) > 0
}
