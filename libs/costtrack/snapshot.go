package costtrack

import (
	"sort"
	"sync"
	"time"

	"github.com/webcenter-fr/eino-ext/callbacks/activity"
	"github.com/webcenter-fr/eino-ext/libs/modelsdev"
)

// Snapshot is a point-in-time view of a session's cost tracking state.
type Snapshot struct {
	SessionID   string        `json:"sessionID"`
	Duration    time.Duration `json:"duration"`
	Steps       int           `json:"steps"`
	Models      []ModelUsage  `json:"models"`
	Totals      Usage         `json:"totals"`
	Compactions int           `json:"compactions"`
	HadFailures bool          `json:"hadFailures"`
	Estimated   bool          `json:"estimated"`
}

// Usage holds aggregate cost, savings, and token counts.
type Usage struct {
	Cost    float64         `json:"cost"`
	Savings float64         `json:"savings"`
	Tokens  activity.Tokens `json:"tokens"`
	Steps   int             `json:"steps"`
}

// ModelUsage groups usage by (provider, model).
type ModelUsage struct {
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Cost      float64         `json:"cost"`
	Savings   float64         `json:"savings"`
	Tokens    activity.Tokens `json:"tokens"`
	Steps     int             `json:"steps"`
	CacheRate float64         `json:"cacheRate"`
}

type modelKey struct {
	provider string
	model    string
}

type sessionState struct {
	mu          sync.Mutex
	models      map[modelKey]*modelAgg
	totals      Usage
	compactions int
	estimated   bool
	toolCalled  bool
	startTime   time.Time
	ended       bool
	endedData   activity.SessionEnded
}

type modelAgg struct {
	Usage
	cacheInput int
}

type snapshotTracker struct {
	mu       sync.Mutex
	sessions map[string]*sessionState
}

func newSnapshotTracker() *snapshotTracker {
	return &snapshotTracker{sessions: make(map[string]*sessionState)}
}

func (st *snapshotTracker) getOrCreate(sessionID string) *sessionState {
	st.mu.Lock()
	defer st.mu.Unlock()
	if s, ok := st.sessions[sessionID]; ok {
		return s
	}
	s := &sessionState{
		models:    make(map[modelKey]*modelAgg),
		startTime: time.Now(),
	}
	st.sessions[sessionID] = s
	return s
}

func (st *snapshotTracker) markRealTask(sessionID, agent string) {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	s.toolCalled = true
	s.mu.Unlock()
}

func (st *snapshotTracker) bumpCompaction(sessionID string) {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	s.compactions++
	s.mu.Unlock()
}

func (st *snapshotTracker) recordStep(sessionID, agent, model string, se activity.StepEnded, b modelsdev.CostBreakdown, breakdownOK bool) {
	s := st.getOrCreate(sessionID)
	key := modelKey{provider: "", model: model}

	s.mu.Lock()
	defer s.mu.Unlock()

	if ma, ok := s.models[key]; ok {
		ma.Cost += se.Cost
		ma.Savings += b.Savings
		ma.Tokens.Input += se.Tokens.Input
		ma.Tokens.Output += se.Tokens.Output
		ma.Tokens.Reasoning += se.Tokens.Reasoning
		ma.Tokens.Cache.Read += se.Tokens.Cache.Read
		ma.Tokens.Cache.Write += se.Tokens.Cache.Write
		ma.cacheInput += se.Tokens.Cache.Read + se.Tokens.Cache.Write
		ma.Steps++
	} else {
		ma := &modelAgg{
			Usage: Usage{
				Cost:    se.Cost,
				Savings: b.Savings,
				Tokens:  se.Tokens,
				Steps:   1,
			},
			cacheInput: se.Tokens.Cache.Read + se.Tokens.Cache.Write,
		}
		s.models[key] = ma
	}

	s.totals.Cost += se.Cost
	s.totals.Savings += b.Savings
	s.totals.Tokens.Input += se.Tokens.Input
	s.totals.Tokens.Output += se.Tokens.Output
	s.totals.Tokens.Reasoning += se.Tokens.Reasoning
	s.totals.Tokens.Cache.Read += se.Tokens.Cache.Read
	s.totals.Tokens.Cache.Write += se.Tokens.Cache.Write
	s.totals.Steps++

	if se.Estimated {
		s.estimated = true
	}
}

func (st *snapshotTracker) buildSessionEnded(sessionID string) activity.SessionEnded {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = true
	return activity.SessionEnded{
		Duration: time.Since(s.startTime).Round(time.Second),
		Cost:     s.totals.Cost,
		Steps:    s.totals.Steps,
		Tools:    0,
	}
}

func (st *snapshotTracker) finalize(sessionID, agent string, se activity.SessionEnded) snapshotInternal {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	s.ended = true
	s.endedData = se
	s.mu.Unlock()
	return st.snapshotInternal(sessionID)
}

type snapshotInternal struct {
	Totals Usage
	isReal bool
}

func (st *snapshotTracker) snapshotInternal(sessionID string) snapshotInternal {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	return snapshotInternal{
		Totals: s.totals,
		isReal: s.toolCalled,
	}
}

func (st *snapshotTracker) snapshot(sessionID string) Snapshot {
	s := st.getOrCreate(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()

	models := make([]ModelUsage, 0, len(s.models))
	for key, ma := range s.models {
		mu := ModelUsage{
			Provider: key.provider,
			Model:    key.model,
			Cost:     ma.Cost,
			Savings:  ma.Savings,
			Tokens:   ma.Tokens,
			Steps:    ma.Steps,
		}
		inputTotal := ma.Tokens.Input + ma.cacheInput
		if inputTotal > 0 {
			mu.CacheRate = float64(ma.Tokens.Cache.Read) / float64(inputTotal)
		}
		models = append(models, mu)
	}

	sort.Slice(models, func(i, j int) bool {
		return models[i].Cost > models[j].Cost
	})

	dur := time.Since(s.startTime)
	if s.ended {
		dur = s.endedData.Duration
	}

	return Snapshot{
		SessionID:   sessionID,
		Duration:    dur,
		Steps:       s.totals.Steps,
		Models:      models,
		Totals:      s.totals,
		Compactions: s.compactions,
		HadFailures: false,
		Estimated:   s.estimated,
	}
}

func (st *snapshotTracker) allSnapshots() map[string]Snapshot {
	st.mu.Lock()
	ids := make([]string, 0, len(st.sessions))
	for id := range st.sessions {
		ids = append(ids, id)
	}
	st.mu.Unlock()

	result := make(map[string]Snapshot, len(ids))
	for _, id := range ids {
		result[id] = st.snapshot(id)
	}
	return result
}
