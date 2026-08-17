// Package otel implements costtrack.Recorder against the OpenTelemetry global
// metric scope (libs/otelmetrics). It records the same logical metrics as the
// default PrometheusRecorder, so a deployment can switch backends or dual-export.
package otel

import (
	"context"

	"emperror.dev/errors"
	"go.opentelemetry.io/otel/attribute"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/costtrack"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
	"github.com/webcenter-fr/eino-ext/libs/otelmetrics"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Config for NewOTelRecorder.
type Config struct {
	Scope *otelmetrics.Scope `json:"-"` // required; built via otelmetrics.NewScope
}

// OTelRecorder implements costtrack.Recorder using the OpenTelemetry global
// metric scope. Instruments mirror the PrometheusRecorder 1:1.
// OTelRecorder is a Recorder that exports metrics via OpenTelemetry.
//
//nolint:revive // Retaining OTel prefix by established naming convention.
type OTelRecorder struct {
	scope *otelmetrics.Scope

	tokens          *otelmetrics.IntCounter
	cost            *otelmetrics.FloatCounter
	savings         *otelmetrics.FloatCounter
	costByComponent *otelmetrics.FloatCounter
	tasksTotal      *otelmetrics.IntCounter
	taskCostHist    *otelmetrics.Histogram
	compactions     *otelmetrics.IntCounter
	realtimeCost    *otelmetrics.Gauge

	complexityRatio *otelmetrics.Gauge
	humanTimeSaved  *otelmetrics.Gauge
	moneySaved      *otelmetrics.Gauge
	fallbackCount   *otelmetrics.IntCounter
	costSaverRuns   *otelmetrics.IntCounter
}

var _ costtrack.Recorder = (*OTelRecorder)(nil)

// NewOTelRecorder validates cfg, then lazily creates all instruments on the
// Scope's Meter. ctx threads to instrument creation.
func NewOTelRecorder(ctx context.Context, cfg *Config) (*OTelRecorder, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if cfg.Scope == nil {
		return nil, errors.New("costtrack/otel: Config.Scope is required")
	}

	s := cfg.Scope

	tokens, err := s.IntCounter("llm.tokens", "Total LLM tokens processed, by model, agent and token type.", "{token}")
	if err != nil {
		return nil, err
	}
	cost, err := s.FloatCounter("llm.cost.usd", "Total LLM cost in USD, by model and agent.", "USD")
	if err != nil {
		return nil, err
	}
	savings, err := s.FloatCounter("llm.cost.savings.usd", "Total LLM cost savings in USD from prompt caching, by model and agent.", "USD")
	if err != nil {
		return nil, err
	}
	costByComponent, err := s.FloatCounter("llm.cost.by_component.usd", "Total LLM cost in USD broken down by cost component, by model and agent.", "USD")
	if err != nil {
		return nil, err
	}
	tasksTotal, err := s.IntCounter("agent.tasks", "Total number of agent tasks completed.", "{task}")
	if err != nil {
		return nil, err
	}
	taskCostHist, err := s.Histogram("agent.task.cost.usd", "Distribution of agent task costs in USD.", "USD", nil)
	if err != nil {
		return nil, err
	}
	compactions, err := s.IntCounter("llm.compactions", "Total number of LLM context compactions.", "{compaction}")
	if err != nil {
		return nil, err
	}
	realtimeCost, err := s.Gauge("llm.realtime.cost.usd", "Real-time running cost for a session, by session and agent.", "USD")
	if err != nil {
		return nil, err
	}

	complexityRatio, err := s.Gauge("cost_saver.complexity_ratio", "Complexity ratio of the session as computed by LLM analyzer.", "1")
	if err != nil {
		return nil, err
	}
	humanTimeSaved, err := s.Gauge("cost_saver.human_time_saved", "Estimated human time saved in seconds.", "s")
	if err != nil {
		return nil, err
	}
	moneySaved, err := s.Gauge("cost_saver.money_saved.usd", "Estimated money saved in USD based on human time and hourly rate.", "USD")
	if err != nil {
		return nil, err
	}
	fallbackCount, err := s.IntCounter("cost_saver.fallback.count", "Count of fallback to simple formula when LLM analysis failed.", "{fallback}")
	if err != nil {
		return nil, err
	}
	costSaverRuns, err := s.IntCounter("cost_saver.runs", "Total number of cost saver analysis runs completed.", "{run}")
	if err != nil {
		return nil, err
	}

	return &OTelRecorder{
		scope:           s,
		tokens:          tokens,
		cost:            cost,
		savings:         savings,
		costByComponent: costByComponent,
		tasksTotal:      tasksTotal,
		taskCostHist:    taskCostHist,
		compactions:     compactions,
		realtimeCost:    realtimeCost,
		complexityRatio: complexityRatio,
		humanTimeSaved:  humanTimeSaved,
		moneySaved:      moneySaved,
		fallbackCount:   fallbackCount,
		costSaverRuns:   costSaverRuns,
	}, nil
}

// ObserveStep records a step observation as an OTel span.
func (r *OTelRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	if r == nil {
		return
	}
	ctx := context.Background()
	ma := otelmetrics.Attrs("model", model, "agent", agent)
	r.tokens.Add(ctx, int64(se.Tokens.Input), otelmetrics.Attrs("model", model, "agent", agent, "type", "input"))
	r.tokens.Add(ctx, int64(se.Tokens.Output), otelmetrics.Attrs("model", model, "agent", agent, "type", "output"))
	r.tokens.Add(ctx, int64(se.Tokens.Reasoning), otelmetrics.Attrs("model", model, "agent", agent, "type", "reasoning"))
	r.tokens.Add(ctx, int64(se.Tokens.Cache.Read), otelmetrics.Attrs("model", model, "agent", agent, "type", "cache_read"))
	if se.Cost != 0 {
		r.cost.Add(ctx, se.Cost, ma)
	}
}

// ObserveBreakdown records a cost breakdown as OTel attributes.
func (r *OTelRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {
	if r == nil {
		return
	}
	ctx := context.Background()
	r.costByComponent.Add(ctx, b.Input, otelmetrics.Attrs("model", model, "agent", agent, "component", "input"))
	r.costByComponent.Add(ctx, b.Output, otelmetrics.Attrs("model", model, "agent", agent, "component", "output"))
	r.costByComponent.Add(ctx, b.CacheRead, otelmetrics.Attrs("model", model, "agent", agent, "component", "cache_read"))
	r.costByComponent.Add(ctx, b.CacheWrite, otelmetrics.Attrs("model", model, "agent", agent, "component", "cache_write"))
	if b.Savings > 0 {
		r.savings.Add(ctx, b.Savings, otelmetrics.Attrs("model", model, "agent", agent))
	}
}

// RecordTask records a task metric via OTel.
func (r *OTelRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	if r == nil {
		return
	}
	ctx := context.Background()
	realStr := "false"
	if real {
		realStr = "true"
	}
	r.tasksTotal.Add(ctx, 1, otelmetrics.Attrs("session_id", sessionID, "agent", agent, "real", realStr))
	r.taskCostHist.Record(ctx, cost, attribute.NewSet())
}

// RecordCompaction increments the compaction counter via OTel.
func (r *OTelRecorder) RecordCompaction(agent string) {
	if r == nil {
		return
	}
	r.compactions.Add(context.Background(), 1, otelmetrics.Attrs("agent", agent))
}

// RecordAnalysis records a complexity analysis via OTel.
func (r *OTelRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {
	if r == nil || a == nil {
		return
	}
	sa := otelmetrics.Attrs("session_id", sessionID, "agent", agent)
	r.complexityRatio.Set(a.ComplexityRatio, sa)
	r.humanTimeSaved.Set(a.HumanTimeSavedSeconds, sa)
	r.moneySaved.Set(a.MoneySavedUSD, sa)
	r.costSaverRuns.Add(context.Background(), 1, otelmetrics.Attrs("agent", agent))
}

// RecordFallback increments the fallback counter via OTel.
func (r *OTelRecorder) RecordFallback(reason string) {
	if r == nil {
		return
	}
	r.fallbackCount.Add(context.Background(), 1, otelmetrics.Attrs("reason", reason))
}

// SetRealtimeCost sets the realtime cost gauge via OTel.
func (r *OTelRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {
	if r == nil {
		return
	}
	r.realtimeCost.Set(cost, otelmetrics.Attrs("session_id", sessionID, "agent", agent))
}
