package agent

import (
	"maps"
	"sync"
	"time"
)

// Recorder captures per-turn and per-tool timing data for one agent
// session. Mirrors the data side of upstream pi's
// `core/timings.ts` startup profiler, but extended to cover the agent
// loop's lifecycle (turn start/end + tool execution latencies) so the
// status line, /cost, and --diagnose can all read from a single
// source.
//
// All methods are safe for concurrent use. Tool execution may run in
// parallel goroutines, so RecordTool can be
// called from multiple goroutines at once.
type Recorder struct {
	mu           sync.RWMutex
	sessionStart time.Time
	turnStart    time.Time // zero when not in a turn
	turns        []time.Duration
	toolTotals   map[string]time.Duration
	toolCounts   map[string]int
}

// NewRecorder creates a Recorder. SessionStart is set to time.Now().
func NewRecorder() *Recorder {
	return &Recorder{
		sessionStart: time.Now(),
		toolTotals:   make(map[string]time.Duration),
		toolCounts:   make(map[string]int),
	}
}

// StartTurn marks the beginning of an LLM turn.
func (r *Recorder) StartTurn() {
	r.mu.Lock()
	r.turnStart = time.Now()
	r.mu.Unlock()
}

// EndTurn appends the elapsed turn duration and clears turn state.
// Returns the recorded duration. If StartTurn was not called, returns 0.
func (r *Recorder) EndTurn() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnStart.IsZero() {
		return 0
	}
	d := time.Since(r.turnStart)
	r.turns = append(r.turns, d)
	r.turnStart = time.Time{}
	return d
}

// RecordTool aggregates a tool execution latency.
func (r *Recorder) RecordTool(name string, d time.Duration) {
	r.mu.Lock()
	r.toolTotals[name] += d
	r.toolCounts[name]++
	r.mu.Unlock()
}

// Elapsed returns wall-clock time since session start.
func (r *Recorder) Elapsed() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return time.Since(r.sessionStart)
}

// LastTurnDuration returns the duration of the most recently completed turn,
// or 0 if no turn has completed yet. This is a static snapshot (not a live
// counter), suitable for display in the status line when the agent is idle.
func (r *Recorder) LastTurnDuration() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.turns) == 0 {
		return 0
	}
	return r.turns[len(r.turns)-1]
}

// CurrentTurnElapsed returns the elapsed time of the in-flight turn,
// or 0 if no turn is in flight.
func (r *Recorder) CurrentTurnElapsed() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.turnStart.IsZero() {
		return 0
	}
	return time.Since(r.turnStart)
}

// TimingSnapshot is an immutable copy of recorder state.
type TimingSnapshot struct {
	Total       time.Duration
	Turns       []time.Duration
	ToolTotals  map[string]time.Duration
	ToolCounts  map[string]int
	CurrentTurn time.Duration // 0 if idle

	// Token usage. Populated externally (not by Recorder itself) -
	// the caller fills these before passing to formatCostSummary.
	TotalIn    int
	TotalOut   int
	CacheRead  int
	CacheWrite int
	// Cost is the accumulated per-turn cost (USD), tier/cache-aware.
	Cost float64
}

// Snapshot returns an immutable copy of recorder state.
func (r *Recorder) Snapshot() TimingSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	turns := make([]time.Duration, len(r.turns))
	copy(turns, r.turns)
	totals := make(map[string]time.Duration, len(r.toolTotals))
	maps.Copy(totals, r.toolTotals)
	counts := make(map[string]int, len(r.toolCounts))
	maps.Copy(counts, r.toolCounts)
	var cur time.Duration
	if !r.turnStart.IsZero() {
		cur = time.Since(r.turnStart)
	}
	return TimingSnapshot{
		Total:       time.Since(r.sessionStart),
		Turns:       turns,
		ToolTotals:  totals,
		ToolCounts:  counts,
		CurrentTurn: cur,
	}
}
