package sse

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

type subscriberCounter interface {
	HasSubscribers(sessionID string) bool
}

// startServer launches a Hertz server on a free port with the SSE handler.
func startServer(t *testing.T, bus activity.Bus) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := server.New(server.WithHostPorts(addr), server.WithExitWaitTime(50*time.Millisecond))
	h.GET("/events", NewHandler(Config{Bus: bus, HeartbeatInterval: 100 * time.Millisecond}))

	go h.Spin()
	t.Cleanup(func() { _ = h.Close() })

	// wait for the port to accept connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close()
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not start")
	return ""
}

func TestSSEStreamsEvents(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer func() { _ = bus.Close() }()
	addr := startServer(t, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/events?session=s", addr), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	// wait until the handler has subscribed, then publish.
	sc := bus.(subscriberCounter)
	deadline := time.Now().Add(2 * time.Second)
	for !sc.HasSubscribers("s") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	bus.Publish(context.Background(), activity.Event{SessionID: "s", Type: activity.TypeTextDelta, Data: activity.TextDelta{Delta: "hi"}})

	id, event, data := readFrame(t, resp.Body)
	if id == "" || event != string(activity.TypeTextDelta) {
		t.Fatalf("frame id=%q event=%q data=%q", id, event, data)
	}
	if !strings.Contains(data, `"delta":"hi"`) {
		t.Fatalf("unexpected data: %q", data)
	}
}

func TestSSEStreamsAgentInData(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer func() { _ = bus.Close() }()
	addr := startServer(t, bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/events?session=s", addr), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	sc := bus.(subscriberCounter)
	deadline := time.Now().Add(2 * time.Second)
	for !sc.HasSubscribers("s") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	bus.Publish(context.Background(), activity.Event{SessionID: "s", Agent: "supervisor", Type: activity.TypeTextDelta, Data: activity.TextDelta{Delta: "hi"}})

	_, _, data := readFrame(t, resp.Body)
	if !strings.Contains(data, `"agent":"supervisor"`) || !strings.Contains(data, `"delta":"hi"`) {
		t.Fatalf("data frame missing agent or delta: %q", data)
	}
}

func TestSSEReplayWithLastEventID(t *testing.T) {
	bus, err := activity.NewBus(activity.Config{})
	if err != nil {
		t.Fatalf("NewBus: %v", err)
	}
	defer func() { _ = bus.Close() }()

	// publish two events before any subscriber so they are buffered.
	bus.Publish(context.Background(), activity.Event{SessionID: "s", Type: activity.TypeTextDelta, Data: activity.TextDelta{Delta: "a"}})
	bus.Publish(context.Background(), activity.Event{SessionID: "s", Type: activity.TypeTextDelta, Data: activity.TextDelta{Delta: "b"}})

	addr := startServer(t, bus)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/events?session=s", addr), nil)
	req.Header.Set("Last-Event-ID", "evt_1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// should replay evt_2 only.
	id, _, data := readFrame(t, resp.Body)
	if id != "evt_2" || !strings.Contains(data, `"delta":"b"`) {
		t.Fatalf("replay frame id=%q data=%q", id, data)
	}
}

// readFrame reads one SSE event frame (until a blank line), skipping comments and
// ping heartbeats.
func readFrame(t *testing.T, r interface{ Read([]byte) (int, error) }) (id, event, data string) {
	t.Helper()
	br := bufio.NewReader(r)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			id, event, data = "", "", ""
			gotAny := false
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				line = strings.TrimRight(line, "\r\n")
				if line == "" {
					break // end of frame
				}
				gotAny = true
				switch {
				case strings.HasPrefix(line, ":"):
					// comment, ignore
				case strings.HasPrefix(line, "id:"):
					id = strings.TrimSpace(line[3:])
				case strings.HasPrefix(line, "event:"):
					event = strings.TrimSpace(line[6:])
				case strings.HasPrefix(line, "data:"):
					data += strings.TrimSpace(line[5:])
				}
			}
			if gotAny && event != "ping" {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out reading SSE frame")
	}
	return id, event, data
}
