// Package costtrack provides a facade for real-time LLM cost tracking: it wires
// the activity bus, models.dev catalog pricer, Prometheus metrics collector,
// cost-saver analysis, and per-session real-time aggregation into a single
// builder API. It also exposes an HTTP endpoint for /metrics and /cost/usage.
//
// The package is intentionally opinionated about wiring (always attaches a
// TokenCounter fallback so cost never silently goes zero on gateways that omit
// streaming usage) but pluggable about the metric backend via the Recorder
// interface.
package costtrack

import (
	"context"
	"net/http"
	"sync/atomic"

	"emperror.dev/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/callbacks/activity/metrics"
	"github.com/webcenter-fr/eino-ext/libs/counter"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
	"github.com/webcenter-fr/eino-ext/libs/toolkit/validate"
)

// Recorder is the abstraction over the metric backend. All methods are
// nil-receiver safe (no-op on nil), so a caller that mishandles construction
// degrades safely. The default implementation is PrometheusRecorder.
type Recorder interface {
	ObserveStep(model, agent string, se activity.StepEnded)
	ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown)
	RecordTask(sessionID, agent string, cost float64, real bool)
	RecordCompaction(agent string)
	RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis)
	RecordFallback(reason string)
	SetRealtimeCost(sessionID, agent string, cost float64)
}

// PrometheusRecorder is the default Recorder backed by Prometheus.
type PrometheusRecorder struct {
	collector       *metrics.Collector
	costSaverColl   *metrics.CostSaverCollector
	tasksTotal      *prometheus.CounterVec
	taskCostHist    prometheus.Histogram
	compactions     *prometheus.CounterVec
	realtimeCost    *prometheus.GaugeVec
	savings         *prometheus.CounterVec
	costByComponent *prometheus.CounterVec
	humanSavings    *prometheus.CounterVec
}

// Config for NewTracker. Validate+jsonschema tags; defaults applied in New.
type Config struct {
	Bus             activity.Bus                      `json:"-"`
	Resolve         modelsdev.NameResolver             `json:"-"`
	CatalogHolder   *atomic.Pointer[modelsdev.Catalog] `json:"-"`
	PricingProvider string                             `json:"pricingProvider" validate:"required"`
	TokenCounter    activity.TokenCounter              `json:"-"`
	Savings         activity.ComplexityAnalyzerConfig  `json:"-"`
	TerminalTypes   []activity.Type                    `json:"-"`
	Registry        prometheus.Registerer              `json:"-"`
	Recorder        Recorder                           `json:"-"`
}

// Tracker is the central facade that ties the activity handler, catalog pricer,
// Prometheus metrics, cost-saver, and per-session aggregation together.
type Tracker struct {
	bus            activity.Bus
	pricer         modelsdev.CatalogPricer
	catalogHolder  *atomic.Pointer[modelsdev.Catalog]
	handler        *activity.Handler
	reg            prometheus.Registerer
	recorder       Recorder
	terminalTypes  []activity.Type
	savings        activity.ComplexityAnalyzerConfig
	summarizer     *activity.SessionSummarizer
	analyzer       *activity.CompositeComplexityAnalyzer
	tracker        *snapshotTracker
}

// NewTracker applies defaults (TokenCounter=counter.DefaultTokenCounter, a new
// private prometheus.Registry, TerminalTypes={answer.ended,question}, and
// Recorder=PrometheusRecorder built from that registry) then calls
// validate.Struct(cfg). When cfg.Recorder is non-nil, NewTracker uses it as-is.
func NewTracker(ctx context.Context, cfg *Config) (*Tracker, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.TokenCounter == nil {
		cfg.TokenCounter = counter.DefaultTokenCounter
	}
	if len(cfg.TerminalTypes) == 0 {
		cfg.TerminalTypes = []activity.Type{"answer.ended", "question"}
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
	if err := validate.Struct(cfg); err != nil {
		return nil, err
	}
	if cfg.Bus == nil {
		return nil, errors.New("costtrack: Config.Bus is required")
	}
	if cfg.Resolve == nil {
		return nil, errors.New("costtrack: Config.Resolve is required")
	}
	if cfg.CatalogHolder == nil {
		return nil, errors.New("costtrack: Config.CatalogHolder is required")
	}

	pricer := modelsdev.CatalogPricer{
		Catalog: cfg.CatalogHolder.Load(),
		Resolve: cfg.Resolve,
	}

	recorder := cfg.Recorder
	if recorder == nil {
		pr, err := newPrometheusRecorder(cfg, pricer)
		if err != nil {
			return nil, errors.Wrap(err, "costtrack: creating PrometheusRecorder")
		}
		recorder = pr
	}

	handler := activity.NewHandlerWithConfig(cfg.Bus,
		activity.WithPricer(pricer),
		activity.WithTokenCounter(cfg.TokenCounter),
	)

	var summarizer *activity.SessionSummarizer
	var analyzer *activity.CompositeComplexityAnalyzer
	if cfg.Savings.HumanHourlyRate > 0 {
		summarizer = activity.NewSessionSummarizer(cfg.Bus)
		analyzer = activity.NewCompositeComplexityAnalyzer(cfg.Savings)
	}

	t := &Tracker{
		bus:           cfg.Bus,
		pricer:        pricer,
		catalogHolder: cfg.CatalogHolder,
		handler:       handler,
		reg:           cfg.Registry,
		recorder:      recorder,
		terminalTypes: cfg.TerminalTypes,
		savings:       cfg.Savings,
		summarizer:    summarizer,
		analyzer:      analyzer,
		tracker:       newSnapshotTracker(),
	}
	return t, nil
}

// ActivityHandler returns the activity.Handler to register globally.
func (t *Tracker) ActivityHandler() *activity.Handler { return t.handler }

// Pricer exposes the dynamic catalog pricer.
func (t *Tracker) Pricer() activity.Pricer { return t.pricer }

// MetricsRegistry is the dedicated Prometheus registry holding all collectors.
func (t *Tracker) MetricsRegistry() prometheus.Registerer { return t.reg }

// PrometheusHandler returns an http.Handler for /metrics.
func (t *Tracker) PrometheusHandler() http.Handler {
	return promhttp.HandlerFor(t.reg.(prometheus.Gatherer), promhttp.HandlerOpts{})
}

// Snapshot returns real-time per-session + global totals (thread-safe).
func (t *Tracker) Snapshot(sessionID string) Snapshot {
	return t.tracker.snapshot(sessionID)
}

// AllSnapshots returns snapshots for all active sessions.
func (t *Tracker) AllSnapshots() map[string]Snapshot {
	return t.tracker.allSnapshots()
}

// Watch must be run in a goroutine per session; it aggregates real-time totals,
// feeds the Recorder, and on a terminal event publishes a synthetic
// session.ended (so the built-in cost-saver path fires) and records task-level
// metrics. Cancel ctx to stop.
func (t *Tracker) Watch(ctx context.Context, sessionID string) {
	ch, unsub := t.bus.Subscribe(ctx, sessionID, "")
	defer unsub()

	seenSessionEnded := false
	currentModel := make(map[string]string)

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
				model := currentModel[e.Agent]
				t.handleStepEnded(sessionID, e.Agent, model, data)
			case activity.SessionEnded:
				seenSessionEnded = true
				t.handleSessionEnded(ctx, sessionID, e.Agent, data)
			case *activity.SessionEnded:
				seenSessionEnded = true
				t.handleSessionEnded(ctx, sessionID, e.Agent, *data)
			case activity.ToolCalled:
				t.tracker.markRealTask(sessionID, e.Agent)
			case activity.CompactionEnded:
				t.tracker.bumpCompaction(sessionID)
				t.recorder.RecordCompaction(e.Agent)
			default:
				if t.isTerminal(e.Type) && !seenSessionEnded {
					seenSessionEnded = true
					se := t.tracker.buildSessionEnded(sessionID)
					t.handleSessionEnded(ctx, sessionID, e.Agent, se)
				}
			}
		}
	}
}

func (t *Tracker) handleStepEnded(sessionID, agent, model string, se activity.StepEnded) {
	t.recorder.ObserveStep(model, agent, se)

	b, ok := t.pricer.Breakdown(model, se.Tokens)
	if ok {
		t.recorder.ObserveBreakdown(model, agent, b)
	}
	t.recorder.SetRealtimeCost(sessionID, agent, se.Cost)

	t.tracker.recordStep(sessionID, agent, model, se, b, ok)
}

func (t *Tracker) handleSessionEnded(ctx context.Context, sessionID, agent string, se activity.SessionEnded) {
	snap := t.tracker.finalize(sessionID, agent, se)
	t.recorder.RecordTask(sessionID, agent, snap.Totals.Cost, snap.isReal)

	if snap.isReal && t.savings.HumanHourlyRate > 0 && t.summarizer != nil && t.analyzer != nil {
		summary, err := t.summarizer.GetSummary(sessionID)
		if err != nil {
			logrus.WithError(err).Warn("costtrack: failed to get session summary for cost saver")
			t.recorder.RecordFallback("summary_error")
			return
		}
		analysis, err := t.analyzer.Analyze(ctx, summary)
		if err != nil {
			logrus.WithError(err).Warn("costtrack: LLM complexity analysis failed, using fallback")
			t.recorder.RecordFallback("analysis_error")
			return
		}
		t.recorder.RecordAnalysis(sessionID, agent, analysis)
	}

	// Publish synthetic session.ended so the built-in metrics.WithCostSaver
	// path can fire if configured.
	evt := activity.Event{
		SessionID: sessionID,
		Agent:     agent,
		Type:      activity.TypeSessionEnded,
		Data:      se,
	}
	t.bus.Publish(context.Background(), evt)
}

func (t *Tracker) isTerminal(typ activity.Type) bool {
	for _, tt := range t.terminalTypes {
		if typ == tt {
			return true
		}
	}
	return typ == activity.TypeSessionEnded
}

// newPrometheusRecorder creates the default PrometheusRecorder and registers
// the facade-owned counters on the given registry.
func newPrometheusRecorder(cfg *Config, pricer modelsdev.CatalogPricer) (*PrometheusRecorder, error) {
	tasksTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "agent_tasks_total",
		Help: "Total number of agent tasks completed.",
	}, []string{"session_id", "agent", "real"})

	taskCostHist := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "agent_task_cost_usd",
		Help:    "Distribution of agent task costs in USD.",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
	})

	compactions := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_compactions_total",
		Help: "Total number of LLM context compactions.",
	}, []string{"agent"})

	realtimeCost := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "llm_realtime_cost_usd",
		Help: "Real-time running cost for a session, by session and agent.",
	}, []string{"session_id", "agent"})

	humanSavings := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "human_savings_usd_total",
		Help: "Total estimated human savings in USD from AI automation, by agent.",
	}, []string{"agent"})

	if err := cfg.Registry.Register(tasksTotal); err != nil {
		return nil, errors.Wrap(err, "registering agent_tasks_total")
	}
	if err := cfg.Registry.Register(taskCostHist); err != nil {
		return nil, errors.Wrap(err, "registering agent_task_cost_usd")
	}
	if err := cfg.Registry.Register(compactions); err != nil {
		return nil, errors.Wrap(err, "registering llm_compactions_total")
	}
	if err := cfg.Registry.Register(realtimeCost); err != nil {
		return nil, errors.Wrap(err, "registering llm_realtime_cost_usd")
	}
	if err := cfg.Registry.Register(humanSavings); err != nil {
		return nil, errors.Wrap(err, "registering human_savings_usd_total")
	}

	savings := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_cost_savings_usd_total",
		Help: "Total LLM cost savings in USD from prompt caching, by model and agent.",
	}, []string{"model", "agent"})

	costByComponent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_cost_usd_by_component_total",
		Help: "Total LLM cost in USD broken down by cost component, by model and agent.",
	}, []string{"model", "agent", "component"})

	if err := cfg.Registry.Register(savings); err != nil {
		return nil, errors.Wrap(err, "registering llm_cost_savings_usd_total")
	}
	if err := cfg.Registry.Register(costByComponent); err != nil {
		return nil, errors.Wrap(err, "registering llm_cost_usd_by_component_total")
	}

	opts := []metrics.Option{}

	collector, err := metrics.NewCollector(cfg.Registry, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "creating metrics collector")
	}

	costSaverColl, err := metrics.NewCostSaverCollector(cfg.Registry)
	if err != nil {
		return nil, errors.Wrap(err, "creating cost saver collector")
	}

	return &PrometheusRecorder{
		collector:       collector,
		costSaverColl:   costSaverColl,
		tasksTotal:      tasksTotal,
		taskCostHist:    taskCostHist,
		compactions:     compactions,
		realtimeCost:    realtimeCost,
		savings:         savings,
		costByComponent: costByComponent,
		humanSavings:    humanSavings,
	}, nil
}

func (r *PrometheusRecorder) ObserveStep(model, agent string, se activity.StepEnded) {
	if r == nil {
		return
	}
	r.collector.Observe(model, agent, se)
}

func (r *PrometheusRecorder) ObserveBreakdown(model, agent string, b modelsdev.CostBreakdown) {
	if r == nil {
		return
	}
	r.costByComponent.WithLabelValues(model, agent, "input").Add(b.Input)
	r.costByComponent.WithLabelValues(model, agent, "output").Add(b.Output)
	r.costByComponent.WithLabelValues(model, agent, "cache_read").Add(b.CacheRead)
	r.costByComponent.WithLabelValues(model, agent, "cache_write").Add(b.CacheWrite)
	if b.Savings > 0 {
		r.savings.WithLabelValues(model, agent).Add(b.Savings)
	}
}

func (r *PrometheusRecorder) RecordTask(sessionID, agent string, cost float64, real bool) {
	if r == nil {
		return
	}
	realStr := "false"
	if real {
		realStr = "true"
	}
	r.tasksTotal.WithLabelValues(sessionID, agent, realStr).Inc()
	r.taskCostHist.Observe(cost)
}

func (r *PrometheusRecorder) RecordCompaction(agent string) {
	if r == nil {
		return
	}
	r.compactions.WithLabelValues(agent).Inc()
}

func (r *PrometheusRecorder) RecordAnalysis(sessionID, agent string, a *activity.ComplexityAnalysis) {
	if r == nil || a == nil {
		return
	}
	r.costSaverColl.RecordAnalysis(sessionID, agent, a)
	r.humanSavings.WithLabelValues(agent).Add(a.MoneySavedUSD)
}

func (r *PrometheusRecorder) RecordFallback(reason string) {
	if r == nil {
		return
	}
	r.costSaverColl.RecordFallback(reason)
}

func (r *PrometheusRecorder) SetRealtimeCost(sessionID, agent string, cost float64) {
	if r == nil {
		return
	}
	r.realtimeCost.WithLabelValues(sessionID, agent).Set(cost)
}
