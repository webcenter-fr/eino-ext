package session

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/webcenter-fr/eino-ext/components/memory"
	"github.com/webcenter-fr/eino-ext/components/memory/file"
)

func newManager(t *testing.T, cfg Config) *SessionManager {
	t.Helper()
	if cfg.Memory == nil {
		mem, err := file.NewFileMemory(file.FileMemoryConfig{Dir: t.TempDir()})
		if err != nil {
			t.Fatalf("NewFileMemory: %v", err)
		}
		cfg.Memory = mem
	}
	sm, err := NewSessionManager(cfg)
	if err != nil {
		t.Fatalf("NewSessionManager: %v", err)
	}
	return sm
}

func TestNewSessionManager_RequiresMemory(t *testing.T) {
	if _, err := NewSessionManager(Config{}); err == nil {
		t.Fatal("expected error when Memory is nil")
	}
}

func TestTurnLifecycle(t *testing.T) {
	sm := newManager(t, Config{})

	turn, err := sm.BeginTurn("u1", "c1", schema.UserMessage("hello"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	win := turn.Window(0)
	if len(win) != 1 || win[0].Content != "hello" {
		t.Fatalf("unexpected window after BeginTurn: %#v", win)
	}

	if err := turn.CommitAssistant(schema.AssistantMessage("hi there", nil)); err != nil {
		t.Fatalf("CommitAssistant: %v", err)
	}

	// Lock must be released and cleaned up.
	if got := sm.activeLocks(); got != 0 {
		t.Fatalf("expected 0 active locks after commit, got %d", got)
	}

	// Reload in a fresh turn: both messages must be persisted.
	turn2, err := sm.BeginTurn("u1", "c1", nil)
	if err != nil {
		t.Fatalf("BeginTurn 2: %v", err)
	}
	defer turn2.Discard()

	full := turn2.Conversation().GetFullMessages()
	if len(full) != 2 {
		t.Fatalf("expected 2 persisted messages, got %d", len(full))
	}
	if full[0].Content != "hello" || full[1].Content != "hi there" {
		t.Fatalf("unexpected persisted messages: %#v", full)
	}
}

func TestCommitAssistant_DoubleCommitGuard(t *testing.T) {
	sm := newManager(t, Config{})
	turn, err := sm.BeginTurn("u", "c", schema.UserMessage("q"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	if err := turn.CommitAssistant(schema.AssistantMessage("a", nil)); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	if err := turn.CommitAssistant(schema.AssistantMessage("b", nil)); err == nil {
		t.Fatal("expected error on second commit")
	}
	if got := sm.activeLocks(); got != 0 {
		t.Fatalf("expected 0 active locks, got %d", got)
	}
}

func TestDiscard_DoesNotPersistUserMessage(t *testing.T) {
	sm := newManager(t, Config{})

	// Aborted turn: BeginTurn then Discard must persist nothing.
	turn, err := sm.BeginTurn("u", "c", schema.UserMessage("dropped"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	if win := turn.Window(0); len(win) != 1 || win[0].Content != "dropped" {
		t.Fatalf("pending user message must be visible in window: %#v", win)
	}
	turn.Discard()

	turn2, err := sm.BeginTurn("u", "c", nil)
	if err != nil {
		t.Fatalf("BeginTurn 2: %v", err)
	}
	defer turn2.Discard()
	if full := turn2.Conversation().GetFullMessages(); len(full) != 0 {
		t.Fatalf("aborted turn must persist nothing, got %#v", full)
	}
}

func TestCommit_PersistsUserThenAssistant(t *testing.T) {
	sm := newManager(t, Config{})
	turn, _ := sm.BeginTurn("u", "c", schema.UserMessage("q"))
	if err := turn.CommitAssistant(schema.AssistantMessage("a", nil)); err != nil {
		t.Fatalf("CommitAssistant: %v", err)
	}

	turn2, _ := sm.BeginTurn("u", "c", nil)
	defer turn2.Discard()
	full := turn2.Conversation().GetFullMessages()
	if len(full) != 2 || full[0].Content != "q" || full[1].Content != "a" {
		t.Fatalf("expected [q a] persisted in order, got %#v", full)
	}
}

func TestDiscard_ReleasesLock(t *testing.T) {
	sm := newManager(t, Config{})
	turn, err := sm.BeginTurn("u", "c", schema.UserMessage("q"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}
	turn.Discard()
	turn.Discard() // idempotent
	if got := sm.activeLocks(); got != 0 {
		t.Fatalf("expected 0 active locks after discard, got %d", got)
	}
}

func TestBeginTurn_SerializesSameSession(t *testing.T) {
	sm := newManager(t, Config{})

	turn1, err := sm.BeginTurn("u", "c", schema.UserMessage("first"))
	if err != nil {
		t.Fatalf("BeginTurn: %v", err)
	}

	var secondAcquired int32
	done := make(chan struct{})
	go func() {
		turn2, err := sm.BeginTurn("u", "c", nil)
		if err == nil {
			atomic.StoreInt32(&secondAcquired, 1)
			turn2.Discard()
		}
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&secondAcquired) != 0 {
		t.Fatal("second BeginTurn acquired the lock while the first turn was active")
	}

	turn1.Discard()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second BeginTurn did not acquire the lock after release")
	}
	if atomic.LoadInt32(&secondAcquired) != 1 {
		t.Fatal("second BeginTurn never acquired the lock")
	}
}

func TestDifferentSessionsDoNotBlock(t *testing.T) {
	sm := newManager(t, Config{})

	turnA, err := sm.BeginTurn("u", "a", schema.UserMessage("x"))
	if err != nil {
		t.Fatalf("BeginTurn a: %v", err)
	}

	done := make(chan struct{})
	go func() {
		turnB, err := sm.BeginTurn("u", "b", schema.UserMessage("y"))
		if err == nil {
			turnB.Discard()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("different session was blocked by an unrelated active turn")
	}
	turnA.Discard()
}

// countingSummarizer records how many times it was invoked.
type countingSummarizer struct {
	calls    int32
	lastPrev string
}

func (s *countingSummarizer) Summarize(_ context.Context, _ []*schema.Message, previousSummary string) (string, error) {
	atomic.AddInt32(&s.calls, 1)
	s.lastPrev = previousSummary
	return "SUMMARY", nil
}

func TestCondense_NoOpWithoutSummarizer(t *testing.T) {
	sm := newManager(t, Config{CondenseThreshold: 1})
	turn, _ := sm.BeginTurn("u", "c", schema.UserMessage(strings.Repeat("a", 1000)))
	defer turn.Discard()

	condensed, err := turn.Condense(context.Background())
	if err != nil {
		t.Fatalf("Condense: %v", err)
	}
	if condensed {
		t.Fatal("expected no condensation without a summarizer")
	}
}

func TestCondense_BelowThreshold(t *testing.T) {
	s := &countingSummarizer{}
	sm := newManager(t, Config{Summarizer: s, CondenseThreshold: 100000})
	turn, _ := sm.BeginTurn("u", "c", schema.UserMessage("short"))
	defer turn.Discard()

	condensed, err := turn.Condense(context.Background())
	if err != nil {
		t.Fatalf("Condense: %v", err)
	}
	if condensed {
		t.Fatal("expected no condensation below threshold")
	}
	if atomic.LoadInt32(&s.calls) != 0 {
		t.Fatal("summarizer must not be called below threshold")
	}
}

func TestCondense_AtThresholdAppendsSummary(t *testing.T) {
	s := &countingSummarizer{}
	// ~4 chars/token: 1000 chars -> ~250 tokens. Threshold below that triggers.
	sm := newManager(t, Config{Summarizer: s, CondenseThreshold: 100})

	turn, _ := sm.BeginTurn("u", "c", schema.UserMessage(strings.Repeat("a", 1000)))

	condensed, err := turn.Condense(context.Background())
	if err != nil {
		t.Fatalf("Condense: %v", err)
	}
	if !condensed {
		t.Fatal("expected condensation at/above threshold")
	}
	if atomic.LoadInt32(&s.calls) != 1 {
		t.Fatalf("expected 1 summarizer call, got %d", s.calls)
	}

	// The window must now start at the persisted anchored summary.
	win := turn.Window(0)
	if len(win) == 0 || !memory.IsSummary(win[0]) {
		t.Fatalf("expected window to start with a summary, got %#v", win)
	}
	if win[0].Content != "SUMMARY" {
		t.Fatalf("unexpected summary content: %q", win[0].Content)
	}

	_ = turn.CommitAssistant(schema.AssistantMessage("ok", nil))

	// A subsequent condensation passes the previous summary text for incremental update.
	turn2, _ := sm.BeginTurn("u", "c", schema.UserMessage(strings.Repeat("b", 1000)))
	defer turn2.Discard()
	if _, err := turn2.Condense(context.Background()); err != nil {
		t.Fatalf("Condense 2: %v", err)
	}
	if s.lastPrev != "SUMMARY" {
		t.Fatalf("expected previousSummary to be passed, got %q", s.lastPrev)
	}
}

func TestDeleteConversation_CleansLock(t *testing.T) {
	sm := newManager(t, Config{})
	turn, _ := sm.BeginTurn("u", "c", schema.UserMessage("q"))
	_ = turn.CommitAssistant(schema.AssistantMessage("a", nil))

	if err := sm.DeleteConversation("u", "c"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if got := sm.activeLocks(); got != 0 {
		t.Fatalf("expected 0 active locks after delete, got %d", got)
	}
}

func TestConcurrentTurnsAcrossSessions(t *testing.T) {
	sm := newManager(t, Config{})

	const sessions = 20
	const rounds = 10
	var wg sync.WaitGroup
	wg.Add(sessions)
	for i := 0; i < sessions; i++ {
		go func(i int) {
			defer wg.Done()
			id := string(rune('a' + i))
			for r := 0; r < rounds; r++ {
				turn, err := sm.BeginTurn("u", id, schema.UserMessage("q"))
				if err != nil {
					t.Errorf("BeginTurn: %v", err)
					return
				}
				_ = turn.Window(0)
				if err := turn.CommitAssistant(schema.AssistantMessage("a", nil)); err != nil {
					t.Errorf("CommitAssistant: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	if got := sm.activeLocks(); got != 0 {
		t.Fatalf("expected all locks cleaned up, got %d", got)
	}
}
