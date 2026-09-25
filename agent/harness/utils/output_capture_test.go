package utils

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

type captureFixture struct {
	capture *OutputCapture
	clock   *fakeClock
	updates []harness.ShellOutputUpdate
	errors  []error
}

func createCapture(t *testing.T, maxBytes, maxLines int, retain harness.ShellOutputRetention) *captureFixture {
	t.Helper()
	if maxBytes == 0 {
		maxBytes = 50
	}
	if maxLines == 0 {
		maxLines = 100
	}
	if retain == "" {
		retain = harness.ShellOutputRetainTail
	}
	fixture := &captureFixture{clock: &fakeClock{}}
	capture, err := newOutputCapture(context.Background(), &harness.ShellOutputCaptureOptions{
		Limits: harness.ShellOutputLimits{MaxBytes: maxBytes, MaxLines: maxLines, Retain: retain},
	}, OutputCaptureHandlers{
		OnUpdate: func(_ context.Context, update harness.ShellOutputUpdate) {
			fixture.updates = append(fixture.updates, update)
		},
		OnError: func(err error) { fixture.errors = append(fixture.errors, err) },
	}, fixture.clock)
	if err != nil {
		t.Fatal(err)
	}
	fixture.capture = capture
	return fixture
}

func fold(updates []harness.ShellOutputUpdate) *harness.ShellOutputView {
	var output *harness.ShellOutputView
	for _, update := range updates {
		next := ApplyShellOutputUpdate(output, update)
		output = &next
	}
	return output
}

func TestOutputCaptureRemovesInvalidControlCharacters(t *testing.T) {
	input := "a\x00b\tc\nd\re\u0007f\ufff9g\ufffbh😀"
	if got := SanitizeShellOutput(input); got != "ab\tc\ndefgh😀" {
		t.Fatalf("sanitized = %q", got)
	}
	fixture := createCapture(t, 0, 0, "")
	fixture.capture.PushString(input)
	if got := fixture.capture.Snapshot().Text; got != "ab\tc\ndefgh😀" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestOutputCaptureDecodesUTF8SplitAcrossRawChunks(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	encoded := []byte("😀")
	fixture.capture.Push(encoded[:2])
	if got := fixture.capture.Snapshot().Text; got != "" {
		t.Fatalf("partial snapshot = %q", got)
	}
	fixture.capture.Push(encoded[2:])
	fixture.capture.Finish()
	if got := fixture.capture.Snapshot().Text; got != "😀" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestOutputCaptureFlushesIncompleteUTF8AsOneReplacementCharacter(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	fixture.capture.Push([]byte{0xf0, 0x9f})
	fixture.capture.PushString("x")
	fixture.capture.Push([]byte{0xf0, 0x9f, 'A', 0xff})
	fixture.capture.Finish()
	if got := fixture.capture.Snapshot().Text; got != "�x�A�" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestOutputCapturePublishesFirstViewImmediatelyAndTricklingAppends(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	fixture.capture.PushString("one")
	if len(fixture.updates) != 1 || fixture.updates[0].Kind != harness.ShellOutputUpdateReplace {
		t.Fatalf("updates = %+v", fixture.updates)
	}
	fixture.clock.advance(150)
	fixture.capture.PushString(" two")
	if len(fixture.updates) != 2 || fixture.updates[1].Kind != harness.ShellOutputUpdateAppend || fixture.updates[1].Text != " two" {
		t.Fatalf("updates = %+v", fixture.updates)
	}
	if got := fold(fixture.updates).Text; got != "one two" {
		t.Fatalf("folded = %q", got)
	}
}

func TestOutputCaptureCollapsesABurstIntoOneTrailingUpdate(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	for _, chunk := range []string{"a", "b", "c"} {
		fixture.capture.PushString(chunk)
	}
	if len(fixture.updates) != 1 {
		t.Fatalf("updates = %+v", fixture.updates)
	}
	fixture.clock.advance(100)
	if len(fixture.updates) != 2 || fixture.updates[1].Kind != harness.ShellOutputUpdateAppend || fixture.updates[1].Text != "bc" {
		t.Fatalf("updates = %+v", fixture.updates)
	}
	if got := fold(fixture.updates).Text; got != "abc" {
		t.Fatalf("folded = %q", got)
	}
}

func TestOutputCapturePublishesASmallSlideForPostCapTrickle(t *testing.T) {
	fixture := createCapture(t, 10, 0, "")
	fixture.capture.PushString("abcdefghij")
	fixture.clock.advance(150)
	fixture.capture.PushString("k")
	update := fixture.updates[1]
	if update.Kind != harness.ShellOutputUpdateSlide || update.Drop != 1 || update.Text != "k" {
		t.Fatalf("update = %+v", update)
	}
	folded := fold(fixture.updates)
	if folded.Text != "bcdefghijk" || folded.Truncation.TotalBytes != 11 {
		t.Fatalf("folded = %+v", folded)
	}
}

func TestOutputCaptureSlideDropCountsUTF16CodeUnits(t *testing.T) {
	fixture := createCapture(t, 10, 0, "")
	fixture.capture.PushString("😀abcdef")
	fixture.clock.advance(150)
	fixture.capture.PushString("g")
	update := fixture.updates[1]
	if update.Kind != harness.ShellOutputUpdateSlide || update.Drop != 2 || update.Text != "g" {
		t.Fatalf("update = %+v", update)
	}
	if got := fold(fixture.updates).Text; got != "abcdefg" {
		t.Fatalf("folded = %q", got)
	}
}

func TestOutputCaptureKeepsExactByteCountForALineLargerThanItsBuffer(t *testing.T) {
	fixture := createCapture(t, 10, 0, "")
	fixture.capture.PushString(strings.Repeat("x", 100))
	snapshot := fixture.capture.Snapshot()
	if snapshot.Text != strings.Repeat("x", 10) || snapshot.LastLineBytes == nil || *snapshot.LastLineBytes != 100 || !snapshot.Truncation.LastLinePartial {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestOutputCaptureUsesACapBoundedReplacementAfterCompleteTurnover(t *testing.T) {
	fixture := createCapture(t, 10, 0, "")
	fixture.capture.PushString("abcdefghij")
	fixture.capture.PushString(strings.Repeat("x", 100))
	fixture.clock.advance(100)
	if fixture.updates[1].Kind != harness.ShellOutputUpdateReplace {
		t.Fatalf("update = %+v", fixture.updates[1])
	}
	folded := fold(fixture.updates)
	if len(folded.Text) != 10 || folded.Truncation.TotalBytes != 110 {
		t.Fatalf("folded = %+v", folded)
	}
}

func TestOutputCaptureForcesHeldStateAndCancelsItsTimerOnDispose(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	fixture.capture.PushString("a")
	fixture.capture.PushString("b")
	fixture.capture.Flush()
	if got := fold(fixture.updates).Text; got != "ab" {
		t.Fatalf("folded = %q", got)
	}
	fixture.capture.Dispose()
	fixture.clock.advance(1_000)
	if len(fixture.updates) != 2 {
		t.Fatalf("updates after dispose = %+v", fixture.updates)
	}
	fixture.capture.PushString("ignored")
	if fixture.capture.Snapshot().Text != "ab" {
		t.Fatal("disposed capture accepted output")
	}
}

func TestOutputCapturePreservesTheOriginalHeadAfterItsRawGuardIsCrossed(t *testing.T) {
	fixture := createCapture(t, 100, 2, harness.ShellOutputRetainHead)
	fixture.capture.PushString("first\nsecond\n" + strings.Repeat("tail", 100))
	if got := fixture.capture.Snapshot().Text; got != "first\nsecond" {
		t.Fatalf("snapshot = %q", got)
	}
}

func TestOutputCapturePublishesSpillMetadataWithoutResendingText(t *testing.T) {
	fixture := createCapture(t, 0, 0, "")
	fixture.capture.PushString("output")
	fixture.capture.SetSpillPath("/tmp/output.log")
	last := fixture.updates[len(fixture.updates)-1]
	if last.Kind != harness.ShellOutputUpdateMetadata || last.Metadata.SpillPath != "/tmp/output.log" {
		t.Fatalf("last update = %+v", last)
	}
	if got := fold(fixture.updates).SpillPath; got != "/tmp/output.log" {
		t.Fatalf("folded spill path = %q", got)
	}
	if len(fixture.errors) != 0 {
		t.Fatalf("errors = %v", fixture.errors)
	}
}

func TestOutputCaptureReportsTruncationAndRejectsInvalidLimits(t *testing.T) {
	fixture := createCapture(t, 0, 2, "")
	fixture.capture.PushString("a\nb\nc")
	snapshot := fixture.capture.Snapshot()
	if !fixture.capture.Truncated() || snapshot.Truncation.TruncatedBy == nil || *snapshot.Truncation.TruncatedBy != "lines" || snapshot.Truncation.TotalLines != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for _, limits := range []harness.ShellOutputLimits{{MaxBytes: 0, MaxLines: 1}, {MaxBytes: 1, MaxLines: 0}} {
		if _, err := NewOutputCapture(context.Background(), &harness.ShellOutputCaptureOptions{Limits: limits}, OutputCaptureHandlers{}); err == nil {
			t.Fatalf("limits %+v accepted", limits)
		}
	}
	if _, err := NewOutputCapture(context.Background(), nil, OutputCaptureHandlers{}); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
}

func TestOutputCaptureWallClockPublicationsFoldToTheFinalSnapshot(t *testing.T) {
	var mu sync.Mutex
	var updates []harness.ShellOutputUpdate
	capture, err := NewOutputCapture(context.Background(), &harness.ShellOutputCaptureOptions{Limits: harness.ShellOutputLimits{MaxBytes: 64, MaxLines: 4}}, OutputCaptureHandlers{
		OnUpdate: func(_ context.Context, update harness.ShellOutputUpdate) {
			mu.Lock()
			updates = append(updates, update)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	for writer := range 4 {
		writers.Go(func() {
			for line := range 50 {
				capture.PushString(fmt.Sprintf("w%d-%d\n", writer, line))
			}
		})
	}
	writers.Wait()
	capture.Finish()
	time.Sleep(150 * time.Millisecond)
	capture.Flush()
	capture.Dispose()
	mu.Lock()
	defer mu.Unlock()
	folded := fold(updates)
	if snapshot := capture.Snapshot(); folded == nil || folded.Text != snapshot.Text || snapshot.Truncation.TotalLines != 200 {
		t.Fatalf("folded %+v != snapshot %+v", folded, snapshot)
	}
}
