/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package activity

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"emperror.dev/errors"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Bus is the transport-agnostic pub/sub surface that producers publish to and
// transports (or in-process UIs) subscribe from. All implementations MUST treat
// publish as non-blocking so a slow subscriber can never stall an agent run.
type Bus interface {
	// Publish assigns the event an id and timestamp (when unset), records it for
	// replay and fans it out to the session's current subscribers. It never
	// blocks on a slow subscriber.
	Publish(ctx context.Context, e Event)
	// Subscribe registers a subscriber for sessionID. When lastEventID is
	// non-empty, buffered events newer than it are replayed first, then live
	// events follow. The returned func unsubscribes and releases resources; it is
	// idempotent. The channel is closed on unsubscribe or Bus.Close.
	Subscribe(ctx context.Context, sessionID, lastEventID string) (<-chan Event, func())
	// Close tears down the Bus and closes every subscriber channel.
	Close() error
}

// SlowPolicy selects what happens when a subscriber channel is full on Publish.
type SlowPolicy int

const (
	// DropEvent discards the event for that slow subscriber but keeps it
	// subscribed. This is the default.
	DropEvent SlowPolicy = iota
	// DropSubscriber unsubscribes (and closes the channel of) a subscriber that
	// cannot keep up.
	DropSubscriber
)

// Config configures the in-memory Bus.
type Config struct {
	// BufferSize is the per-session ring buffer size used for Last-Event-ID
	// replay. 0 selects DefaultBufferSize.
	BufferSize int `validate:"gte=0"`
	// SubscriberQueueSize is the additional buffering of each subscriber channel
	// beyond the replay buffer. 0 selects DefaultSubscriberQueueSize.
	SubscriberQueueSize int `validate:"gte=0"`
	// SlowPolicy decides the behavior when a subscriber channel is full.
	SlowPolicy SlowPolicy `validate:"oneof=0 1"`
	// MaxSessions bounds the number of distinct sessions retained for replay.
	// When a new session would exceed it, the least-recently-active session that
	// currently has no subscribers is evicted (sessions with active subscribers
	// are never dropped). 0 selects DefaultMaxSessions. This prevents unbounded
	// memory growth in long-running processes that run many distinct sessions.
	MaxSessions int `validate:"gte=0"`
	// Clock supplies the timestamp for events whose Timestamp is zero. Defaults
	// to time.Now.
	Clock func() time.Time
}

// Default sizing for the in-memory Bus.
const (
	DefaultBufferSize          = 256
	DefaultSubscriberQueueSize = 64
	DefaultMaxSessions         = 4096
)

// memBus is the in-memory Bus implementation.
type memBus struct {
	bufferSize  int
	queueSize   int
	policy      SlowPolicy
	maxSessions int
	clock       func() time.Time

	counter atomic.Uint64

	mu       sync.RWMutex
	closed   bool
	sessions map[string]*sessionState
}

// sessionState holds the replay ring buffer and live subscribers of one session.
type sessionState struct {
	ring       []Event // bounded; oldest at front
	subs       map[int]*subscriber
	nextSubID  int       // monotonic per-session subscriber id allocator
	lastActive time.Time // last publish/subscribe time, for LRU eviction
}

type subscriber struct {
	ch     chan Event
	closed bool
}

// NewBus validates cfg and returns an in-memory Bus.
func NewBus(cfg Config) (Bus, error) {
	if err := validate.Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid activity.Config")
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = DefaultBufferSize
	}
	if cfg.SubscriberQueueSize == 0 {
		cfg.SubscriberQueueSize = DefaultSubscriberQueueSize
	}
	if cfg.MaxSessions == 0 {
		cfg.MaxSessions = DefaultMaxSessions
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &memBus{
		bufferSize:  cfg.BufferSize,
		queueSize:   cfg.SubscriberQueueSize,
		policy:      cfg.SlowPolicy,
		maxSessions: cfg.MaxSessions,
		clock:       clock,
		sessions:    make(map[string]*sessionState),
	}, nil
}

// nextID returns the next process-monotonic event id.
func (b *memBus) nextID() string {
	return fmt.Sprintf("evt_%d", b.counter.Add(1))
}

// parseID extracts the numeric ordinal of an event id. ok is false when the id is
// not in the "evt_<n>" form.
func parseID(id string) (uint64, bool) {
	s, ok := strings.CutPrefix(id, "evt_")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (b *memBus) Publish(_ context.Context, e Event) {
	if e.ID == "" {
		e.ID = b.nextID()
	}
	if e.Timestamp.IsZero() {
		e.Timestamp = b.clock()
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}

	st := b.getOrCreateSessionLocked(e.SessionID)
	st.lastActive = e.Timestamp

	// Record for replay (bounded ring buffer, drop oldest).
	st.ring = append(st.ring, e)
	if len(st.ring) > b.bufferSize {
		st.ring = st.ring[len(st.ring)-b.bufferSize:]
	}

	// Non-blocking fan-out.
	for id, sub := range st.subs {
		select {
		case sub.ch <- e:
		default:
			if b.policy == DropSubscriber {
				b.removeSubLocked(st, id)
			}
		}
	}
}

// getOrCreateSessionLocked returns the session state for id, creating it (and
// evicting an idle session if at capacity) when absent. Caller must hold b.mu.
func (b *memBus) getOrCreateSessionLocked(id string) *sessionState {
	if st := b.sessions[id]; st != nil {
		return st
	}
	if b.maxSessions > 0 && len(b.sessions) >= b.maxSessions {
		b.evictIdleSessionLocked()
	}
	st := &sessionState{subs: make(map[int]*subscriber)}
	b.sessions[id] = st
	return st
}

// evictIdleSessionLocked drops the least-recently-active session that has no
// subscribers, bounding memory growth. Sessions with active subscribers are
// never evicted, so a delivering stream is never severed. Caller must hold b.mu.
func (b *memBus) evictIdleSessionLocked() {
	var (
		victim string
		found  bool
		oldest time.Time
	)
	for key, st := range b.sessions {
		if len(st.subs) > 0 {
			continue
		}
		if !found || st.lastActive.Before(oldest) {
			victim, oldest, found = key, st.lastActive, true
		}
	}
	if found {
		delete(b.sessions, victim)
	}
}

func (b *memBus) Subscribe(ctx context.Context, sessionID, lastEventID string) (<-chan Event, func()) {
	ch := make(chan Event, b.bufferSize+b.queueSize)

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		close(ch)
		return ch, func() {}
	}

	st := b.getOrCreateSessionLocked(sessionID)
	st.lastActive = b.clock()

	// Replay buffered events newer than lastEventID. The channel capacity is at
	// least bufferSize, so this never blocks under the lock.
	if after, ok := parseID(lastEventID); ok {
		for _, e := range st.ring {
			if n, ok := parseID(e.ID); ok && n > after {
				ch <- e
			}
		}
	}

	// Allocate a monotonic per-session subscriber id (no linear probe).
	id := st.nextSubID
	st.nextSubID++
	st.subs[id] = &subscriber{ch: ch}
	b.mu.Unlock()

	// stop is closed by unsub so the ctx-cancellation goroutine terminates even
	// when ctx is never cancelled (avoids leaking the goroutine + its closure).
	stop := make(chan struct{})
	var once sync.Once
	unsub := func() {
		once.Do(func() {
			close(stop)
			b.mu.Lock()
			if s := b.sessions[sessionID]; s != nil {
				b.removeSubLocked(s, id)
				// The ring buffer is retained on purpose so a briefly
				// disconnected client can reconnect and replay via
				// Last-Event-ID; idle sessions are reaped by MaxSessions
				// eviction instead.
			}
			b.mu.Unlock()
		})
	}

	// Honor ctx cancellation for cleanup.
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				unsub()
			case <-stop:
			}
		}()
	}

	return ch, unsub
}

// removeSubLocked deletes and closes a subscriber. Caller must hold b.mu.
func (b *memBus) removeSubLocked(st *sessionState, id int) {
	sub, ok := st.subs[id]
	if !ok {
		return
	}
	if !sub.closed {
		sub.closed = true
		close(sub.ch)
	}
	delete(st.subs, id)
}

// HasSubscribers reports whether sessionID currently has at least one
// subscriber. It implements the SubscriberCounter optimization used by Handler.
func (b *memBus) HasSubscribers(sessionID string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	st := b.sessions[sessionID]
	return st != nil && len(st.subs) > 0
}

func (b *memBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, st := range b.sessions {
		for id := range st.subs {
			b.removeSubLocked(st, id)
		}
	}
	b.sessions = make(map[string]*sessionState)
	return nil
}
