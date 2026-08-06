package activity

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func mustBus(t *testing.T, cfg Config) Bus {
	t.Helper()
	b, err := NewBus(cfg)
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	return b
}

func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case e, ok := <-ch:
		return e, ok
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}, false
	}
}

func TestBusPublishSubscribe(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()

	ch, unsub := b.Subscribe(context.Background(), "s1", "")
	defer unsub()

	b.Publish(context.Background(), Event{SessionID: "s1", Type: TypeTextDelta, Data: TextDelta{Delta: "hi"}})

	e, ok := recv(t, ch)
	if !ok {
		t.Fatal("channel closed")
	}
	if e.Type != TypeTextDelta || e.ID == "" {
		t.Fatalf("unexpected event: %+v", e)
	}
}

func TestBusSessionIsolation(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()

	ch1, u1 := b.Subscribe(context.Background(), "s1", "")
	defer u1()
	ch2, u2 := b.Subscribe(context.Background(), "s2", "")
	defer u2()

	b.Publish(context.Background(), Event{SessionID: "s1", Type: TypeTextStarted})

	if _, ok := recv(t, ch1); !ok {
		t.Fatal("s1 should receive")
	}
	select {
	case e := <-ch2:
		t.Fatalf("s2 must not receive s1 event: %+v", e)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBusReplay(t *testing.T) {
	b := mustBus(t, Config{BufferSize: 8})
	defer func() { _ = b.Close() }()

	// publish three before any subscriber.
	for i := 0; i < 3; i++ {
		b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta, Data: TextDelta{Delta: fmt.Sprint(i)}})
	}

	// subscribe replaying after the first event.
	ch, unsub := b.Subscribe(context.Background(), "s", "evt_1")
	defer unsub()

	// should replay evt_2 and evt_3.
	e, _ := recv(t, ch)
	if e.ID != "evt_2" {
		t.Fatalf("want evt_2, got %s", e.ID)
	}
	e, _ = recv(t, ch)
	if e.ID != "evt_3" {
		t.Fatalf("want evt_3, got %s", e.ID)
	}

	// live event follows replay.
	b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta})
	e, _ = recv(t, ch)
	if e.ID != "evt_4" {
		t.Fatalf("want evt_4, got %s", e.ID)
	}
}

func TestBusReplayBounded(t *testing.T) {
	b := mustBus(t, Config{BufferSize: 2})
	defer func() { _ = b.Close() }()

	for i := 0; i < 5; i++ {
		b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta})
	}
	// only last 2 (evt_4, evt_5) retained.
	ch, unsub := b.Subscribe(context.Background(), "s", "evt_0")
	defer unsub()

	e, _ := recv(t, ch)
	if e.ID != "evt_4" {
		t.Fatalf("want evt_4 as oldest retained, got %s", e.ID)
	}
}

func TestBusSlowSubscriberDropEvent(t *testing.T) {
	// queue of 1 + buffer 1 => capacity 2; publishing more must not block.
	b := mustBus(t, Config{BufferSize: 1, SubscriberQueueSize: 1, SlowPolicy: DropEvent})
	defer func() { _ = b.Close() }()

	ch, unsub := b.Subscribe(context.Background(), "s", "")
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}
	_ = ch
}

func TestBusSlowSubscriberDropSubscriber(t *testing.T) {
	b := mustBus(t, Config{BufferSize: 1, SubscriberQueueSize: 1, SlowPolicy: DropSubscriber})
	defer func() { _ = b.Close() }()

	ch, unsub := b.Subscribe(context.Background(), "s", "")
	defer unsub()

	for i := 0; i < 100; i++ {
		b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta})
	}
	// drain until channel is closed (subscriber dropped).
	closed := false
	for i := 0; i < 200; i++ {
		_, ok := <-ch
		if !ok {
			closed = true
			break
		}
	}
	if !closed {
		t.Fatal("slow subscriber channel should be closed")
	}
}

func TestBusUnsubscribe(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()

	ch, unsub := b.Subscribe(context.Background(), "s", "")
	unsub()
	unsub() // idempotent

	if _, ok := <-ch; ok {
		t.Fatal("channel should be closed after unsubscribe")
	}
}

func TestBusContextCancelUnsubscribes(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := b.Subscribe(ctx, "s", "")
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("context cancel did not unsubscribe")
	}
}

func TestBusHasSubscribers(t *testing.T) {
	b := mustBus(t, Config{})
	defer func() { _ = b.Close() }()

	sc := b.(SubscriberCounter)
	if sc.HasSubscribers("s") {
		t.Fatal("no subscribers yet")
	}
	_, unsub := b.Subscribe(context.Background(), "s", "")
	if !sc.HasSubscribers("s") {
		t.Fatal("expected a subscriber")
	}
	unsub()
	if sc.HasSubscribers("s") {
		t.Fatal("subscriber should be gone")
	}
}

func TestBusConcurrentPublish(t *testing.T) {
	b := mustBus(t, Config{BufferSize: 1024, SubscriberQueueSize: 1024})
	defer func() { _ = b.Close() }()

	ch, unsub := b.Subscribe(context.Background(), "s", "")
	defer unsub()

	var wg sync.WaitGroup
	const writers, perWriter = 8, 50
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextDelta})
			}
		}()
	}

	got := 0
	collected := make(chan struct{})
	go func() {
		for range ch {
			got++
			if got == writers*perWriter {
				close(collected)
				return
			}
		}
	}()

	wg.Wait()
	select {
	case <-collected:
	case <-time.After(2 * time.Second):
		t.Fatalf("got %d of %d events", got, writers*perWriter)
	}
}

func TestBusClosedNoPublishPanic(t *testing.T) {
	b := mustBus(t, Config{})
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// publish after close is a no-op, must not panic.
	b.Publish(context.Background(), Event{SessionID: "s", Type: TypeTextStarted})
	// subscribe after close returns a closed channel.
	ch, _ := b.Subscribe(context.Background(), "s", "")
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel after Close")
	}
}

func TestBusMaxSessionsEvictsIdle(t *testing.T) {
	b := mustBus(t, Config{MaxSessions: 2})
	defer func() { _ = b.Close() }()
	mb := b.(*memBus)

	// publish to two idle sessions, then a third triggers eviction of the LRU.
	b.Publish(context.Background(), Event{SessionID: "a", Type: TypeTextStarted})
	b.Publish(context.Background(), Event{SessionID: "b", Type: TypeTextStarted})
	b.Publish(context.Background(), Event{SessionID: "c", Type: TypeTextStarted})

	mb.mu.RLock()
	n := len(mb.sessions)
	_, hasA := mb.sessions["a"]
	mb.mu.RUnlock()
	if n != 2 {
		t.Fatalf("want 2 sessions retained, got %d", n)
	}
	if hasA {
		t.Fatal("least-recently-active session 'a' should have been evicted")
	}
}

func TestBusMaxSessionsNeverEvictsSubscribed(t *testing.T) {
	b := mustBus(t, Config{MaxSessions: 1})
	defer func() { _ = b.Close() }()
	mb := b.(*memBus)

	// 'a' has an active subscriber; it must survive eviction pressure.
	_, unsub := b.Subscribe(context.Background(), "a", "")
	defer unsub()
	b.Publish(context.Background(), Event{SessionID: "b", Type: TypeTextStarted})

	mb.mu.RLock()
	_, hasA := mb.sessions["a"]
	mb.mu.RUnlock()
	if !hasA {
		t.Fatal("session with an active subscriber must not be evicted")
	}
}
