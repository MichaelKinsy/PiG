package daemon

// Ports packages/env/daemon/src/window.rs
//
// Durable's output window (ShellExecOptions.window, ShellOutputInfo.skipped): undelivered command output is held and
// coalesced; text followed by more than the window (by a byte or a line) can never be in the caller's tail and is
// replaced by counts. Delivery follows the caller's commit pace, and waits while an earlier frame is still unsent.

import (
	"strings"
	"time"
	"unicode/utf8"
)

type window struct {
	maxBytes       int
	maxLines       int
	minInterval    time.Duration
	bytesPerSecond float64
}

// windowFromJSON is nil unless the value is an object with all four fields.
func windowFromJSON(value any) *window {
	object, ok := value.(Object)
	if !ok {
		return nil
	}
	maxBytes, okBytes := unsignedNumber(object["maxBytes"])
	maxLines, okLines := unsignedNumber(object["maxLines"])
	interval, okInterval := object["minIntervalMs"].(float64)
	rate, okRate := object["bytesPerSecond"].(float64)
	if !okBytes || !okLines || !okInterval || !okRate {
		return nil
	}
	return &window{
		maxBytes:       int(min(maxBytes, 1<<53)),
		maxLines:       int(min(maxLines, 1<<53)),
		minInterval:    seconds(max(interval, 0) / 1000),
		bytesPerSecond: max(rate, 1),
	}
}

func seconds(value float64) time.Duration {
	if value >= float64(1<<62)/float64(time.Second) {
		return time.Duration(1 << 62)
	}
	return time.Duration(value * float64(time.Second))
}

type skipped struct {
	bytes           uint64
	newlines        uint64
	endsWithNewline bool
}

// outputEvent is one output event: its stream, text, and the output omitted right before it.
type outputEvent struct {
	stream  string
	text    string
	skipped *skipped
}

type segment struct {
	stream string
	text   string
}

type pending struct {
	window   window
	segments []segment
	bytes    int
	newlines int
	skipped  *skipped
	nextSend time.Time
}

func newPending(w window) *pending { return &pending{window: w, nextSend: time.Now()} }

func newlineCount(text string) int { return strings.Count(text, "\n") }

func (p *pending) isEmpty() bool { return len(p.segments) == 0 }

func (p *pending) push(stream, text string) {
	if text == "" {
		return
	}
	p.bytes += len(text)
	p.newlines += newlineCount(text)
	if last := len(p.segments) - 1; last >= 0 && p.segments[last].stream == stream {
		p.segments[last].text += text
	} else {
		p.segments = append(p.segments, segment{stream, text})
	}
	// Trimming costs a pass over the held text; do it once a few windows have accumulated.
	if p.bytes > 4*(p.window.maxBytes+1) || p.newlines > 4*(p.window.maxLines+1) {
		p.trim()
	}
}

// proves reports whether the text after a cut still proves the cut text is outside the window.
func (p *pending) proves(bytes, newlines int) bool {
	return bytes > p.window.maxBytes || newlines > p.window.maxLines
}

func isCharBoundary(text string, index int) bool {
	return index == 0 || index == len(text) || utf8.RuneStart(text[index])
}

func saturatingSub(a, b int) int { return max(a-b, 0) }

// trim replaces the longest prefix whose remainder still proves exclusion by counts.
func (p *pending) trim() {
	var removed strings.Builder
	// Whole segments first.
	for len(p.segments) > 1 {
		first := p.segments[0].text
		bytes, newlines := len(first), newlineCount(first)
		if !p.proves(p.bytes-bytes, p.newlines-newlines) {
			break
		}
		p.segments = p.segments[1:]
		p.bytes -= bytes
		p.newlines -= newlines
		removed.WriteString(first)
	}
	// Then a prefix of the first remaining segment, at a character boundary.
	first := p.segments[0].text
	restBytes := p.bytes - len(first)
	restNewlines := p.newlines - newlineCount(first)
	cut := 0
	// By bytes: keep more than maxBytes overall.
	keepBytes := saturatingSub(p.window.maxBytes+1, restBytes)
	if len(first) > keepBytes {
		index := len(first) - keepBytes
		for index > 0 && !isCharBoundary(first, index) {
			index--
		}
		cut = max(cut, index)
	}
	// By lines: keep more than maxLines newlines overall, starting at a newline of this segment.
	keepNewlines := saturatingSub(p.window.maxLines+1, restNewlines)
	if keepNewlines > 0 {
		var positions []int
		for index := range len(first) {
			if first[index] == '\n' {
				positions = append(positions, index)
			}
		}
		if len(positions) >= keepNewlines {
			cut = max(cut, positions[len(positions)-keepNewlines])
		}
	} else {
		cut = len(first)
	}
	if cut > 0 {
		prefix := first[:cut]
		p.segments[0].text = first[cut:]
		p.bytes -= len(prefix)
		p.newlines -= newlineCount(prefix)
		removed.WriteString(prefix)
		if p.segments[0].text == "" {
			p.segments = p.segments[1:]
		}
	}
	if removed.Len() > 0 {
		text := removed.String()
		if p.skipped == nil {
			p.skipped = &skipped{}
		}
		p.skipped.bytes += uint64(len(text))
		p.skipped.newlines += uint64(newlineCount(text))
		p.skipped.endsWithNewline = strings.HasSuffix(text, "\n")
	}
}

// take delivers everything held: one event carrying the skip and all text after it, or one event per segment.
func (p *pending) take() []outputEvent {
	segments := p.segments
	p.segments = nil
	delivered := p.bytes
	p.bytes = 0
	p.newlines = 0
	// The caller writes at most the window per sample, and paces itself by what it wrote.
	pace := seconds(float64(min(delivered, p.window.maxBytes)) / p.window.bytesPerSecond)
	p.nextSend = time.Now().Add(max(p.window.minInterval, pace))
	if p.skipped != nil {
		skip := p.skipped
		p.skipped = nil
		stream := "stdout"
		if len(segments) > 0 {
			stream = segments[len(segments)-1].stream
		}
		var text strings.Builder
		for _, held := range segments {
			text.WriteString(held.text)
		}
		return []outputEvent{{stream: stream, text: text.String(), skipped: skip}}
	}
	events := make([]outputEvent, len(segments))
	for index, held := range segments {
		events[index] = outputEvent{stream: held.stream, text: held.text}
	}
	return events
}
