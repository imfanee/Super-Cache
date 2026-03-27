// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// SCAN cursor iteration over the keyspace.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"sort"
	"strings"
)

// Scan returns the next cursor and up to count key names matching match and optional wantType (Redis type name).
func (s *Store) Scan(cursor uint64, match string, count int, wantType string) (uint64, []string) {
	if match == "" {
		match = "*"
	}
	if count <= 0 {
		count = 10
	}
	all := s.Keys(match)
	sort.Strings(all)
	if wantType != "" {
		wantType = strings.ToLower(wantType)
		var filt []string
		for _, k := range all {
			if strings.EqualFold(s.Type(k), wantType) {
				filt = append(filt, k)
			}
		}
		all = filt
	}
	start := int(cursor)
	if start >= len(all) {
		return 0, nil
	}
	end := start + count
	if end > len(all) {
		end = len(all)
	}
	out := all[start:end]
	var next uint64
	if end < len(all) {
		next = uint64(end)
	}
	return next, out
}

// SampleKeysForDebug returns up to max keys with Redis type name and TTL in seconds (-1 none, -2 missing).
func (s *Store) SampleKeysForDebug(max int) []map[string]any {
	if max <= 0 {
		max = 10
	}
	keys := s.Keys("*")
	sort.Strings(keys)
	if len(keys) > max {
		keys = keys[:max]
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{
			"key":         k,
			"type":        s.Type(k),
			"ttl_seconds": s.TTL(k),
		})
	}
	return out
}
