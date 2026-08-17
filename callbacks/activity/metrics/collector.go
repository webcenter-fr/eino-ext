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
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
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
	tokens          *prometheus.CounterVec
	cost            *prometheus.CounterVec
	savings         *prometheus.CounterVec
	costByComponent *prometheus.CounterVec
	reg             prometheus.Registerer
	costSaver       *CostSaverCollector
	summarizer      *activity.SessionSummarizer
	analyzer        *activity.CompositeComplexityAnalyzer
	breakdownFn     func(model string, t activity.Tokens) (modelsdev.CostBreakdown, bool)
}

// CostSaverConfig configures the cost saver feature.
type CostSaverConfig struct {
	Enabled        bool                               `json:"enabled"`
	AnalyzerConfig *activity.ComplexityAnalyzerConfig `json:"analyzer_config"`
}

// Option configures a Collector.
type Option func(*Collector)

// WithCostSaver enables the cost saver feature with the given configuration.
func WithCostSaver(cfg CostSaverConfig, bus activity.Bus) Option {
	return func(c *Collector) {
		if cfg.Enabled && cfg.AnalyzerConfig != nil {
			costSaver, err := NewCostSaverCollector(c.reg)
			if err != nil {
				logrus.WithError(err).Warn("metrics: failed to create cost saver collector")
				return
			}
			c.costSaver = costSaver
			c.summarizer = activity.NewSessionSummarizer(bus)
			c.analyzer = activity.NewCompositeComplexityAnalyzer(*cfg.AnalyzerConfig)
		}
	}
}

// WithBreakdown attaches a breakdown function so the collector records
// llm_cost_savings_usd_total and llm_cost_usd_by_component_total on each
// step.ended event. The counters are registered on the collector's registry
// when this option is applied. When fn is nil, only llm_tokens_total and
// llm_cost_usd_total are recorded, preserving backward compatibility.
func WithBreakdown(fn func(model string, t activity.Tokens) (modelsdev.CostBreakdown, bool)) Option {
	return func(c *Collector) {
		if fn == nil {
			return
		}
		c.breakdownFn = fn
		if c.savings == nil {
			c.savings = prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "llm_cost_savings_usd_total",
				Help: "Total LLM cost savings in USD from prompt caching, by model and agent.",
			}, []string{"model", "agent"})
			if err := c.reg.Register(c.savings); err != nil {
				logrus.WithError(err).Warn("metrics: failed to register llm_cost_savings_usd_total")
			}
		}
		if c.costByComponent == nil {
			c.costByComponent = prometheus.NewCounterVec(prometheus.CounterOpts{
				Name: "llm_cost_usd_by_component_total",
				Help: "Total LLM cost in USD broken down by cost component, by model and agent.",
			}, []string{"model", "agent", "component"})
			if err := c.reg.Register(c.costByComponent); err != nil {
				logrus.WithError(err).Warn("metrics: failed to register llm_cost_usd_by_component_total")
			}
		}
	}
}

// NewCollector creates a Collector and registers its metrics on reg. It
// returns an error if registration fails (e.g. duplicate registration).
func NewCollector(reg prometheus.Registerer, opts ...Option) (*Collector, error) {
	c := &Collector{
		tokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_tokens_total",
			Help: "Total LLM tokens processed, by model, agent and token type.",
		}, []string{"model", "agent", "type"}),
		cost: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "llm_cost_usd_total",
			Help: "Total LLM cost in USD, by model and agent.",
		}, []string{"model", "agent"}),
		reg: reg,
	}
	if err := reg.Register(c.tokens); err != nil {
		return nil, err
	}
	if err := reg.Register(c.cost); err != nil {
		return nil, err
	}
	for _, opt := range opts {
		opt(c)
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
	if c.breakdownFn != nil {
		b, ok := c.breakdownFn(model, se.Tokens)
		if ok {
			c.costByComponent.WithLabelValues(model, agent, "input").Add(b.Input)
			c.costByComponent.WithLabelValues(model, agent, "output").Add(b.Output)
			c.costByComponent.WithLabelValues(model, agent, "cache_read").Add(b.CacheRead)
			c.costByComponent.WithLabelValues(model, agent, "cache_write").Add(b.CacheWrite)
			if b.Savings > 0 {
				c.savings.WithLabelValues(model, agent).Add(b.Savings)
			}
		}
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
//
// When cost saver is enabled, Watch also handles session.ended events to
// analyze session complexity and record cost saver metrics. Cost saver
// analysis only runs for sessions that called at least one tool; no-tool
// sessions are skipped, matching the costtrack facade's isReal guard, so
// trivial sessions neither invoke the analyzer nor increment
// cost_saver_runs_total.
func (c *Collector) Watch(ctx context.Context, bus activity.Bus, sessionID string) {
	if c == nil {
		return
	}
	ch, unsub := bus.Subscribe(ctx, sessionID, "")
	defer unsub()

	currentModel := make(map[string]string) // agent -> most recently started model
	toolCalled := false
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
			case activity.ToolCalled:
				toolCalled = true
			case *activity.ToolCalled:
				toolCalled = true
			case activity.SessionEnded:
				if c.costSaver != nil && c.summarizer != nil && c.analyzer != nil && toolCalled {
					go c.handleSessionEnded(ctx, sessionID, e.Agent, data)
				}
			case *activity.SessionEnded:
				if c.costSaver != nil && c.summarizer != nil && c.analyzer != nil && toolCalled {
					go c.handleSessionEnded(ctx, sessionID, e.Agent, *data)
				}
			}
		}
	}
}

func (c *Collector) handleSessionEnded(ctx context.Context, sessionID, agent string, ended activity.SessionEnded) {
	summary, err := c.summarizer.GetSummary(sessionID)
	if err != nil {
		logrus.WithError(err).Warn("metrics: failed to get session summary for cost saver")
		return
	}

	analysis, err := c.analyzer.Analyze(ctx, summary)
	if err != nil {
		logrus.WithError(err).Warn("metrics: LLM complexity analysis failed, using fallback")
		if c.costSaver != nil {
			c.costSaver.RecordFallback("analysis_error")
		}
		return
	}

	c.costSaver.RecordAnalysis(sessionID, agent, analysis)
}
