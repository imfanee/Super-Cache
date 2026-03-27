// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Apply replicated payloads to the local store.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/supercache/supercache/internal/store"
)

// ApplyReplicatePayload applies a single mutation from a peer (idempotent with local command path).
func ApplyReplicatePayload(st *store.Store, p ReplicatePayload) error {
	return applyReplicatePayload(st, p)
}

func applyWireRepl(st *store.Store, w wireRepl) error {
	p := ReplicatePayload{
		Op:      w.Op,
		Key:     w.Key,
		Value:   w.Value,
		TTLms:   w.TTLms,
		Cnt:     w.Cnt,
		Dest:    w.Dest,
		Fields:  w.Fields,
		Members: w.Members,
	}
	return applyReplicatePayload(st, p)
}

func applyReplicatePayload(st *store.Store, p ReplicatePayload) error {
	op := strings.ToUpper(strings.TrimSpace(p.Op))
	switch op {
	case "SET":
		var exp time.Time
		if p.TTLms > 0 {
			exp = time.Now().Add(time.Duration(p.TTLms) * time.Millisecond)
		}
		return st.Set(p.Key, p.Value, exp)
	case "DEL":
		st.Del([]string{p.Key})
		return nil
	case "HSET":
		if len(p.Fields) == 0 {
			return nil
		}
		_, err := st.HSet(p.Key, p.Fields)
		return err
	case "HDEL":
		fields := make([]string, len(p.Members))
		for i, m := range p.Members {
			fields[i] = string(m)
		}
		_, err := st.HDel(p.Key, fields)
		return err
	case "SADD":
		if len(p.Members) == 0 {
			return nil
		}
		_, err := st.SAdd(p.Key, p.Members)
		return err
	case "SREM":
		if len(p.Members) == 0 {
			return nil
		}
		_, err := st.SRem(p.Key, p.Members)
		return err
	case "LPUSH":
		if len(p.Members) == 0 {
			return nil
		}
		_, err := st.LPush(p.Key, p.Members)
		return err
	case "RPUSH":
		if len(p.Members) == 0 {
			return nil
		}
		_, err := st.RPush(p.Key, p.Members)
		return err
	case "FLUSHDB", "FLUSHALL":
		st.FlushDB()
		return nil
	case "EXPIRE":
		if p.Cnt <= 0 {
			return nil
		}
		_, err := st.Expire(p.Key, p.Cnt)
		return err
	case "PEXPIRE":
		if p.Cnt <= 0 {
			return nil
		}
		_, err := st.ExpireAt(p.Key, time.Now().Add(time.Duration(p.Cnt)*time.Millisecond))
		return err
	case "EXPIREAT":
		_, err := st.ExpireAt(p.Key, time.Unix(p.Cnt, 0))
		return err
	case "PEXPIREAT":
		_, err := st.ExpireAt(p.Key, time.UnixMilli(p.TTLms))
		return err
	case "PERSIST":
		_, err := st.Persist(p.Key)
		return err
	case "RENAME":
		return st.Rename(p.Key, p.Dest)
	case "LPOP":
		n := int(p.Cnt)
		if n < 1 {
			n = 1
		}
		_, err := st.LPop(p.Key, n)
		return err
	case "RPOP":
		n := int(p.Cnt)
		if n < 1 {
			n = 1
		}
		_, err := st.RPop(p.Key, n)
		return err
	case "LREM":
		_, err := st.LRem(p.Key, int(p.Cnt), p.Value)
		return err
	case "LSET":
		return st.LSet(p.Key, p.Cnt, p.Value)
	case "LTRIM":
		if len(p.Members) < 2 {
			return nil
		}
		start, err1 := strconv.ParseInt(string(p.Members[0]), 10, 64)
		stop, err2 := strconv.ParseInt(string(p.Members[1]), 10, 64)
		if err1 != nil {
			return fmt.Errorf("ltrim start: %w", err1)
		}
		if err2 != nil {
			return fmt.Errorf("ltrim stop: %w", err2)
		}
		return st.LTrim(p.Key, start, stop)
	case "LINSERT_BEFORE":
		if len(p.Members) < 2 {
			return nil
		}
		_, err := st.LInsert(p.Key, true, p.Members[0], p.Members[1])
		return err
	case "LINSERT_AFTER":
		if len(p.Members) < 2 {
			return nil
		}
		_, err := st.LInsert(p.Key, false, p.Members[0], p.Members[1])
		return err
	default:
		return fmt.Errorf("unknown replicate op %q", op)
	}
}
