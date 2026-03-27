// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Shared helpers for Redis command handlers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/peer"
	"github.com/supercache/supercache/internal/resp"
	"github.com/supercache/supercache/internal/store"
)

// Standard RESP error payloads (exact Redis-style text after leading '-').
const (
	errWrongType     = "WRONGTYPE Operation against a key holding the wrong kind of value"
	errNotInt        = "ERR value is not an integer or out of range"
	errIncrOverflow  = "ERR increment or decrement would overflow"
	errNoSuchKey     = "ERR no such key"
	errDBRange       = "ERR DB index is out of range"
	errOOM           = "OOM command not allowed when used memory > 'maxmemory'"
	errWrongPass     = "WRONGPASS invalid username-password pair or user is disabled"
	errAuthNoPass    = "ERR Client sent AUTH, but no password is set. Did you mean ACL SETUSER with >password?"
	errNoAuth        = "NOAUTH Authentication required"
	errExecNoMulti   = "ERR EXEC without MULTI"
	errMultiNested   = "ERR MULTI calls can not be nested"
	errDiscardNoMulti = "ERR DISCARD without MULTI"
	errSyntax        = "ERR syntax error"
)

func keyspaceHit(ctx *CommandContext, n int64) {
	if ctx == nil || ctx.Keyspace == nil || n <= 0 {
		return
	}
	ctx.Keyspace.RecordHit(n)
}

func keyspaceMiss(ctx *CommandContext, n int64) {
	if ctx == nil || ctx.Keyspace == nil || n <= 0 {
		return
	}
	ctx.Keyspace.RecordMiss(n)
}

// ReplicateListPush sends LPUSH or RPUSH replication.
func ReplicateListPush(rep peer.Replicator, key string, left bool, vals [][]byte) {
	if rep == nil {
		return
	}
	op := "RPUSH"
	if left {
		op = "LPUSH"
	}
	cp := make([][]byte, len(vals))
	for i, v := range vals {
		cp[i] = append([]byte(nil), v...)
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: op, Key: key, Members: cp})
}

// ReplicateHDel sends HDEL replication (field names in Members).
func ReplicateHDel(rep peer.Replicator, key string, fields []string) {
	if rep == nil {
		return
	}
	membs := make([][]byte, len(fields))
	for i, f := range fields {
		membs[i] = []byte(f)
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "HDEL", Key: key, Members: membs})
}

// ReplicateSAdd sends SADD replication.
func ReplicateSAdd(rep peer.Replicator, key string, members [][]byte) {
	if rep == nil {
		return
	}
	cp := make([][]byte, len(members))
	for i, m := range members {
		cp[i] = append([]byte(nil), m...)
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "SADD", Key: key, Members: cp})
}

// ReplicateSRem sends SREM replication.
func ReplicateSRem(rep peer.Replicator, key string, members [][]byte) {
	if rep == nil {
		return
	}
	cp := make([][]byte, len(members))
	for i, m := range members {
		cp[i] = append([]byte(nil), m...)
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "SREM", Key: key, Members: cp})
}

func writeErr(w *resp.Writer, msg string) error {
	if err := w.WriteError(msg); err != nil {
		return fmt.Errorf("write error: %w", err)
	}
	return w.Flush()
}

func writeSimple(w *resp.Writer, s string) error {
	if err := w.WriteSimpleString(s); err != nil {
		return fmt.Errorf("write simple: %w", err)
	}
	return w.Flush()
}

func writeOK(w *resp.Writer) error {
	return writeSimple(w, "OK")
}

// ReplyOK writes a RESP simple-string OK reply (+OK) and flushes.
func ReplyOK(w *resp.Writer) error {
	return writeOK(w)
}

// WriteNoAuth writes the standard NOAUTH error and flushes.
func WriteNoAuth(w *resp.Writer) error {
	return writeErr(w, errNoAuth)
}

func writeBulkNil(w *resp.Writer) error {
	if err := w.WriteNullBulkString(); err != nil {
		return fmt.Errorf("write null bulk: %w", err)
	}
	return w.Flush()
}

func writeBulk(w *resp.Writer, b []byte) error {
	if b == nil {
		return writeBulkNil(w)
	}
	if err := w.WriteBulkString(b); err != nil {
		return fmt.Errorf("write bulk: %w", err)
	}
	return w.Flush()
}

func writeInt(w *resp.Writer, n int64) error {
	if err := w.WriteInteger(n); err != nil {
		return fmt.Errorf("write int: %w", err)
	}
	return w.Flush()
}

func argErr(cmd string) string {
	return fmt.Sprintf("ERR wrong number of arguments for '%s' command", strings.ToLower(cmd))
}

func invalidExpireErr(cmd string) string {
	return fmt.Sprintf("ERR invalid expire time in '%s' command", strings.ToLower(cmd))
}

func bytesToStr(b []byte) string {
	if b == nil {
		return ""
	}
	return string(b)
}

func cmdName(args [][]byte) string {
	if len(args) == 0 {
		return ""
	}
	return strings.ToUpper(bytesToStr(args[0]))
}

// CheckArity returns an error if arity does not match Meta.
func CheckArity(meta CommandMeta, args [][]byte) error {
	n := len(args)
	if meta.Arity >= 0 {
		if n != meta.Arity {
			return errors.New(argErr(cmdName(args)))
		}
		return nil
	}
	need := -meta.Arity
	if n < need {
		return errors.New(argErr(cmdName(args)))
	}
	return nil
}

func isWrongType(err error) bool {
	return errors.Is(err, store.ErrWrongType)
}

// ReplicateSet sends SET replication after a successful local write.
func ReplicateSet(rep peer.Replicator, key string, val []byte, expiresAt time.Time) {
	if rep == nil {
		return
	}
	var ttlms int64
	if !expiresAt.IsZero() {
		ttlms = time.Until(expiresAt).Milliseconds()
		if ttlms < 0 {
			ttlms = 0
		}
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "SET", Key: key, Value: val, TTLms: ttlms})
}

// ReplicateDel sends DEL replication.
func ReplicateDel(rep peer.Replicator, key string) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "DEL", Key: key})
}

// ReplicateHash sends HSET replication.
func ReplicateHash(rep peer.Replicator, key string, fields map[string][]byte) {
	if rep == nil {
		return
	}
	cp := make(map[string][]byte, len(fields))
	for k, v := range fields {
		cp[k] = append([]byte(nil), v...)
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "HSET", Key: key, Fields: cp})
}

// ReplicateExpire sends EXPIRE replication (seconds).
func ReplicateExpire(rep peer.Replicator, key string, sec int64) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "EXPIRE", Key: key, Cnt: sec})
}

// ReplicateExpireAtUnix sends EXPIREAT replication (unix seconds).
func ReplicateExpireAtUnix(rep peer.Replicator, key string, unixSec int64) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "EXPIREAT", Key: key, Cnt: unixSec})
}

// ReplicatePExpire sends PEXPIRE replication (relative milliseconds).
func ReplicatePExpire(rep peer.Replicator, key string, ms int64) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "PEXPIRE", Key: key, Cnt: ms})
}

// ReplicatePExpireAtUnixMs sends PEXPIREAT replication (absolute unix milliseconds).
func ReplicatePExpireAtUnixMs(rep peer.Replicator, key string, unixMs int64) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "PEXPIREAT", Key: key, TTLms: unixMs})
}

// ReplicatePersist sends PERSIST replication.
func ReplicatePersist(rep peer.Replicator, key string) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "PERSIST", Key: key})
}

// ReplicateRename sends RENAME replication.
func ReplicateRename(rep peer.Replicator, src, dst string) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "RENAME", Key: src, Dest: dst})
}

// ReplicateLPop sends LPOP replication (number of elements removed from the left).
func ReplicateLPop(rep peer.Replicator, key string, n int) {
	if rep == nil || n < 1 {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "LPOP", Key: key, Cnt: int64(n)})
}

// ReplicateRPop sends RPOP replication (number of elements removed from the right).
func ReplicateRPop(rep peer.Replicator, key string, n int) {
	if rep == nil || n < 1 {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "RPOP", Key: key, Cnt: int64(n)})
}

// ReplicateLRem sends LREM replication.
func ReplicateLRem(rep peer.Replicator, key string, count int, elem []byte) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "LREM", Key: key, Cnt: int64(count), Value: append([]byte(nil), elem...)})
}

// ReplicateLSet sends LSET replication.
func ReplicateLSet(rep peer.Replicator, key string, index int64, val []byte) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{Op: "LSET", Key: key, Cnt: index, Value: append([]byte(nil), val...)})
}

// ReplicateLTrim sends LTRIM replication.
func ReplicateLTrim(rep peer.Replicator, key string, start, stop int64) {
	if rep == nil {
		return
	}
	_ = rep.Replicate(peer.ReplicatePayload{
		Op:  "LTRIM",
		Key: key,
		Members: [][]byte{
			[]byte(strconv.FormatInt(start, 10)),
			[]byte(strconv.FormatInt(stop, 10)),
		},
	})
}

// ReplicateLInsert sends LINSERT replication.
func ReplicateLInsert(rep peer.Replicator, key string, before bool, pivot, insert []byte) {
	if rep == nil {
		return
	}
	op := "LINSERT_AFTER"
	if before {
		op = "LINSERT_BEFORE"
	}
	_ = rep.Replicate(peer.ReplicatePayload{
		Op:  op,
		Key: key,
		Members: [][]byte{
			append([]byte(nil), pivot...),
			append([]byte(nil), insert...),
		},
	})
}

// ParseInt parses a decimal integer argument.
func ParseInt(b []byte) (int64, error) {
	return strconv.ParseInt(bytesToStr(b), 10, 64)
}

// ParseUint parses a decimal unsigned argument.
func ParseUint(b []byte) (uint64, error) {
	return strconv.ParseUint(bytesToStr(b), 10, 64)
}

// ParseFloat parses a float argument.
func ParseFloat(b []byte) (float64, error) {
	return strconv.ParseFloat(bytesToStr(b), 64)
}

// ValueToArgs converts a RESP array value to [][]byte arguments for command dispatch.
func ValueToArgs(v resp.Value) ([][]byte, error) {
	if v.Type != '*' || v.IsNull {
		return nil, fmt.Errorf("expected array")
	}
	out := make([][]byte, len(v.Array))
	for i, el := range v.Array {
		switch el.Type {
		case '$':
			if el.IsNull {
				out[i] = nil
			} else {
				out[i] = []byte(el.Str)
			}
		case '+', '-':
			out[i] = []byte(el.Str)
		case ':':
			out[i] = []byte(strconv.FormatInt(el.Integer, 10))
		default:
			return nil, fmt.Errorf("unsupported nested type in command array")
		}
	}
	return out, nil
}
