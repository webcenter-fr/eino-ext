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

// Package session provides the generic, cross-request lifecycle for persisting a
// conversation history on top of a memory.Memory store.
//
// It encapsulates:
//   - per-session locking (one logical lock per "<userId>:<id>") with ref-counted
//     cleanup so the lock map never leaks entries;
//   - the turn lifecycle BeginTurn -> (Condense) -> Window -> CommitAssistant;
//   - optional non-destructive condensation (anchored summary) driven by a token
//     threshold, reusing an injected Summarizer.
//
// The package is intentionally policy-free: filtering of agent events, SSE wiring
// and the choice of Summarizer implementation are injected by the caller. In
// particular the Summarizer interface is structurally compatible with
// contextopt.Summarizer, so a single contextopt.NewModelSummarizer(...) instance
// can be shared between the intra-run optimization middleware and this
// cross-request condenser without creating an import dependency.
package session

import (
	"context"
	"fmt"
	"sync"

	"emperror.dev/errors"
	"github.com/cloudwego/eino/schema"
	"github.com/go-playground/validator/v10"

	"github.com/webcenter-fr/eino-ext/components/memory"
)

// Summarizer produces an anchored summary of a conversation history.
// previousSummary is the text of the last summary (empty when none), allowing
// incremental updates.
//
// The signature is intentionally identical to
// contextopt.Summarizer.Summarize so that an instance built with
// contextopt.NewModelSummarizer satisfies this interface by structural typing
// (no import of the contextopt package is required here).
type Summarizer interface {
	Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
}

// Config configures a SessionManager.
type Config struct {
	// Memory is the underlying cross-request store. Required.
	Memory memory.Memory `validate:"required"`

	// Summarizer, when non-nil, enables condensation: Condense generates an
	// anchored summary and appends it to the store. When nil, Condense is a no-op.
	Summarizer Summarizer

	// CondenseThreshold is the token count of the current window at (or above)
	// which Condense triggers a summarization. <= 0 disables condensation.
	CondenseThreshold int `validate:"gte=0"`

	// WindowBudget is the default token budget passed to GetWindow when a turn
	// requests its window (and the budget used to evaluate condensation). 0 means
	// "use the store's own default" (e.g. FileMemoryConfig.MaxWindowTokens).
	WindowBudget int `validate:"gte=0"`

	// TokenCounter estimates the token count of a window to decide when to
	// condense. Defaults to memory.DefaultTokenCounter.
	//
	// To guarantee a turn never pays the LLM summarization cost twice, inject the
	// SAME counter here, in the memory store, and in the intra-run optimization
	// middleware (see the plan's anti-double-cost invariant).
	TokenCounter memory.TokenCounter
}

// SessionManager owns the per-session locking and turn lifecycle on top of a
// memory.Memory store.
type SessionManager struct {
	mem          memory.Memory
	summarizer   Summarizer
	threshold    int
	windowBudget int
	tokenCounter memory.TokenCounter

	mu    sync.Mutex
	locks map[string]*refLock
}

// refLock is a mutex with a reference count so the SessionManager can drop the
// map entry once no turn references it anymore (avoids the unbounded growth of a
// naive "one mutex per session id forever" map).
type refLock struct {
	mu   sync.Mutex
	refs int
}

// NewSessionManager validates cfg and returns a SessionManager.
func NewSessionManager(cfg Config) (*SessionManager, error) {
	if err := validator.New().Struct(&cfg); err != nil {
		return nil, errors.Wrap(err, "invalid session.Config")
	}
	tc := cfg.TokenCounter
	if tc == nil {
		tc = memory.DefaultTokenCounter
	}
	return &SessionManager{
		mem:          cfg.Memory,
		summarizer:   cfg.Summarizer,
		threshold:    cfg.CondenseThreshold,
		windowBudget: cfg.WindowBudget,
		tokenCounter: tc,
		locks:        make(map[string]*refLock),
	}, nil
}

// sessionKey builds the lock/identity key for a (userId, id) pair. Both parts are
// quoted so values containing the separator (e.g. a userId with ":") cannot
// collide into the same key (which would otherwise over-serialize unrelated
// sessions).
func sessionKey(userId, id string) string {
	return fmt.Sprintf("%q:%q", userId, id)
}

// lock acquires the logical lock for key, creating the entry (and bumping its
// ref count) if necessary.
func (sm *SessionManager) lock(key string) {
	sm.mu.Lock()
	rl, ok := sm.locks[key]
	if !ok {
		rl = &refLock{}
		sm.locks[key] = rl
	}
	rl.refs++
	sm.mu.Unlock()

	rl.mu.Lock()
}

// unlock releases the logical lock for key and drops the map entry once the last
// reference is gone. Safe to call only once per matching lock.
func (sm *SessionManager) unlock(key string) {
	sm.mu.Lock()
	rl, ok := sm.locks[key]
	sm.mu.Unlock()
	if !ok {
		return
	}

	rl.mu.Unlock()

	sm.mu.Lock()
	rl.refs--
	if rl.refs <= 0 {
		delete(sm.locks, key)
	}
	sm.mu.Unlock()
}

// activeLocks returns the number of currently tracked session locks. Intended for
// tests and diagnostics.
func (sm *SessionManager) activeLocks() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.locks)
}

// BeginTurn acquires the session lock and loads (or creates) the conversation.
// The user message is NOT persisted yet: it is held on the Turn and only written
// durably by CommitAssistant. This keeps it visible to Window/Condense during the
// turn while guaranteeing that an aborted turn (Discard) persists nothing — so a
// failed run never leaves a dangling user message, and a retry cannot duplicate it.
//
// The returned Turn MUST be released via CommitAssistant or Discard (typically
// `defer turn.Discard()`), otherwise the session stays locked.
func (sm *SessionManager) BeginTurn(userId, id string, userMsg *schema.Message) (*Turn, error) {
	key := sessionKey(userId, id)
	sm.lock(key)

	conv, err := sm.mem.GetConversation(userId, id, true)
	if err != nil {
		sm.unlock(key)
		return nil, errors.Wrap(err, "session: failed to get conversation")
	}

	return &Turn{sm: sm, key: key, conv: conv, pendingUser: userMsg}, nil
}

// ListConversations forwards to the underlying store.
func (sm *SessionManager) ListConversations(userId string) ([]string, error) {
	return sm.mem.ListConversations(userId)
}

// DeleteConversation deletes the conversation from the store. It briefly acquires
// the session lock to avoid racing an in-flight turn; the ref-counted cleanup
// then removes the now-unused lock entry, preventing a memory leak.
func (sm *SessionManager) DeleteConversation(userId, id string) error {
	key := sessionKey(userId, id)
	sm.lock(key)
	defer sm.unlock(key)

	if err := sm.mem.DeleteConversation(userId, id); err != nil {
		return errors.Wrap(err, "session: failed to delete conversation")
	}
	return nil
}

// Turn represents a single locked request/response cycle on one conversation.
//
// Lifecycle: BeginTurn -> [Condense] -> Window -> CommitAssistant (or Discard on
// failure). The handle is single-use and not safe for concurrent use.
type Turn struct {
	sm   *SessionManager
	key  string
	conv memory.Conversation

	// pendingUser is the user message for this turn, not yet persisted. It is
	// surfaced by Window/Condense and written durably by CommitAssistant (before
	// the assistant message). Discard drops it without persisting.
	pendingUser *schema.Message

	releaseOnce sync.Once
	committed   bool
}

// Conversation exposes the underlying conversation for advanced/read-only use.
// Note: the current turn's user message is pending and not part of the
// conversation's persisted messages until CommitAssistant.
func (t *Turn) Conversation() memory.Conversation {
	return t.conv
}

// Window returns the windowed history [last summary + recent messages] to feed to
// the model, with the current turn's (not-yet-persisted) user message appended.
// When budget <= 0, the SessionManager's configured WindowBudget is used (which
// itself may fall back to the store's default).
func (t *Turn) Window(budget int) []*schema.Message {
	if budget <= 0 {
		budget = t.sm.windowBudget
	}
	win := t.conv.GetWindow(budget)

	if t.pendingUser == nil {
		return win
	}
	out := make([]*schema.Message, 0, len(win)+1)
	out = append(out, win...)
	out = append(out, t.pendingUser)
	return out
}

// Condense generates and persists an anchored summary when the current window
// reaches the configured token threshold. It is a no-op (returns false) when no
// Summarizer is configured or the threshold is disabled (<= 0) or not reached.
//
// The produced summary is appended non-destructively (the full log is preserved);
// subsequent windows start from this new summary. Summarization reuses the
// injected Summarizer and the marker shared with contextopt
// (memory.SummaryMarkerKey), so persisted summaries interoperate natively with
// the intra-run optimization middleware.
func (t *Turn) Condense(ctx context.Context) (bool, error) {
	if t.sm.summarizer == nil || t.sm.threshold <= 0 {
		return false, nil
	}

	window := t.Window(0)
	if t.sm.tokenCount(window) < t.sm.threshold {
		return false, nil
	}

	text, err := t.sm.summarizer.Summarize(ctx, window, previousSummaryText(window))
	if err != nil {
		return false, errors.Wrap(err, "session: summarization failed")
	}

	t.conv.AppendSummary(memory.NewSummaryMessage(text))
	return true, nil
}

// CommitAssistant persists the pending user message (from BeginTurn) followed by
// the assistant message (when non-nil), then releases the session lock. It guards
// against a double commit; the lock release is idempotent.
func (t *Turn) CommitAssistant(msg *schema.Message) error {
	if t.committed {
		return errors.New("session: assistant already committed for this turn")
	}
	t.committed = true

	if t.pendingUser != nil {
		t.conv.Append(t.pendingUser)
		t.pendingUser = nil
	}
	if msg != nil {
		t.conv.Append(msg)
	}
	t.release()
	return nil
}

// Discard releases the session lock without persisting the pending user message or
// any assistant message. It is safe to call multiple times and after
// CommitAssistant (no-op), making it ideal for `defer turn.Discard()`.
func (t *Turn) Discard() {
	t.pendingUser = nil
	t.release()
}

func (t *Turn) release() {
	t.releaseOnce.Do(func() {
		t.sm.unlock(t.key)
	})
}

// tokenCount counts tokens of msgs via the injected TokenCounter, kept
// consistent with the memory store and the intra-run middleware so condensation
// decisions agree across layers.
func (sm *SessionManager) tokenCount(window []*schema.Message) int {
	return sm.tokenCounter(window)
}

// previousSummaryText returns the content of the last summary in window, or "".
func previousSummaryText(window []*schema.Message) string {
	for i := len(window) - 1; i >= 0; i-- {
		if memory.IsSummary(window[i]) {
			return window[i].Content
		}
	}
	return ""
}
