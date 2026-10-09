package tui

// Ports packages/tui/src/tui.ts

import (
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// TerminalColorQueryOptions specifies the terminal query deadline in milliseconds.
type TerminalColorQueryOptions struct {
	TimeoutMs float64
	// OnLateReply receives the replies of a QueryTerminalColors query that completes after its timeout. It runs on the goroutine that consumed the completing reply.
	OnLateReply func(TerminalColors)
}

type terminalBackgroundQueries struct {
	mu sync.Mutex
	// pendingTerminalColorQueries wait for their DA1 reply, oldest first. Queries stay here after a timeout to collect late replies.
	pendingTerminalColorQueries []*pendingTerminalColorQuery
}

func terminalColorQueryDelay(milliseconds float64) time.Duration {
	// Node setTimeout truncates fractional delays and uses 1ms for invalid, sub-millisecond, and overflowing delays.
	if math.IsNaN(milliseconds) || milliseconds < 1 || milliseconds > math.MaxInt32 {
		milliseconds = 1
	}
	return time.Duration(milliseconds) * time.Millisecond
}

// TerminalColorsResult is the completion of a terminal color query. Err reports a failed terminal write.
type TerminalColorsResult struct {
	Colors TerminalColors
	Err    error
}

const (
	terminalPaletteSize = 16
	// terminalColorReplyCount counts OSC 10 and 11 plus OSC 4 for every palette color.
	terminalColorReplyCount = 2 + terminalPaletteSize
)

// terminalColorQuery is OSC 10 and 11, then OSC 4 for each palette color, then a trailing primary device attributes (DA1) request. Every terminal answers DA1 and terminals answer in order, so the DA1 reply marks the end of the color replies, including for terminals that ignore the color queries.
var terminalColorQuery = func() string {
	var query strings.Builder
	query.WriteString("\x1b]10;?\x07\x1b]11;?\x07")
	for index := range terminalPaletteSize {
		query.WriteString("\x1b]4;" + strconv.Itoa(index) + ";?\x07")
	}
	query.WriteString("\x1b[c")
	return query.String()
}()

var deviceAttributesResponsePattern = lazyregexp.New(`^\x1b\[\?[0-9;]*c$`)

// pendingTerminalColorQuery collects the replies of one QueryTerminalColors call.
type pendingTerminalColorQuery struct {
	foreground, background *RgbColor
	palette                [terminalPaletteSize]*RgbColor
	// replied holds the targets that already replied, so duplicates do not count twice.
	replied map[OscColorTarget]struct{}
	// deliver receives the result: the completion channel until the timeout, then OnLateReply. It is nil once the query completed (on the DA1 reply, or once every color replied) and when no late-reply callback was given; later replies are ignored.
	deliver func(TerminalColors)
	// timer is the pending timeout, nil once it fired or the query completed.
	timer *time.Timer
}

func (q *pendingTerminalColorQuery) colors() TerminalColors {
	colors := TerminalColors{Foreground: q.foreground, Background: q.background}
	palette := make([]RgbColor, terminalPaletteSize)
	for index, color := range q.palette {
		if color == nil {
			return colors
		}
		palette[index] = *color
	}
	colors.Palette = palette
	return colors
}

// complete stops the timeout and returns the callback that delivers the result, to run without the queries lock.
func (q *pendingTerminalColorQuery) complete() func() {
	deliver := q.deliver
	q.deliver = nil
	if q.timer != nil {
		q.timer.Stop()
		q.timer = nil
	}
	if deliver == nil {
		return func() {}
	}
	colors := q.colors()
	return func() { deliver(colors) }
}

// QueryTerminalColors queries the terminal's theme colors: the default foreground (OSC 10), the default background (OSC 11), and ANSI colors 0-15 (OSC 4), followed by a DA1 request that marks the end of the replies. The completion arrives when the DA1 reply or all color replies arrive, or when the timeout expires. Colors the terminal did not report are nil; the palette is set only when all 16 arrived. A query that completes after the timeout reports its replies to OnLateReply. Stop does not cancel the deadline, matching the terminal query's independent Promise lifetime. Mirrors tui.ts queryTerminalColors.
func (t *tuiBase) QueryTerminalColors(options TerminalColorQueryOptions) <-chan TerminalColorsResult {
	return t.queryTerminalColors(t.out, options)
}

// queryTerminalColors is QueryTerminalColors with the query written to out.
func (t *tuiBase) queryTerminalColors(out io.Writer, options TerminalColorQueryOptions) <-chan TerminalColorsResult {
	result := make(chan TerminalColorsResult, 1)
	query := &pendingTerminalColorQuery{replied: map[OscColorTarget]struct{}{}}
	query.deliver = func(colors TerminalColors) {
		result <- TerminalColorsResult{Colors: colors}
		close(result)
	}
	queries := t.terminalBackground
	queries.mu.Lock()
	queries.pendingTerminalColorQueries = append(queries.pendingTerminalColorQueries, query)
	queries.mu.Unlock()

	// Node timers cannot run while terminal.write is on the caller's stack. Start the Go callback after writing, but retain the deadline measured before the write; a synchronous reply may already have completed the query.
	deadline := time.Now().Add(terminalColorQueryDelay(options.TimeoutMs))
	_, err := io.WriteString(out, terminalColorQuery)
	queries.mu.Lock()
	defer queries.mu.Unlock()
	// Resolve with the replies so far, and keep collecting late replies for OnLateReply.
	expire := func(failure error) {
		query.deliver = options.OnLateReply
		query.timer = nil
		result <- TerminalColorsResult{Colors: query.colors(), Err: failure}
		close(result)
	}
	if err != nil {
		if query.deliver != nil {
			expire(err)
		}
		return result
	}
	if query.deliver != nil {
		query.timer = time.AfterFunc(time.Until(deadline), func() {
			queries.mu.Lock()
			defer queries.mu.Unlock()
			if query.timer != nil {
				expire(nil)
			}
		})
	}
	return result
}

// ConsumeTerminalColorResponse consumes a color or DA1 reply that belongs to the oldest pending color query and reports whether it did. Terminals answer in order, so color replies belong to the oldest query. Call it before terminal-input listeners or focused-component dispatch, including after a query timeout, when a query still waits for its DA1 reply.
func (t *tuiBase) ConsumeTerminalColorResponse(data string) bool {
	queries := t.terminalBackground
	if queries == nil {
		return false
	}
	queries.mu.Lock()
	if len(queries.pendingTerminalColorQueries) == 0 {
		queries.mu.Unlock()
		return false
	}
	query := queries.pendingTerminalColorQueries[0]
	if deviceAttributesResponsePattern.MatchString(data) {
		queries.pendingTerminalColorQueries[0] = nil
		queries.pendingTerminalColorQueries = queries.pendingTerminalColorQueries[1:]
		deliver := query.complete()
		queries.mu.Unlock()
		deliver()
		return true
	}

	reply, ok := ParseOscColorResponse(data)
	if !ok {
		queries.mu.Unlock()
		return false
	}
	if _, duplicate := query.replied[reply.Target]; query.deliver == nil || duplicate {
		queries.mu.Unlock()
		return true
	}
	query.replied[reply.Target] = struct{}{}
	switch {
	case reply.Target == OscColorTargetForeground:
		query.foreground = reply.RGB
	case reply.Target == OscColorTargetBackground:
		query.background = reply.RGB
	case reply.Target >= 0 && reply.Target < terminalPaletteSize:
		query.palette[reply.Target] = reply.RGB
	}
	deliver := func() {}
	if len(query.replied) == terminalColorReplyCount {
		deliver = query.complete()
	}
	queries.mu.Unlock()
	deliver()
	return true
}
