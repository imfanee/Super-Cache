// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// SubscriptionManager tracks Redis pub/sub channel and pattern subscribers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package client

import (
	"path"
	"sync"
)

// SubscriptionManager holds node-local pub/sub subscribers (not replicated in v1.0).
type SubscriptionManager struct {
	mu    sync.RWMutex
	chans map[string][]*Session
	psubs map[string][]*Session
}

// NewSubscriptionManager creates an empty subscription registry.
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		chans: make(map[string][]*Session),
		psubs: make(map[string][]*Session),
	}
}

// Subscribe adds a client session to an exact channel name.
func (m *SubscriptionManager) Subscribe(ch string, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chans[ch] = append(m.chans[ch], s)
}

// Unsubscribe removes a session from a channel.
func (m *SubscriptionManager) Unsubscribe(ch string, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chans[ch] = removeSession(m.chans[ch], s)
	if len(m.chans[ch]) == 0 {
		delete(m.chans, ch)
	}
}

// PSubscribe registers a pattern subscription.
func (m *SubscriptionManager) PSubscribe(pat string, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.psubs[pat] = append(m.psubs[pat], s)
}

// PUnsubscribe removes a pattern subscription.
func (m *SubscriptionManager) PUnsubscribe(pat string, s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.psubs[pat] = removeSession(m.psubs[pat], s)
	if len(m.psubs[pat]) == 0 {
		delete(m.psubs, pat)
	}
}

// Publish delivers a message to subscribers; returns the number of clients that received the payload.
func (m *SubscriptionManager) Publish(channel string, message []byte) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[*Session]struct{})
	n := 0
	for _, s := range m.chans[channel] {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		n++
		_ = s.pushPubSubMessage(channel, message)
	}
	for pat, subs := range m.psubs {
		ok, err := path.Match(pat, channel)
		if err != nil || !ok {
			continue
		}
		for _, s := range subs {
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			n++
			_ = s.pushPMessage(pat, channel, message)
		}
	}
	return n
}

func removeSession(subs []*Session, s *Session) []*Session {
	out := subs[:0]
	for _, x := range subs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// RemoveSessionFromAll unsubscribes a session from every channel and pattern.
func (m *SubscriptionManager) RemoveSessionFromAll(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch, subs := range m.chans {
		m.chans[ch] = removeSession(subs, s)
		if len(m.chans[ch]) == 0 {
			delete(m.chans, ch)
		}
	}
	for pat, subs := range m.psubs {
		m.psubs[pat] = removeSession(subs, s)
		if len(m.psubs[pat]) == 0 {
			delete(m.psubs, pat)
		}
	}
}
