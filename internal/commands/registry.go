// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package commands registers Redis command handlers and dispatches RESP requests.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package commands

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/supercache/supercache/internal/resp"
)

// Registry maps command names (lowercase) to metadata and handlers.
type Registry struct {
	mu   sync.RWMutex
	cmds map[string]CommandMeta
}

// NewRegistry builds a registry with all built-in Redis commands registered.
func NewRegistry() *Registry {
	r := &Registry{cmds: make(map[string]CommandMeta)}
	registerStringCommands(r)
	registerHashCommands(r)
	registerListCommands(r)
	registerSetCommands(r)
	registerGenericCommands(r)
	registerServerCommands(r)
	registerPubSubCommands(r)
	registerTransactionCommands(r)
	return r
}

// Register adds or replaces a command.
func (r *Registry) Register(meta CommandMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds[strings.ToLower(meta.Name)] = meta
}

// Lookup returns metadata for a command name (case-insensitive).
func (r *Registry) Lookup(name string) (CommandMeta, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.cmds[strings.ToLower(name)]
	return m, ok
}

// Commands returns all registered commands sorted by name (for COMMAND / COMMAND INFO).
func (r *Registry) Commands() []CommandMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]CommandMeta, 0, len(r.cmds))
	for _, m := range r.cmds {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Dispatch runs the handler for a parsed RESP array value.
func (r *Registry) Dispatch(ctx *CommandContext, v resp.Value) error {
	args, err := ValueToArgs(v)
	if err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}
	if len(args) == 0 {
		return fmt.Errorf("empty command")
	}
	name := bytesToStr(args[0])
	meta, ok := r.Lookup(name)
	if !ok {
		return writeErr(ctx.Writer, fmt.Sprintf("ERR unknown command `%s`, with args beginning with: ", strings.ToLower(name)))
	}
	if meta.Arity != 0 {
		if err := CheckArity(meta, args); err != nil {
			return writeErr(ctx.Writer, err.Error())
		}
	}
	ctx.Args = args
	return meta.Handler(ctx)
}
