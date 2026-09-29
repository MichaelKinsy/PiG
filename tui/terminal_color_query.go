package tui

// Ports packages/tui/src/tui.ts

import (
	"io"
	"math"
	"sync"
	"time"
)

// TerminalColorQueryOptions specifies the terminal query deadline in milliseconds.
type TerminalColorQueryOptions struct {
	TimeoutMs float64
}

// TerminalBackgroundColorResult is the completion of a background query. Color is nil for a timeout or an unparseable reply; Err reports a failed terminal write.
type TerminalBackgroundColorResult struct {
	Color *RgbColor
	Err   error
}

type pendingOsc11BackgroundQuery struct {
	settled bool
	result  chan TerminalBackgroundColorResult
	timer   *time.Timer
}

type terminalBackgroundQueries struct {
	mu                            sync.Mutex
	pendingOsc11BackgroundQueries []*pendingOsc11BackgroundQuery
}

// QueryTerminalBackgroundColor writes OSC 11 and returns a one-shot completion. A timed-out query retains its FIFO reply slot so a late reply cannot settle a newer query. Stop does not cancel these deadlines, matching the terminal query's independent Promise lifetime.
func (t *tuiBase) QueryTerminalBackgroundColor(options TerminalColorQueryOptions) <-chan TerminalBackgroundColorResult {
	result := make(chan TerminalBackgroundColorResult, 1)
	query := &pendingOsc11BackgroundQuery{result: result}
	queries := t.terminalBackground
	queries.mu.Lock()
	queries.pendingOsc11BackgroundQueries = append(queries.pendingOsc11BackgroundQueries, query)
	queries.mu.Unlock()

	// Node timers cannot run while terminal.write is on the caller's stack. Start the Go callback after writing, but retain the deadline measured before the write; a synchronous reply may already have settled the query.
	deadline := time.Now().Add(terminalColorQueryDelay(options.TimeoutMs))
	_, err := io.WriteString(t.out, "\x1b]11;?\x07")
	queries.mu.Lock()
	defer queries.mu.Unlock()
	if err != nil {
		query.settle(TerminalBackgroundColorResult{Err: err})
	}
	if !query.settled {
		query.timer = time.AfterFunc(time.Until(deadline), func() {
			queries.mu.Lock()
			defer queries.mu.Unlock()
			query.settle(TerminalBackgroundColorResult{})
		})
	}
	return result
}

func (q *pendingOsc11BackgroundQuery) settle(result TerminalBackgroundColorResult) {
	if q.settled {
		return
	}
	q.settled = true
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	completion := q.result
	q.result = nil
	completion <- result
	close(completion)
}

// ConsumeOsc11BackgroundResponse consumes one strict OSC 11 reply only when a query still owns a reply slot. Call it before terminal-input listeners or focused-component dispatch, including after a query timeout.
func (t *tuiBase) ConsumeOsc11BackgroundResponse(data string) bool {
	queries := t.terminalBackground
	if queries == nil {
		return false
	}
	queries.mu.Lock()
	defer queries.mu.Unlock()
	if len(queries.pendingOsc11BackgroundQueries) == 0 || !IsOsc11BackgroundColorResponse(data) {
		return false
	}
	query := queries.pendingOsc11BackgroundQueries[0]
	queries.pendingOsc11BackgroundQueries[0] = nil
	queries.pendingOsc11BackgroundQueries = queries.pendingOsc11BackgroundQueries[1:]
	query.settle(TerminalBackgroundColorResult{Color: ParseOsc11BackgroundColor(data)})
	return true
}

func terminalColorQueryDelay(milliseconds float64) time.Duration {
	// Node setTimeout truncates fractional delays and uses 1ms for invalid, sub-millisecond, and overflowing delays.
	if math.IsNaN(milliseconds) || milliseconds < 1 || milliseconds > math.MaxInt32 {
		milliseconds = 1
	}
	return time.Duration(milliseconds) * time.Millisecond
}
