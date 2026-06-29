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

// Package sse fans an activity.Bus out over HTTP Server-Sent Events using Hertz.
//
// It is the only package in the activity tree that depends on a web framework;
// the core (event/bus/handler) stays transport-agnostic. A UI opens
// EventSource('/events?session=ID') and reconnects transparently using the
// Last-Event-ID header, which this handler maps to the Bus replay buffer.
package sse

import (
	"context"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/hertz-contrib/sse"

	"github.com/webcenter-fr/eino-ext/components/observability/activity"
)

// Config configures the SSE handler.
type Config struct {
	// Bus is the activity Bus to read from. Required.
	Bus activity.Bus
	// SessionQuery is the query parameter holding the session id. Defaults to
	// "session".
	SessionQuery string
	// HeartbeatInterval is how often a keep-alive comment is written to hold the
	// connection open through proxies. <= 0 disables heartbeats. Defaults to 15s.
	HeartbeatInterval time.Duration
}

const defaultHeartbeat = 15 * time.Second

// NewHandler returns a Hertz handler streaming activity events for the session
// named by the configured query parameter. It honors the Last-Event-ID header
// for replay and cleans up its subscription on client disconnect.
func NewHandler(cfg Config) app.HandlerFunc {
	sessionQuery := cfg.SessionQuery
	if sessionQuery == "" {
		sessionQuery = "session"
	}
	heartbeat := cfg.HeartbeatInterval
	if heartbeat == 0 {
		heartbeat = defaultHeartbeat
	}
	bus := cfg.Bus

	return func(ctx context.Context, c *app.RequestContext) {
		session := c.Query(sessionQuery)
		lastID := sse.GetLastEventID(c)

		events, unsubscribe := bus.Subscribe(ctx, session, lastID)
		defer unsubscribe()

		stream := sse.NewStream(c)

		var ticker *time.Ticker
		var tickC <-chan time.Time
		if heartbeat > 0 {
			ticker = time.NewTicker(heartbeat)
			defer ticker.Stop()
			tickC = ticker.C
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-tickC:
				if err := stream.Publish(&sse.Event{Event: "ping", Data: []byte("keep-alive")}); err != nil {
					return
				}
			case e, ok := <-events:
				if !ok {
					return
				}
				data, err := activity.MarshalSSEData(e)
				if err != nil {
					continue
				}
				if err := stream.Publish(&sse.Event{
					ID:    e.ID,
					Event: string(e.Type),
					Data:  data,
				}); err != nil {
					return
				}
			}
		}
	}
}
