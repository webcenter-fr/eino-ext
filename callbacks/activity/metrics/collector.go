// Package metrics provides an optional Prometheus collector for the activity
// package: it subscribes to a Bus session and, on step.ended events, records
// per-model/agent token and cost counters.
//
// It has no global side effects: metrics are registered on a
// prometheus.Registerer supplied by the caller, and the collector only
// consumes events for sessions the caller explicitly watches.
package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
)

// Collector records llm_tokens_total and llm_cost_usd_total from step.ended
// activity events. The label set is intentionally small (model, agent, and a
// token type for llm_tokens_total) to bound cardinality; provider is not
// included since the activity event model does not carry it.
//
// The model label is the raw gateway model name (activity.StepStarted.Model),
// not normalized through a catalog: callers deploying with highly dynamic
// model identifiers (e.g. per-request fine-tune ids) should normalize the
// name before it reaches StepStarted.Model, or cardinality will grow
// unbounded.
type Collector struct {
	tokens *prometheus.CounterVec
	cost   *prometheus.CounterVec
}

// NewCollector creates a Collector and registers its metrics on reg. It
// returns an error if registration fails (e.g. duplicate registration).
func NewCollector(reg prometheus.Registerer) (*Collector, error) {
	c := &Collector{
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_tokens_total",
			Help: "Total LLM tokens processed, by model, agent and token type.",
		}, []string{"model", "agent", "type"}),
		cost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_cost_usd_total",
			Help: "Total LLM cost in USD, by model and agent.",
		}, []string{"model", "agent"}),
	}
	if err := reg.Register(c.tokens); err != nil {
		return nil, err
	}
	if err := reg.Register(c.cost); err != nil {
		return nil, err
	}
	return c, nil
}

// Observe records one step.ended event's tokens and cost for (model, agent).
// It is exported directly (in addition to Watch) so callers that already
// consume the Bus themselves (e.g. to also forward events elsewhere) can feed
// the Collector without a second subscription.
//
// Observe is a no-op on a nil Collector so a caller that mishandles
// NewCollector's error (e.g. `c, _ := metrics.NewCollector(reg)`) degrades
// safely instead of panicking.
func (c *Collector) Observe(model, agent string, se activity.StepEnded) {
	if c == nil {
		return
	}
	c.tokens.WithLabelValues(model, agent, "input").Add(float64(se.Tokens.Input))
	c.tokens.WithLabelValues(model, agent, "output").Add(float64(se.Tokens.Output))
	c.tokens.WithLabelValues(model, agent, "reasoning").Add(float64(se.Tokens.Reasoning))
	c.tokens.WithLabelValues(model, agent, "cache_read").Add(float64(se.Tokens.Cache.Read))
	if se.Cost != 0 {
		c.cost.WithLabelValues(model, agent).Add(se.Cost)
	}
}

// Watch subscribes to bus for sessionID and records every step.ended event
// until ctx is cancelled or the subscription's channel is closed. It runs in
// the calling goroutine; callers typically invoke it with `go c.Watch(...)`.
// It is a no-op on a nil Collector (see Observe).
//
// The activity.StepEnded event does not carry the model name of the step that
// produced it (see activity.StepStarted.Model instead); Watch tracks the most
// recently started model **per agent** (falling back to a session-wide value
// for unattributed/single-agent events) so it can label the following
// step.ended event. This correctly handles agent.switched sessions, but a
// single agent running multiple chat-model steps concurrently within the
// same session can still have step.ended events mislabeled with whichever
// step.started was observed last for that agent, since the wire format does
// not correlate step.started/step.ended pairs by id.
func (c *Collector) Watch(ctx context.Context, bus activity.Bus, sessionID string) {
	if c == nil {
		return
	}
	ch, unsub := bus.Subscribe(ctx, sessionID, "")
	defer unsub()

	currentModel := make(map[string]string) // agent -> most recently started model
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			switch data := e.Data.(type) {
			case activity.StepStarted:
				currentModel[e.Agent] = data.Model
			case activity.StepEnded:
				c.Observe(currentModel[e.Agent], e.Agent, data)
			}
		}
	}
}
