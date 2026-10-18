// Ports packages/chord/src/output.ts.

package output

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// OutputLimits are the retention limits of one output stream. Retain is "head" or "tail".
type OutputLimits struct {
	MaxBytes int
	MaxLines int
	Retain   string
}

// BoundedOutput is retained output and what the limits dropped.
type BoundedOutput struct {
	Text         string `json:"text"`
	DroppedBytes int    `json:"droppedBytes"`
	DroppedLines int    `json:"droppedLines"`
}

// OutputSlice is an exact slice of the input within the limits, and what it left out.
type OutputSlice struct {
	Text         string
	Bytes        int
	DroppedBytes int
	DroppedLines int
}

// OutputSkip is output a producer omitted before a chunk, measured on the decoded text: every U+FFFD counts as three bytes, and no sanitizing is applied.
type OutputSkip struct {
	// Bytes is the UTF-8 byte length of the omitted text.
	Bytes int
	// Newlines is the count of newlines (U+000A) in the omitted text.
	Newlines int
	// EndsWithNewline reports whether the omitted text ends with a newline.
	EndsWithNewline bool
}

// Utf8ByteLength returns the UTF-8 byte length of content. Go strings hold UTF-8 (lone UTF-16 surrogates arrive as U+FFFD or as their 3-byte WTF-8 form), so the length is the byte count, as Node's Buffer.byteLength reports it.
func Utf8ByteLength(content string) int {
	return len(content)
}

const outputNewline = 0x0a

// SanitizeOutput removes control characters that break display and transcripts; tabs and newlines stay. It removes U+0000-U+0008, U+000B-U+001F, and U+FFF9-U+FFFB (output.ts:24).
func SanitizeOutput(text string) string {
	return strings.Map(func(r rune) rune {
		if (r <= 0x08) || (r >= 0x0b && r <= 0x1f) || (r >= 0xfff9 && r <= 0xfffb) {
			return -1
		}
		return r
	}, text)
}

// BoundOutput bounds text to whole lines within the limits: the first lines for "head", the last lines for "tail". The result is an exact slice, trailing newline included. A single line longer than MaxBytes is cut at the byte limit on a character boundary.
func BoundOutput(text string, limits OutputLimits) OutputSlice {
	data := []byte(text)
	var from, to int
	if limits.Retain == "head" {
		from, to = headRange(data, limits)
	} else {
		from, to = tailRange(data, limits)
	}
	kept := data[from:to]
	keptText := text
	if len(kept) != len(data) {
		keptText = decodeUTF8(kept)
	}
	return OutputSlice{
		Text:         keptText,
		Bytes:        len(kept),
		DroppedBytes: len(data) - len(kept),
		DroppedLines: lineCount(data) - lineCount(kept),
	}
}

func indexFrom(data []byte, from int) int {
	if from >= len(data) {
		return -1
	}
	if from < 0 {
		from = 0
	}
	index := bytes.IndexByte(data[from:], outputNewline)
	if index < 0 {
		return -1
	}
	return from + index
}

// lastIndexFrom mirrors Uint8Array.lastIndexOf(NEWLINE, from).
func lastIndexFrom(data []byte, from int) int {
	if from < 0 {
		return -1
	}
	if from >= len(data) {
		from = len(data) - 1
	}
	return bytes.LastIndexByte(data[:from+1], outputNewline)
}

func headRange(data []byte, limits OutputLimits) (int, int) {
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return 0, 0
	}
	end := len(data)
	lines := 0
	for index := indexFrom(data, 0); index != -1; index = indexFrom(data, index+1) {
		lines++
		if lines == limits.MaxLines {
			end = index + 1
			break
		}
	}
	if end > limits.MaxBytes {
		newline := lastIndexFrom(data, limits.MaxBytes-1)
		if newline == -1 {
			end = CharacterEnd(data, limits.MaxBytes)
		} else {
			end = newline + 1
		}
	}
	return 0, end
}

func tailRange(data []byte, limits OutputLimits) (int, int) {
	if limits.MaxLines == 0 || limits.MaxBytes == 0 {
		return len(data), len(data)
	}
	// A trailing newline ends the last line rather than starting another.
	last := len(data) - 1
	if len(data) > 0 && data[len(data)-1] == outputNewline {
		last = len(data) - 2
	}
	start := 0
	lines := 1
	index := -1
	if last >= 0 {
		index = lastIndexFrom(data, last)
	}
	for index != -1 {
		if lines == limits.MaxLines {
			start = index + 1
			break
		}
		lines++
		if index == 0 {
			index = -1
		} else {
			index = lastIndexFrom(data, index-1)
		}
	}
	if len(data)-start > limits.MaxBytes {
		from := len(data) - limits.MaxBytes
		newline := indexFrom(data, from-1)
		// The first line starting inside the byte window, or a cut of the last line when it alone is too long.
		if newline != -1 && newline+1 < len(data) {
			start = newline + 1
		} else {
			start = characterStart(data, from)
		}
	}
	return start, len(data)
}

// CharacterEnd returns the last character boundary at or before index.
func CharacterEnd(data []byte, index int) int {
	end := index
	for end > 0 && byteAt(data, end)&0xc0 == 0x80 {
		end--
	}
	return end
}

func byteAt(data []byte, index int) byte {
	if index < 0 || index >= len(data) {
		return 0
	}
	return data[index]
}

// characterStart returns the first character boundary at or after index.
func characterStart(data []byte, index int) int {
	start := index
	for start < len(data) && byteAt(data, start)&0xc0 == 0x80 {
		start++
	}
	return start
}

func lineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	newlines := bytes.Count(data, []byte{outputNewline})
	if data[len(data)-1] == outputNewline {
		return newlines
	}
	return newlines + 1
}

type outputChunk struct {
	text     string
	bytes    int
	newlines int
}

// OutputBuffer is the bounded running output of one stream, such as a tool call's or a process's. Accepting a chunk costs time proportional to the chunk: head retention stops storing once the window is full, and tail retention drops stored text the window no longer needs when it snapshots. Counts of the whole stream are kept so the dropped totals stay exact. It is not safe for concurrent use.
type OutputBuffer struct {
	limits  OutputLimits
	decoder utf8StreamDecoder
	// started is whether anything was accepted: only a byte-order mark at the very start of the output is dropped, never a U+FEFF later in it.
	started        bool
	chunks         []outputChunk
	storedBytes    int
	storedNewlines int
	full           bool
	totalBytes     int
	totalNewlines  int
	endsWithNL     bool
}

// NewOutputBuffer returns an empty buffer bounded by limits.
func NewOutputBuffer(limits OutputLimits) *OutputBuffer {
	return &OutputBuffer{limits: limits, endsWithNL: true}
}

// StoredBytes reports the bytes currently held; bounded by the limits plus one chunk.
func (buffer *OutputBuffer) StoredBytes() int { return buffer.storedBytes }

// Push is OutputBuffer.push(chunk, skipped?): chunk is a string or a []byte (any other type is an error, as TypeScript's type rules out), and a
// non-nil skipped is output omitted right before it. It reports whether anything was accepted; a skipped on head retention is an error.
func (buffer *OutputBuffer) Push(chunk any, skipped *OutputSkip) (bool, error) {
	switch value := chunk.(type) {
	case string:
		if skipped == nil {
			return buffer.PushString(value), nil
		}
		return buffer.PushStringSkipping(value, *skipped)
	case []byte:
		if skipped == nil {
			return buffer.PushBytes(value), nil
		}
		return buffer.PushBytesSkipping(value, *skipped)
	}
	return false, errors.New("OutputBuffer chunk must be a string or bytes")
}

// PushString accepts a text chunk; bytes of an incomplete character from an earlier byte chunk come first, as a replacement character. It reports whether anything was accepted.
func (buffer *OutputBuffer) PushString(chunk string) bool {
	pending := buffer.decoder.decode(nil, false)
	buffer.markStarted(pending, chunk, false)
	return buffer.accept(pending + chunk)
}

// markStarted records that text arrived, and reports whether it is the first output: nothing came before it, neither text, an incomplete character, nor omitted output.
func (buffer *OutputBuffer) markStarted(pending, text string, skipped bool) (first bool) {
	first = !buffer.started && pending == "" && !skipped
	if pending != "" || text != "" || skipped {
		buffer.started = true
	}
	return first
}

// decodeBytes decodes a byte chunk after pending (the flushed incomplete character of earlier bytes, if any). Like the decoder of upstream's OutputBuffer (TextDecoder with ignoreBOM), it keeps a U+FEFF, and drops one only at the very start of the output.
func (buffer *OutputBuffer) decodeBytes(pending string, chunk []byte, skipped bool) string {
	text := buffer.decoder.decode(chunk, true)
	if buffer.markStarted(pending, text, skipped) {
		text = strings.TrimPrefix(text, "\ufeff")
	}
	return text
}

// PushBytes accepts a byte chunk, decoding UTF-8 across chunk boundaries. It reports whether anything was accepted.
func (buffer *OutputBuffer) PushBytes(chunk []byte) bool {
	return buffer.accept(buffer.decodeBytes("", chunk, false))
}

// PushStringSkipping accepts a text chunk that follows omitted output: skipped is output omitted right before the chunk,
// which must be more than the tail window by at least one byte or line (ShellOutputInfo.Skipped). Only tail retention
// accepts it; head retention returns an error. It reports that the chunk was accepted.
func (buffer *OutputBuffer) PushStringSkipping(chunk string, skipped OutputSkip) (bool, error) {
	pending := buffer.decoder.decode(nil, false)
	buffer.markStarted(pending, chunk, true)
	return buffer.pushSkipping(pending, chunk, skipped)
}

// PushBytesSkipping is PushStringSkipping for a byte chunk, decoding UTF-8 across the omission as upstream does: bytes of
// an incomplete character before it end as a replacement character.
func (buffer *OutputBuffer) PushBytesSkipping(chunk []byte, skipped OutputSkip) (bool, error) {
	pending := buffer.decoder.decode(nil, false)
	return buffer.pushSkipping(pending, buffer.decodeBytes(pending, chunk, true), skipped)
}

func (buffer *OutputBuffer) pushSkipping(pending, text string, skipped OutputSkip) (bool, error) {
	if buffer.limits.Retain != "tail" {
		return false, errors.New("Skipped output requires tail retention")
	}
	buffer.accept(pending)
	buffer.skip(skipped)
	buffer.accept(text)
	return true, nil
}

// skip counts omitted output; nothing stored before it can be in the window once the text after it arrives.
func (buffer *OutputBuffer) skip(skipped OutputSkip) {
	if skipped.Bytes == 0 {
		return
	}
	buffer.totalBytes += skipped.Bytes
	buffer.totalNewlines += skipped.Newlines
	buffer.endsWithNL = skipped.EndsWithNewline
	buffer.chunks = nil
	buffer.storedBytes = 0
	buffer.storedNewlines = 0
}

// End flushes an incomplete trailing character as a replacement character; call when the stream ends.
func (buffer *OutputBuffer) End() {
	buffer.accept(buffer.decoder.decode(nil, false))
}

func (buffer *OutputBuffer) accept(text string) bool {
	if text == "" {
		return false
	}
	size := len(text)
	newlines := strings.Count(text, "\n")
	buffer.totalBytes += size
	buffer.totalNewlines += newlines
	buffer.endsWithNL = strings.HasSuffix(text, "\n")
	if buffer.full {
		return true
	}
	buffer.chunks = append(buffer.chunks, outputChunk{text: text, bytes: size, newlines: newlines})
	buffer.storedBytes += size
	buffer.storedNewlines += newlines
	if buffer.limits.Retain == "head" {
		// Nothing past a full window is ever needed.
		buffer.full = buffer.storedBytes > buffer.limits.MaxBytes || buffer.storedNewlines >= buffer.limits.MaxLines
		return true
	}
	// Drop leading chunks while the rest still holds more than a window: more than MaxBytes bytes or MaxLines newlines, plus one, so the window's line start can still be found. Each chunk is dropped once.
	for len(buffer.chunks) > 1 {
		first := buffer.chunks[0]
		bytesAfter := buffer.storedBytes - first.bytes
		newlinesAfter := buffer.storedNewlines - first.newlines
		if bytesAfter <= buffer.limits.MaxBytes+1 && newlinesAfter <= buffer.limits.MaxLines+1 {
			break
		}
		buffer.chunks = buffer.chunks[1:]
		buffer.storedBytes = bytesAfter
		buffer.storedNewlines = newlinesAfter
	}
	return true
}

// Snapshot returns the retained, sanitized output and what the limits dropped from the whole stream.
func (buffer *OutputBuffer) Snapshot() BoundedOutput {
	var stored string
	if len(buffer.chunks) == 1 {
		stored = buffer.chunks[0].text
	} else {
		var builder strings.Builder
		for _, chunk := range buffer.chunks {
			builder.WriteString(chunk.text)
		}
		stored = builder.String()
	}
	kept := BoundOutput(stored, buffer.limits)
	storedLines := outputLines(buffer.storedNewlines, stored == "" || strings.HasSuffix(stored, "\n"))
	keptLines := storedLines - kept.DroppedLines
	// Tail windows never reach back before this one, but finding a later window's first line needs what precedes it:
	// keep the shortest suffix longer than the window by a byte or a line, as accept does.
	if buffer.limits.Retain == "tail" || len(buffer.chunks) > 1 {
		text, size := stored, buffer.storedBytes
		if buffer.limits.Retain == "tail" {
			text = tailMargin(stored, buffer.limits)
			size = len(text)
		}
		buffer.chunks = nil
		buffer.storedNewlines = 0
		if text != "" {
			buffer.chunks = []outputChunk{{text: text, bytes: size, newlines: strings.Count(text, "\n")}}
			buffer.storedNewlines = buffer.chunks[0].newlines
		}
		buffer.storedBytes = size
	}
	return BoundedOutput{
		Text:         SanitizeOutput(kept.Text),
		DroppedBytes: buffer.totalBytes - kept.Bytes,
		DroppedLines: outputLines(buffer.totalNewlines, buffer.endsWithNL) - keptLines,
	}
}

// tailMargin is the shortest suffix of text with more than MaxBytes bytes or more than MaxLines newlines, or all of it.
// The tail window of any text that ends with this suffix, followed by anything, is the same as of text followed by it.
func tailMargin(text string, limits OutputLimits) string {
	data := []byte(text)
	byteStart := 0
	if len(data) > limits.MaxBytes {
		byteStart = CharacterEnd(data, len(data)-limits.MaxBytes-1)
	}
	lineStart := 0
	newlines := 0
	for index := lastIndexFrom(data, len(data)-1); index != -1; index = lastIndexFrom(data, index-1) {
		newlines++
		if newlines > limits.MaxLines {
			lineStart = index
			break
		}
		if index == 0 {
			break
		}
	}
	start := max(byteStart, lineStart)
	if start == 0 {
		return text
	}
	return decodeUTF8(data[start:])
}

// outputLines counts the lines of text with newlines newlines; a final unterminated line counts.
func outputLines(newlines int, terminated bool) int {
	if terminated {
		return newlines
	}
	return newlines + 1
}

// decodeUTF8 decodes bytes like a non-streaming WHATWG TextDecoder: each maximal invalid subpart becomes U+FFFD.
func decodeUTF8(data []byte) string {
	var decoder utf8StreamDecoder
	return decoder.decode(data, false)
}

// utf8StreamDecoder is the WHATWG UTF-8 decoder with replacement error mode, as TextDecoder.decode(chunk, {stream}) uses: an incomplete trailing sequence is held while streaming and becomes one U+FFFD on a final decode; each maximal invalid subpart becomes one U+FFFD.
type utf8StreamDecoder struct {
	pending []byte
}

func (decoder *utf8StreamDecoder) decode(chunk []byte, stream bool) string {
	input := chunk
	if len(decoder.pending) > 0 {
		input = append(slices.Clip(decoder.pending), chunk...)
		decoder.pending = nil
	}
	var out strings.Builder
	out.Grow(len(input))
	index := 0
	for index < len(input) {
		lead := input[index]
		if lead < 0x80 {
			out.WriteByte(lead)
			index++
			continue
		}
		needed, lower, upper := 0, byte(0x80), byte(0xbf)
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			needed = 1
		case lead >= 0xe0 && lead <= 0xef:
			needed = 2
			switch lead {
			case 0xe0:
				lower = 0xa0
			case 0xed:
				upper = 0x9f
			}
		case lead >= 0xf0 && lead <= 0xf4:
			needed = 3
			switch lead {
			case 0xf0:
				lower = 0x90
			case 0xf4:
				upper = 0x8f
			}
		default:
			out.WriteString("\uFFFD")
			index++
			continue
		}
		seen := 1
		valid := true
		for seen <= needed {
			if index+seen >= len(input) {
				break
			}
			next := input[index+seen]
			if next < lower || next > upper {
				valid = false
				break
			}
			lower, upper = 0x80, 0xbf
			seen++
		}
		if valid && seen > needed {
			out.Write(input[index : index+seen])
			index += seen
			continue
		}
		if valid && index+seen >= len(input) {
			// Incomplete sequence at the end of input.
			if stream {
				decoder.pending = append([]byte(nil), input[index:]...)
			} else {
				out.WriteString("\uFFFD")
			}
			index = len(input)
			continue
		}
		// Invalid continuation: one replacement for the maximal subpart, then reprocess the offending byte.
		out.WriteString("\uFFFD")
		index += seen
	}
	return out.String()
}

// Each progress commit also buys a pause proportional to what it wrote (output.ts:250).
const (
	ProgressBytesPerSecond  = 100 * 1024
	ProgressMillisPerSecond = 1000
)

// ProgressClock is the clock and timer Progress paces on (output.ts ProgressClock): Now is the time in milliseconds, SetTimer calls
// callback once after about delayMs and returns a function that disarms it.
type ProgressClock struct {
	Now      func() float64
	SetTimer func(callback func(), delayMs float64) (disarm func())
}

// nodeTimeoutMax is Node's TIMEOUT_MAX (lib/internal/timers.js), the longest delay a timer honors: 2^31-1 ms.
const nodeTimeoutMax = 1<<31 - 1

// SystemProgressClock is Date.now and setTimeout, Progress's default clock (output.ts SYSTEM_CLOCK). Its timer follows Node's delay rule: a
// delay outside [1 ms, TIMEOUT_MAX], or NaN, runs after 1 ms (`if (!(after >= 1 && after <= TIMEOUT_MAX)) after = 1`), and the rest is
// truncated to whole milliseconds.
var SystemProgressClock = ProgressClock{
	Now: func() float64 { return float64(time.Now().UnixMilli()) },
	SetTimer: func(callback func(), delayMs float64) func() {
		if !(delayMs >= 1 && delayMs <= nodeTimeoutMax) {
			delayMs = 1
		}
		timer := time.AfterFunc(time.Duration(math.Trunc(delayMs))*time.Millisecond, callback) //portlint:allow numbers delay is within [1, 2^31-1] above, so the conversion is exact
		return func() { timer.Stop() }
	},
}

// ProgressWaiter settles with the progress commit that includes its change. It settles once.
type ProgressWaiter struct {
	once sync.Once
	done chan struct{}
	err  error
	// waiting is the goroutine that queued the waiter or last called Wait on it (the caller of details(), which waits right after queueing), so
	// the settling code can let it run before it goes on.
	waiting atomic.Uint64
}

func newProgressWaiter() *ProgressWaiter { return &ProgressWaiter{done: make(chan struct{})} }

// Resolve settles the waiter successfully.
func (waiter *ProgressWaiter) Resolve() { waiter.settle(nil) }

// Reject settles the waiter with err.
func (waiter *ProgressWaiter) Reject(err error) { waiter.settle(err) }

func (waiter *ProgressWaiter) settle(err error) {
	waiter.once.Do(func() {
		waiter.err = err
		close(waiter.done)
	})
}

// Wait blocks until the waiter settles or ctx is cancelled; cancellation ends only this wait.
func (waiter *ProgressWaiter) Wait(ctx context.Context) error {
	waiter.waiting.Store(goroutineid.Current())
	select {
	case <-waiter.done:
		return waiter.err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// AwaitCaller lets the goroutine that waits on waiter, once the waiter settled, run until it waits again or ends: upstream's continuation of
// a settled promise runs up to its next await before the code after the settlement does. The settling goroutine itself is not waited for.
func AwaitCaller(waiter *ProgressWaiter) {
	if id := waiter.waiting.Load(); id != 0 && id != goroutineid.Current() {
		entered.Await(id, nil)
	}
}

// SettleInOrder settles waiters in order with err. Upstream settles the details() promises of one commit in order and
// runs each continuation up to its next await before the next one, so each waiter's caller, if one waits, runs until it
// waits again or ends before the next waiter is settled. The last waiter's caller is not waited for: what follows the
// settlement of a progress commit does not depend on it.
func SettleInOrder(waiters []*ProgressWaiter, err error) {
	for index, waiter := range waiters {
		waiter.settle(err)
		if index == len(waiters)-1 {
			break
		}
		AwaitCaller(waiter)
	}
}

// SettleAndAwait is SettleInOrder that also lets the last waiter's caller run before it returns. A tool's terminal commit settles the
// details() still waiting, and their continuations run before the run goes on (harness-tools.test.ts:1019), which the caller of
// SettleAndAwait keeps by not letting the commit's owner take the Session line until this returns.
func SettleAndAwait(waiters []*ProgressWaiter, err error) {
	for _, waiter := range waiters {
		waiter.settle(err)
		AwaitCaller(waiter)
	}
}

var errProgressStopped = errors.New("progress stopped before the change could be published")

// Progress schedules adaptive progress commits: the first change after an idle period commits at once; each commit then delays the next by at least the minimum interval and by its written size at 100 KiB/s. At most one commit is in flight; changes made meanwhile coalesce into the next one. Write runs on a goroutine Progress owns; Stop joins it.
type Progress struct {
	write         func() (int, error)
	onError       func(error)
	minIntervalMs float64
	clock         ProgressClock

	mu        sync.Mutex
	waiters   []*ProgressWaiter
	stopTimer func()
	inFlight  chan struct{}
	nextAt    float64
	dirty     bool
	stopped   bool
	// trailing is the last waiter the latest commit settled; its caller was not waited for.
	trailing *ProgressWaiter
}

// NewProgress returns a Progress that commits through write, which reports the bytes it wrote; onError receives a failed commit's error. minIntervalMs is the least pause between commits.
func NewProgress(write func() (int, error), onError func(error), minIntervalMs float64) *Progress {
	return NewProgressWithClock(write, onError, minIntervalMs, SystemProgressClock)
}

// NewProgressWithClock is NewProgress with the time source of the commits, for tests that hold them.
func NewProgressWithClock(write func() (int, error), onError func(error), minIntervalMs float64, clock ProgressClock) *Progress {
	return &Progress{write: write, onError: onError, minIntervalMs: minIntervalMs, clock: clock}
}

// Mark schedules a commit.
func (progress *Progress) Mark() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	progress.markLocked()
}

func (progress *Progress) markLocked() {
	progress.dirty = true
	progress.scheduleLocked()
}

// MarkAndWait schedules a commit; the returned waiter settles with the commit that includes this change.
func (progress *Progress) MarkAndWait() *ProgressWaiter {
	waiter := newProgressWaiter()
	waiter.waiting.Store(goroutineid.Current())
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if progress.stopped {
		// Stop already handed the waiters to the final commit; one added now would never settle.
		waiter.Reject(errProgressStopped)
		return waiter
	}
	progress.waiters = append(progress.waiters, waiter)
	progress.markLocked()
	return waiter
}

// WaitIdle waits until no commit is in flight. A test that drives Progress with its own ProgressClock settles each commit it starts with it, as advanceTimersByTimeAsync drains microtasks.
func (progress *Progress) WaitIdle() {
	for {
		progress.mu.Lock()
		inFlight := progress.inFlight
		progress.mu.Unlock()
		if inFlight == nil {
			return
		}
		<-inFlight
	}
}

// Waiting is the number of changes whose MarkAndWait has not yet had a commit settle it, for tests that hold Progress's timers.
func (progress *Progress) Waiting() int {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	return len(progress.waiters)
}

// Stop stops committing and waits for the commit in flight; it returns the waiters the final commit must settle.
func (progress *Progress) Stop() []*ProgressWaiter {
	progress.mu.Lock()
	progress.stopped = true
	if progress.stopTimer != nil {
		progress.stopTimer()
		progress.stopTimer = nil
	}
	inFlight := progress.inFlight
	progress.mu.Unlock()
	if inFlight != nil {
		<-inFlight
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	waiters := progress.waiters
	progress.waiters = nil
	return waiters
}

// TrailingWaiter is the last waiter the latest progress commit settled, whose caller SettleInOrder did not wait for, or nil. It reads the state Stop
// left, so the caller of a final commit lets that continuation run before the terminal state is published.
func (progress *Progress) TrailingWaiter() *ProgressWaiter {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	return progress.trailing
}

func (progress *Progress) scheduleLocked() {
	if progress.stopped || progress.stopTimer != nil || progress.inFlight != nil {
		return
	}
	wait := progress.nextAt - progress.clock.Now()
	if wait <= 0 {
		progress.flushLocked()
		return
	}
	stop := progress.clock.SetTimer(func() {
		progress.mu.Lock()
		defer progress.mu.Unlock()
		if progress.stopTimer == nil {
			return
		}
		progress.stopTimer = nil
		// Checks the clock again: a timer that fires early waits for the rest (output.ts #schedule).
		progress.scheduleLocked()
	}, wait)
	progress.stopTimer = stop
}

func (progress *Progress) flushLocked() {
	if progress.stopped || !progress.dirty {
		return
	}
	progress.dirty = false
	waiters := progress.waiters
	progress.waiters = nil
	started := progress.clock.Now()
	done := make(chan struct{})
	progress.inFlight = done
	go progress.commit(started, waiters, done) //portlint:allow golifetime the commit is owned by inFlight (done): Stop and WaitIdle wait for it, as Pi's Progress awaits its in-flight commit
}

func (progress *Progress) commit(started float64, waiters []*ProgressWaiter, done chan struct{}) {
	written, err := progress.write()
	progress.mu.Lock()
	if err == nil {
		progress.nextAt = started + math.Max(progress.minIntervalMs, float64(written)*ProgressMillisPerSecond/ProgressBytesPerSecond)
	} else {
		progress.nextAt = started + progress.minIntervalMs
	}
	progress.mu.Unlock()
	// Upstream rejects the waiters and reports in one synchronous handler, so no waiter continuation observes the report missing; report first to keep that.
	if err != nil {
		progress.onError(err)
	}
	SettleInOrder(waiters, err)
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if len(waiters) > 0 {
		progress.trailing = waiters[len(waiters)-1]
	}
	progress.inFlight = nil
	close(done)
	if progress.dirty {
		progress.scheduleLocked()
	}
}
