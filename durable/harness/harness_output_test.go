// Ports packages/durable/test/harness-output.test.ts.

package harness

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

func headLimits(maxLines int, maxBytes ...int) OutputLimits {
	return OutputLimits{MaxBytes: optionalBytes(maxBytes), MaxLines: maxLines, Retain: "head"}
}

func tailLimits(maxLines int, maxBytes ...int) OutputLimits {
	return OutputLimits{MaxBytes: optionalBytes(maxBytes), MaxLines: maxLines, Retain: "tail"}
}

func optionalBytes(maxBytes []int) int {
	if len(maxBytes) == 0 {
		return 1000
	}
	return maxBytes[0]
}

// jsString converts UTF-16 code units the way Node's Buffer.from(string, "utf8") does: pairs combine, lone surrogates become U+FFFD. Go strings cannot hold a lone surrogate, so the upstream inputs are given as code units.
func jsString(units ...uint16) string { return string(utf16.Decode(units)) }

func bufferTail(content string, maxBytes int) string {
	data := []byte(content)
	if len(data) <= maxBytes {
		return content
	}
	start := len(data) - maxBytes
	for start < len(data) && data[start]&0xc0 == 0x80 {
		start++
	}
	return string(data[start:])
}

// assertMatchesBufferTail checks that a single line longer than the byte limit is cut like Buffer's tail slice on a character boundary.
func assertMatchesBufferTail(t *testing.T, input string, maxByteValues []int) {
	t.Helper()
	totalBytes := len(input)
	values := maxByteValues
	if values == nil {
		for maxBytes := range totalBytes + 5 {
			values = append(values, maxBytes)
		}
	}
	for _, maxBytes := range values {
		kept := BoundOutput(input, OutputLimits{MaxBytes: maxBytes, MaxLines: 10, Retain: "tail"}).Text
		expected := bufferTail(input, maxBytes)
		if kept != expected {
			t.Fatalf("tail mismatch input=%q maxBytes=%d expected=%q actual=%q", input, maxBytes, expected, kept)
		}
		if len(kept) > maxBytes {
			t.Fatalf("tail output exceeded %d bytes", maxBytes)
		}
	}
}

func sampledByteLimits(input string) []int {
	totalBytes := len(input)
	candidates := []int{0, 1, 2, 3, 4, 5, 8, totalBytes / 2, totalBytes - 4, totalBytes - 1, totalBytes, totalBytes + 1}
	var values []int
	for _, value := range candidates {
		if value >= 0 && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	slices.Sort(values)
	return values
}

type boundResult struct {
	kept         string
	droppedBytes int
	droppedLines int
}

func bound(text string, limits OutputLimits) boundResult {
	slice := BoundOutput(text, limits)
	return boundResult{kept: slice.Text, droppedBytes: slice.DroppedBytes, droppedLines: slice.DroppedLines}
}

func expectBound(t *testing.T, got, want boundResult) {
	t.Helper()
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestToolOutputBounds(t *testing.T) {
	t.Run("removes control characters but keeps tabs, newlines, and other text", func(t *testing.T) {
		if got := SanitizeOutput("a\x00b\tc\nd\re\u0007f\ufff9g\ufffbh😀"); got != "ab\tc\ndefgh😀" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("keeps output within the limits unchanged", func(t *testing.T) {
		expectBound(t, bound("a\nb\n", headLimits(2)), boundResult{"a\nb\n", 0, 0})
		expectBound(t, bound("a\nb", tailLimits(2)), boundResult{"a\nb", 0, 0})
		expectBound(t, bound("", tailLimits(2)), boundResult{"", 0, 0})
	})

	t.Run("keeps nothing with a zero limit", func(t *testing.T) {
		expectBound(t, bound("ab\ncd\n", headLimits(10, 0)), boundResult{"", 6, 2})
		expectBound(t, bound("ab\ncd\n", tailLimits(0)), boundResult{"", 6, 2})
	})

	t.Run("keeps exact slices of whole lines, trailing newline included", func(t *testing.T) {
		expectBound(t, bound("a\nb\nc\n", headLimits(2)), boundResult{"a\nb\n", 2, 1})
		expectBound(t, bound("a\nb\nc\n", tailLimits(2)), boundResult{"b\nc\n", 2, 1})
		expectBound(t, bound("a\nb\nc", tailLimits(2)), boundResult{"b\nc", 2, 1})
		// Blank lines are lines.
		expectBound(t, bound("a\nb\nc\n\n", tailLimits(3)), boundResult{"b\nc\n\n", 2, 1})
	})

	t.Run("cuts at the byte limit on whole lines when possible", func(t *testing.T) {
		expectBound(t, bound("aa\nbb\ncc\n", headLimits(10, 7)), boundResult{"aa\nbb\n", 3, 1})
		expectBound(t, bound("aa\nbb\ncc\n", tailLimits(10, 7)), boundResult{"bb\ncc\n", 3, 1})
	})

	t.Run("cuts a single line longer than the byte limit on a character boundary", func(t *testing.T) {
		// "é" is two bytes; five bytes hold two whole characters.
		expectBound(t, bound("ééé\n", headLimits(10, 5)), boundResult{"éé", 3, 0})
		expectBound(t, bound("x\néééé", tailLimits(10, 5)), boundResult{"éé", 6, 1})
	})

	t.Run("cuts tails of surrogate edge cases exactly like Buffer", func(t *testing.T) {
		inputs := []string{
			jsString('a', 0xd83d),
			jsString(0xde42, 'b'),
			jsString('a', 0xde42, 'b'),
			jsString(0xd83d, 0xd83d, 0xde42),
			jsString(0xd83d, 0xde42, 0xde42),
			"👩‍💻",
		}
		for _, input := range inputs {
			assertMatchesBufferTail(t, input, nil)
		}
	})

	t.Run("cuts tails exactly like Buffer across deterministic fuzz cases", func(t *testing.T) {
		alphabet := [][]uint16{
			{'a'}, {0x007f}, {0x0080}, {0x00e9}, {0x07ff}, {0x0800}, {0x4e2d}, {0xd7ff},
			{0xd800}, {0xd83d}, {0xdc00}, {0xde42}, utf16.Encode([]rune{'🙂'}), {0xe000}, {0xffff},
		}
		var checkExhaustive func(prefix []uint16, depth int)
		checkExhaustive = func(prefix []uint16, depth int) {
			input := jsString(prefix...)
			assertMatchesBufferTail(t, input, sampledByteLimits(input))
			if depth == 0 {
				return
			}
			for _, character := range alphabet {
				checkExhaustive(slices.Concat(prefix, character), depth-1)
			}
		}
		checkExhaustive(nil, 3)
		seed := uint32(0x12345678)
		random := func() float64 {
			seed = seed*1664525 + 1013904223
			return float64(seed) / 0x100000000
		}
		for range 1000 {
			var input []uint16
			length := int(random() * 80)
			for range length {
				input = append(input, alphabet[int(random()*float64(len(alphabet)))]...)
			}
			text := jsString(input...)
			assertMatchesBufferTail(t, text, sampledByteLimits(text))
		}
	})
}

func TestOutputBuffer(t *testing.T) {
	t.Run("keeps exact totals across chunks and decodes UTF-8 split across byte chunks", func(t *testing.T) {
		buffer := NewOutputBuffer(tailLimits(2))
		data := []byte("😀\n")
		buffer.PushString("a\nb\n")
		buffer.PushBytes(data[:2])
		buffer.PushBytes(data[2:])
		if got, want := buffer.Snapshot(), (BoundedOutput{Text: "b\n😀\n", DroppedBytes: 2, DroppedLines: 1}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("sanitizes the retained text but counts the raw stream", func(t *testing.T) {
		buffer := NewOutputBuffer(tailLimits(1))
		buffer.PushString("a\u0007\n")
		if got, want := buffer.Snapshot(), (BoundedOutput{Text: "a\n"}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
		buffer.PushString("b\u001b\n")
		if got, want := buffer.Snapshot(), (BoundedOutput{Text: "b\n", DroppedBytes: 3, DroppedLines: 1}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("flushes an incomplete character before a string chunk and at the end", func(t *testing.T) {
		buffer := NewOutputBuffer(tailLimits(10))
		euro := []byte("€")
		buffer.PushBytes(euro[:1])
		buffer.PushString("x")
		buffer.PushBytes(euro[:2])
		buffer.End()
		if got := buffer.Snapshot().Text; got != "\ufffdx\ufffd" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("matches bounding the whole stream when several chunks arrive between snapshots", func(t *testing.T) {
		for _, limits := range []OutputLimits{headLimits(3, 40), tailLimits(3, 40), headLimits(50, 25), tailLimits(50, 25)} {
			buffer := NewOutputBuffer(limits)
			stream := ""
			for index := range 300 {
				chunk := fmt.Sprintf("line %d\n", index)
				if index%7 == 0 {
					chunk = strings.Repeat("é", index%30) + "\n"
				}
				stream += chunk
				buffer.PushString(chunk)
				if index%5 != 4 {
					continue
				}
				expected := BoundOutput(stream, limits)
				want := BoundedOutput{Text: expected.Text, DroppedBytes: expected.DroppedBytes, DroppedLines: expected.DroppedLines}
				if got := buffer.Snapshot(); got != want {
					t.Fatalf("limits %+v index %d: got %+v, want %+v", limits, index, got, want)
				}
			}
		}
	})

	t.Run("stops storing head output once the window is full", func(t *testing.T) {
		buffer := NewOutputBuffer(headLimits(2))
		for index := range 1000 {
			buffer.PushString(fmt.Sprintf("line %d\n", index))
		}
		if buffer.StoredBytes() >= 20 {
			t.Fatalf("stored %d bytes", buffer.StoredBytes())
		}
		if got, want := buffer.Snapshot(), (BoundedOutput{Text: "line 0\nline 1\n", DroppedBytes: 8876, DroppedLines: 998}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("stores only the tail window after each snapshot", func(t *testing.T) {
		buffer := NewOutputBuffer(tailLimits(3, 100))
		stream := ""
		for index := range 2000 {
			chunk := fmt.Sprintf("line %d\n\n", index)
			stream += chunk
			buffer.PushString(chunk)
			snapshot := buffer.Snapshot()
			if buffer.StoredBytes() > 100 {
				t.Fatalf("stored %d bytes", buffer.StoredBytes())
			}
			if want := BoundOutput(stream, tailLimits(3, 100)).Text; snapshot.Text != want {
				t.Fatalf("got %q, want %q", snapshot.Text, want)
			}
		}
	})
}

// fakeProgressClock stands in for vi fake timers: time moves only through advance, which fires due timers in order and lets the commit each one starts settle, as advanceTimersByTimeAsync drains microtasks.
type fakeProgressClock struct {
	mu     sync.Mutex
	at     float64
	timers []*fakeTimer
}

type fakeTimer struct {
	due     float64
	run     func()
	stopped bool
}

func (clock *fakeProgressClock) now() float64 {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.at
}

func (clock *fakeProgressClock) afterFunc(delay float64, run func()) func() {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	timer := &fakeTimer{due: clock.at + delay, run: run}
	clock.timers = append(clock.timers, timer)
	return func() {
		clock.mu.Lock()
		defer clock.mu.Unlock()
		timer.stopped = true
	}
}

func (clock *fakeProgressClock) advance(progress *Progress, ms float64) {
	waitIdle(progress)
	clock.mu.Lock()
	target := clock.at + ms
	clock.mu.Unlock()
	for {
		clock.mu.Lock()
		var next *fakeTimer
		for _, timer := range clock.timers {
			if !timer.stopped && timer.due <= target && (next == nil || timer.due < next.due) {
				next = timer
			}
		}
		if next == nil {
			clock.at = target
			clock.mu.Unlock()
			return
		}
		next.stopped = true
		clock.at = next.due
		clock.mu.Unlock()
		next.run()
		waitIdle(progress)
	}
}

// waitIdle waits until no commit is in flight.
func waitIdle(progress *Progress) {
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

func TestProgress(t *testing.T) {
	t.Run("commits the first change at once, then waits at least 100 ms and the written size at 100 KiB/s", func(t *testing.T) {
		clock := &fakeProgressClock{}
		var mu sync.Mutex
		var commits []float64
		size := 50 * 1024
		progress := newProgressWithClock(func() (int, error) {
			mu.Lock()
			defer mu.Unlock()
			commits = append(commits, clock.now())
			return size, nil
		}, func(error) {}, 100, clock)
		expect := func(want ...float64) {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(commits, want) {
				t.Fatalf("commits %v, want %v", commits, want)
			}
		}
		progress.Mark()
		clock.advance(progress, 0)
		expect(0)
		// 50 KiB buys 500 ms; changes meanwhile coalesce into one commit.
		mu.Lock()
		size = 10
		mu.Unlock()
		progress.Mark()
		progress.Mark()
		clock.advance(progress, 499)
		expect(0)
		clock.advance(progress, 1)
		expect(0, 500)
		// A small commit still waits the minimum 100 ms.
		progress.Mark()
		clock.advance(progress, 99)
		expect(0, 500)
		clock.advance(progress, 1)
		expect(0, 500, 600)
	})

	t.Run("waits the configured minimum interval between small commits", func(t *testing.T) {
		// upstream: packages/durable/test/harness-output.test.ts:237
		clock := &fakeProgressClock{}
		var mu sync.Mutex
		var commits []float64
		progress := newProgressWithClock(func() (int, error) {
			mu.Lock()
			defer mu.Unlock()
			commits = append(commits, clock.now())
			return 10, nil
		}, func(error) {}, 500, clock)
		expect := func(want ...float64) {
			t.Helper()
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(commits, want) {
				t.Fatalf("commits %v, want %v", commits, want)
			}
		}
		progress.Mark()
		clock.advance(progress, 0)
		progress.Mark()
		clock.advance(progress, 499)
		expect(0)
		clock.advance(progress, 1)
		expect(0, 500)
	})

	t.Run("rejects the waiters of a failed commit and reports its error", func(t *testing.T) {
		var errs []error
		failure := errors.New("commit failed")
		progress := NewProgress(func() (int, error) { return 0, failure }, func(err error) { errs = append(errs, err) }, 100)
		if err := progress.MarkAndWait().Wait(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("got %v, want %v", err, failure)
		}
		if len(errs) != 1 || !errors.Is(errs[0], failure) {
			t.Fatalf("reports %v", errs)
		}
	})

	t.Run("stops: waits for the commit in flight and hands back waiters no commit covered yet", func(t *testing.T) {
		clock := &fakeProgressClock{}
		inFlight := make(chan struct{})
		progress := newProgressWithClock(func() (int, error) {
			<-inFlight
			return 0, nil
		}, func(error) {}, 100, clock)
		first := progress.MarkAndWait()
		second := progress.MarkAndWait()
		stopped := make(chan []*ProgressWaiter)
		go func() { stopped <- progress.Stop() }()
		// Upstream calls stop() before releasing the commit; wait until Stop has taken effect.
		for {
			progress.mu.Lock()
			isStopped := progress.stopped
			progress.mu.Unlock()
			if isStopped {
				break
			}
			runtime.Gosched()
		}
		close(inFlight)
		pending := <-stopped
		if err := first.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 {
			t.Fatalf("pending %d", len(pending))
		}
		pending[0].Resolve()
		if err := second.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}
