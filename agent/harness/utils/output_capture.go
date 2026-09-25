package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Output publication pacing for bounded shell output.
const (
	OutputMinEmitIntervalMs    = 100
	OutputTargetBytesPerSecond = 100 * 1024
)

// OutputCaptureHandlers receive publications and trailing-timer errors.
type OutputCaptureHandlers struct {
	OnUpdate func(ctx context.Context, update harness.ShellOutputUpdate)
	OnError  func(err error)
}

// OutputCapture maintains and publishes one bounded shell-output view. Writes
// received while publication is rate-limited collapse into the latest view.
// Small changes remain responsive; complete window turnovers purchase a
// proportionally longer delay. The first update after idle and an explicit
// final flush are immediate.
type OutputCapture struct {
	mu       sync.Mutex
	maxBytes int
	maxLines int
	retain   harness.ShellOutputRetention
	// ctx is the invocation context handed to OnUpdate with each
	// publication; upstream binds the capture to its invocation Context.
	ctx      context.Context
	onUpdate func(ctx context.Context, update harness.ShellOutputUpdate)

	decoder          utf8StreamDecoder
	buffer           string
	totalBytes       int
	newlines         int
	endsWithNewline  bool
	currentLineBytes int
	spillPath        string
	disposed         bool
	publisher        *AdaptivePublisher[harness.ShellOutputView, harness.ShellOutputUpdate]
}

// NewOutputCapture validates the limits and constructs a capture. Nil options
// use the default byte and line limits with tail retention.
func NewOutputCapture(ctx context.Context, options *harness.ShellOutputCaptureOptions, handlers OutputCaptureHandlers) (*OutputCapture, error) {
	return newOutputCapture(ctx, options, handlers, systemClock{})
}

func newOutputCapture(ctx context.Context, options *harness.ShellOutputCaptureOptions, handlers OutputCaptureHandlers, clock publisherClock) (*OutputCapture, error) {
	capture := &OutputCapture{
		maxBytes:        tools.DefaultMaxBytesUpstream,
		maxLines:        tools.DefaultMaxLinesUpstream,
		retain:          harness.ShellOutputRetainTail,
		ctx:             ctx,
		onUpdate:        handlers.OnUpdate,
		endsWithNewline: true,
	}
	if options != nil {
		capture.maxBytes = options.Limits.MaxBytes
		capture.maxLines = options.Limits.MaxLines
		if options.Limits.Retain != "" {
			capture.retain = options.Limits.Retain
		}
	}
	if capture.maxBytes <= 0 {
		return nil, errors.New("Output maxBytes must be a positive finite number")
	}
	if capture.maxLines <= 0 {
		return nil, errors.New("Output maxLines must be a positive integer")
	}
	minInterval, target := float64(OutputMinEmitIntervalMs), float64(OutputTargetBytesPerSecond)
	capture.publisher = newAdaptivePublisher(AdaptivePublisherOptions[harness.ShellOutputView, harness.ShellOutputUpdate]{
		Snapshot:             capture.Snapshot,
		Update:               updateFrom,
		Measure:              measureUpdate,
		Publish:              capture.publish,
		OnError:              handlers.OnError,
		MinIntervalMs:        &minInterval,
		TargetBytesPerSecond: &target,
	}, clock)
	return capture, nil
}

func (capture *OutputCapture) publish(update harness.ShellOutputUpdate) error {
	if capture.onUpdate != nil {
		capture.onUpdate(capture.ctx, update)
	}
	return nil
}

func measureUpdate(update harness.ShellOutputUpdate) int {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(update); err != nil {
		return 0
	}
	return encoded.Len() - 1
}

// Truncated reports whether total output exceeds either limit.
func (capture *OutputCapture) Truncated() bool {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.truncatedLocked()
}

func (capture *OutputCapture) truncatedLocked() bool {
	return capture.totalBytes > capture.maxBytes || capture.totalLinesLocked() > capture.maxLines
}

// Push appends one raw process chunk, decoding UTF-8 split across chunks.
func (capture *OutputCapture) Push(chunk []byte) {
	capture.appendDecoded(func(decoder *utf8StreamDecoder) []string {
		return []string{decoder.decode(chunk, true)}
	})
}

// PushString flushes pending partial bytes and appends one text chunk.
func (capture *OutputCapture) PushString(chunk string) {
	capture.appendDecoded(func(decoder *utf8StreamDecoder) []string {
		return []string{decoder.decode(nil, false), chunk}
	})
}

// Finish flushes pending partial bytes as replacement characters.
func (capture *OutputCapture) Finish() {
	capture.appendDecoded(func(decoder *utf8StreamDecoder) []string {
		return []string{decoder.decode(nil, false)}
	})
}

func (capture *OutputCapture) appendDecoded(decode func(*utf8StreamDecoder) []string) {
	capture.mu.Lock()
	if capture.disposed {
		capture.mu.Unlock()
		return
	}
	texts := decode(&capture.decoder)
	capture.mu.Unlock()
	for _, text := range texts {
		capture.appendText(text)
	}
}

// SetSpillPath records the complete-output spill file and publishes it.
func (capture *OutputCapture) SetSpillPath(path string) {
	capture.mu.Lock()
	if capture.disposed || capture.spillPath == path {
		capture.mu.Unlock()
		return
	}
	capture.spillPath = path
	capture.mu.Unlock()
	_ = capture.publisher.MarkDirty()
	capture.Flush()
}

// Snapshot returns the current bounded view.
func (capture *OutputCapture) Snapshot() harness.ShellOutputView {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	var retained tools.TruncationResult
	if capture.retain == harness.ShellOutputRetainHead {
		retained = tools.TruncateHead(capture.buffer, capture.maxBytes, capture.maxLines)
	} else {
		retained = tools.TruncateTail(capture.buffer, capture.maxBytes, capture.maxLines)
	}
	totalLines := capture.totalLinesLocked()
	truncated := capture.truncatedLocked()
	var truncatedBy *string
	if truncated {
		by := "bytes"
		if totalLines > capture.maxLines {
			by = "lines"
		}
		truncatedBy = &by
	}
	view := harness.ShellOutputView{
		Text: SanitizeShellOutput(retained.Content),
		ShellOutputMetadata: harness.ShellOutputMetadata{
			Truncation: harness.ShellOutputTruncation{
				Truncated:             truncated,
				TruncatedBy:           truncatedBy,
				TotalLines:            totalLines,
				TotalBytes:            capture.totalBytes,
				OutputLines:           retained.OutputLines,
				OutputBytes:           retained.OutputBytes,
				LastLinePartial:       retained.LastLinePartial,
				FirstLineExceedsLimit: retained.FirstLineExceedsLimit,
				MaxLines:              retained.MaxLines,
				MaxBytes:              retained.MaxBytes,
			},
			SpillPath: capture.spillPath,
		},
	}
	if retained.LastLinePartial {
		view.LastLineBytes = new(capture.currentLineBytes)
	}
	return view
}

// Flush publishes held state immediately.
func (capture *OutputCapture) Flush() {
	capture.mu.Lock()
	disposed := capture.disposed
	capture.mu.Unlock()
	if !disposed {
		_ = capture.publisher.Flush(true)
	}
}

// Dispose cancels the trailing timer and ignores later output.
func (capture *OutputCapture) Dispose() {
	capture.publisher.Dispose()
	capture.mu.Lock()
	capture.disposed = true
	capture.mu.Unlock()
}

func (capture *OutputCapture) appendText(text string) {
	if text == "" {
		return
	}
	capture.mu.Lock()
	textBytes := len(text)
	capture.totalBytes += textBytes
	capture.newlines += strings.Count(text, "\n")
	capture.endsWithNewline = strings.HasSuffix(text, "\n")
	if lastNewline := strings.LastIndexByte(text, '\n'); lastNewline == -1 {
		capture.currentLineBytes += textBytes
	} else {
		capture.currentLineBytes = len(text) - lastNewline - 1
	}
	capture.buffer += text
	guard := capture.maxBytes * 2
	if len(capture.buffer) > guard*2 {
		if capture.retain == harness.ShellOutputRetainTail {
			capture.buffer = trimToLastUTF8Bytes(capture.buffer, guard)
		} else {
			capture.buffer = trimToFirstUTF8Bytes(capture.buffer, guard)
		}
	}
	capture.mu.Unlock()
	_ = capture.publisher.MarkDirty()
}

func (capture *OutputCapture) totalLinesLocked() int {
	if capture.endsWithNewline || capture.totalBytes == 0 {
		return capture.newlines
	}
	return capture.newlines + 1
}

// ApplyShellOutputUpdate folds one update into the previous view (nil before
// the first update).
func ApplyShellOutputUpdate(current *harness.ShellOutputView, update harness.ShellOutputUpdate) harness.ShellOutputView {
	previous := ""
	if current != nil {
		previous = current.Text
	}
	switch update.Kind {
	case harness.ShellOutputUpdateReplace:
		return update.Output
	case harness.ShellOutputUpdateAppend:
		return harness.ShellOutputView{Text: previous + update.Text, ShellOutputMetadata: update.Metadata}
	case harness.ShellOutputUpdateSlide:
		return harness.ShellOutputView{Text: utf16Slice(previous, update.Drop) + update.Text, ShellOutputMetadata: update.Metadata}
	default:
		return harness.ShellOutputView{Text: previous, ShellOutputMetadata: update.Metadata}
	}
}

func updateFrom(previous *harness.ShellOutputView, current harness.ShellOutputView) (harness.ShellOutputUpdate, bool) {
	if previous == nil {
		return harness.ShellOutputUpdate{Kind: harness.ShellOutputUpdateReplace, Output: current}, true
	}
	metadata := current.ShellOutputMetadata
	if current.Text == previous.Text {
		return harness.ShellOutputUpdate{Kind: harness.ShellOutputUpdateMetadata, Metadata: metadata}, true
	}
	if len(current.Text) > len(previous.Text) && strings.HasPrefix(current.Text, previous.Text) {
		return harness.ShellOutputUpdate{Kind: harness.ShellOutputUpdateAppend, Text: current.Text[len(previous.Text):], Metadata: metadata}, true
	}
	before := utf16.Encode([]rune(previous.Text))
	after := utf16.Encode([]rune(current.Text))
	shared := suffixPrefixOverlap(before, after, min(len(before), len(after), current.Truncation.MaxBytes*2))
	if shared > 0 {
		return harness.ShellOutputUpdate{
			Kind:     harness.ShellOutputUpdateSlide,
			Drop:     len(before) - shared,
			Text:     string(utf16.Decode(after[shared:])),
			Metadata: metadata,
		}, true
	}
	return harness.ShellOutputUpdate{Kind: harness.ShellOutputUpdateReplace, Output: current}, true
}

// suffixPrefixOverlap returns the longest overlap, in UTF-16 code units,
// between a suffix of before (within its last scan units) and a prefix of
// after, probing at most eight candidate positions per probe length.
func suffixPrefixOverlap(before, after []uint16, scan int) int {
	if len(before) == 0 || len(after) == 0 || scan == 0 {
		return 0
	}
	tail := before
	if len(before) > scan {
		tail = before[len(before)-scan:]
	}
	for _, probeLength := range []int{min(64, len(after)), 1} {
		if overlap := probeOverlap(tail, after, after[:probeLength]); overlap > 0 {
			return overlap
		}
		if probeLength == 1 {
			break
		}
	}
	return 0
}

func probeOverlap(tail, after, probe []uint16) int {
	candidates := 0
	for index := indexUnits(tail, probe, 0); index != -1; index = indexUnits(tail, probe, index+1) {
		candidates++
		if candidates > 8 {
			break
		}
		overlapLength := len(tail) - index
		if overlapLength <= len(after) && equalUnits(tail[index:], after[:overlapLength]) {
			return overlapLength
		}
	}
	return 0
}

func indexUnits(haystack, needle []uint16, from int) int {
	for index := from; index+len(needle) <= len(haystack); index++ {
		if equalUnits(haystack[index:index+len(needle)], needle) {
			return index
		}
	}
	return -1
}

func equalUnits(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func utf16Slice(text string, start int) string {
	units := utf16.Encode([]rune(text))
	if start >= len(units) {
		return ""
	}
	return string(utf16.Decode(units[max(start, 0):]))
}

// SanitizeShellOutput removes C0 control characters other than tab and
// newline, and the U+FFF9-U+FFFB interlinear annotation characters.
func SanitizeShellOutput(text string) string {
	return strings.Map(func(r rune) rune {
		if (r <= 0x08) || (r >= 0x0b && r <= 0x1f) || (r >= 0xfff9 && r <= 0xfffb) {
			return -1
		}
		return r
	}, text)
}

func trimToLastUTF8Bytes(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	start := len(text) - maxBytes
	for start < len(text) && text[start]&0xc0 == 0x80 {
		start++
	}
	return text[start:]
}

func trimToFirstUTF8Bytes(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	end := maxBytes
	for end > 0 && text[end]&0xc0 == 0x80 {
		end--
	}
	return text[:end]
}
