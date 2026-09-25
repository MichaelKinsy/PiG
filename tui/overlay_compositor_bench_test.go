package tui

import (
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"
)

var overlayBenchmarkMatrix = []struct {
	width, height int
}{
	{80, 24},
	{240, 80},
}

type overlayBenchmarkFixture struct {
	base       *TUI
	background []string
	handles    []*OverlayHandle
	frames     [][][]string
	geometry   uint64
}

func newOverlayBenchmarkFixture(b *testing.B, width, height, overlayCount int, content string) overlayBenchmarkFixture {
	b.Helper()
	base := NewWithOutput(io.Discard, width, height)
	background := make([]string, height)
	for row := range background {
		background[row] = strings.Repeat(string(rune('a'+row%26)), width)
	}
	fixture := overlayBenchmarkFixture{
		base: base, background: background,
		geometry: base.updateOverlayGeometry(width, height),
		frames:   make([][][]string, overlayCount),
	}
	for i := range overlayCount {
		handle := base.OpenOverlay(&recordingComponent{}, OverlayOptions{
			width:  overlayPercent(60),
			anchor: overlayTopLeft,
			row:    overlayCells(i % max(1, height-8)),
			col:    overlayCells((i * 3) % max(1, width/3)),
		})
		fixture.handles = append(fixture.handles, handle)
		fixture.frames[i] = [][]string{
			benchmarkOverlayLines(width, content, i, 0),
			benchmarkOverlayLines(width, content, i, 1),
		}
		if !base.replaceOverlaySnapshot(handle, fixture.geometry, 1, fixture.frames[i][0], true) {
			b.Fatal("fixture snapshot rejected")
		}
	}
	return fixture
}

type overlayLatency struct {
	p50, p95, max int64
}

func measureOverlayBenchmark(b *testing.B, operation func() overlayCompositionStats) (overlayLatency, overlayCompositionStats) {
	b.Helper()
	const (
		latencyWarmup  = 256
		latencySamples = 8192
	)
	var stats overlayCompositionStats
	b.ReportAllocs()
	b.StopTimer()
	for range latencyWarmup {
		stats = operation()
	}
	durations := make([]int64, latencySamples)
	for i := range durations {
		started := time.Now()
		stats = operation()
		durations[i] = time.Since(started).Nanoseconds()
	}
	slices.Sort(durations)
	latency := overlayLatency{
		p50: durations[(len(durations)-1)*50/100],
		p95: durations[(len(durations)-1)*95/100],
		max: durations[len(durations)-1],
	}

	b.ResetTimer()
	b.StartTimer()
	for range b.N {
		stats = operation()
	}
	b.StopTimer()
	return latency, stats
}

func reportOverlayBenchmark(b *testing.B, latency overlayLatency, stats overlayCompositionStats) {
	b.Helper()
	b.ReportMetric(float64(latency.p50), "owner_p50_ns")
	b.ReportMetric(float64(latency.p95), "owner_p95_ns")
	b.ReportMetric(float64(latency.max), "owner_max_ns")
	b.ReportMetric(float64(stats.ComposedOutputBytes), "composed_output_bytes/op")
	b.ReportMetric(float64(stats.RetainedSnapshotPayloadBytes), "retained_snapshot_payload_bytes")
}

func BenchmarkOverlayCompositor(b *testing.B) {
	for _, terminal := range overlayBenchmarkMatrix {
		for _, overlays := range []int{1, 8} {
			for _, content := range []string{"ascii", "ansi-wide"} {
				name := fmt.Sprintf("%dx%d/%d-overlays/%s", terminal.width, terminal.height, overlays, content)
				b.Run(name, func(b *testing.B) {
					fixture := newOverlayBenchmarkFixture(b, terminal.width, terminal.height, overlays, content)
					latency, stats := measureOverlayBenchmark(b, func() overlayCompositionStats {
						_, stats := fixture.base.composeOverlayLinesWithStats(fixture.background, terminal.width, terminal.height)
						return stats
					})
					reportOverlayBenchmark(b, latency, stats)
				})
			}
		}
	}
}

func BenchmarkOverlayReplaceAndCompose(b *testing.B) {
	for _, terminal := range overlayBenchmarkMatrix {
		for _, overlays := range []int{1, 8} {
			for _, content := range []string{"ascii", "ansi-wide"} {
				name := fmt.Sprintf("%dx%d/%d-overlays/%s", terminal.width, terminal.height, overlays, content)
				b.Run(name, func(b *testing.B) {
					fixture := newOverlayBenchmarkFixture(b, terminal.width, terminal.height, overlays, content)
					sequence := uint64(1)
					var ingressSliceBytes int
					latency, stats := measureOverlayBenchmark(b, func() overlayCompositionStats {
						sequence++
						ingressSliceBytes = 0
						for i, handle := range fixture.handles {
							lines := fixture.frames[i][sequence%2]
							ingressSliceBytes += len(lines) * int(unsafe.Sizeof(""))
							if !fixture.base.replaceOverlaySnapshot(handle, fixture.geometry, sequence, lines, true) {
								b.Fatal("current dynamic snapshot rejected")
							}
						}
						_, stats := fixture.base.composeOverlayLinesWithStats(fixture.background, terminal.width, terminal.height)
						return stats
					})
					reportOverlayBenchmark(b, latency, stats)
					b.ReportMetric(float64(ingressSliceBytes), "ingress_slice_header_bytes/op")
				})
			}
		}
	}
}

func BenchmarkOverlaySnapshotReplacement256KiB(b *testing.B) {
	// This isolates immutable ownership transfer. The string payload is already
	// immutable, so replacement copies one slice of string headers and retains
	// the 256 KiB payload; it intentionally does not copy 256 KiB per update.
	base := NewWithOutput(io.Discard, 80, 24)
	handle := base.OpenOverlay(&recordingComponent{}, OverlayOptions{})
	geometry := base.updateOverlayGeometry(80, 24)
	const payloadBytes = 256 << 10
	frames := [2][]string{{strings.Repeat("x", payloadBytes)}, {strings.Repeat("y", payloadBytes)}}
	sequence := uint64(0)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		sequence++
		if !base.replaceOverlaySnapshot(handle, geometry, sequence, frames[sequence%2], true) {
			b.Fatal("large current snapshot rejected")
		}
	}
	b.StopTimer()
	b.ReportMetric(payloadBytes, "retained_snapshot_payload_bytes")
	b.ReportMetric(float64(int(unsafe.Sizeof(""))), "ingress_slice_header_bytes/op")
}

func benchmarkOverlayLines(width int, content string, index, generation int) []string {
	lineWidth := max(8, width*3/5)
	lines := make([]string, 6)
	for row := range lines {
		if content == "ascii" {
			fill := "x"
			if generation%2 == 1 {
				fill = "y"
			}
			lines[row] = fmt.Sprintf("overlay=%d row=%d %s", index, row, strings.Repeat(fill, max(0, lineWidth-24)))
			continue
		}
		lines[row] = fmt.Sprintf("\x1b[1;3%dm界👩‍💻e\u0301\trow=%d\x1b[0m %s", (index+generation)%8, row, strings.Repeat("界", max(0, lineWidth/2-14)))
	}
	return lines
}

func TestOverlayImmutableFramePayloadIsSharedUntilReplacement(t *testing.T) {
	base := NewWithOutput(io.Discard, 80, 24)
	handle := base.OpenOverlay(&recordingComponent{}, OverlayOptions{})
	geometry := base.updateOverlayGeometry(80, 24)
	if !base.replaceOverlaySnapshot(handle, geometry, 1, []string{"first", "second"}, true) {
		t.Fatal("initial snapshot rejected")
	}

	base.overlayMu.Lock()
	accepted := base.overlayModel.overlayByID(handle.id).frame.lines
	base.overlayMu.Unlock()
	published := base.overlaySnapshot()
	entry, ok := published.entryByID(handle.id)
	if !ok {
		t.Fatal("published snapshot omitted mounted frame")
	}
	if &entry.frame.lines[0] != &accepted[0] {
		t.Fatal("publishing immutable frame copied its line-slice payload")
	}
	rendered := renderOverlayEntry(&entry, 80, 0, false)
	if &rendered[0] != &entry.frame.lines[0] {
		t.Fatal("rendering immutable frame copied its line-slice payload")
	}

	if !base.replaceOverlaySnapshot(handle, geometry, 2, []string{"replacement"}, true) {
		t.Fatal("replacement snapshot rejected")
	}
	if entry.frame.lines[0] != "first" || rendered[1] != "second" {
		t.Fatalf("replacement mutated in-flight immutable frame: entry=%v rendered=%v", entry.frame.lines, rendered)
	}
}

func TestOverlaySnapshotBuffersReclaimedAfterReplacementAndTeardown(t *testing.T) {
	base := NewWithOutput(io.Discard, 80, 24)
	handle := base.OpenOverlay(&recordingComponent{}, OverlayOptions{})
	geometry := base.updateOverlayGeometry(80, 24)
	large := []string{strings.Repeat("x", 1<<20)}
	if !base.replaceOverlaySnapshot(handle, geometry, 1, large, true) {
		t.Fatal("large snapshot rejected")
	}
	large[0] = "caller released"
	if retained := base.overlaySnapshot().RetainedBytes; retained != 1<<20 {
		t.Fatalf("retained bytes = %d, want %d", retained, 1<<20)
	}
	if !base.replaceOverlaySnapshot(handle, geometry, 2, []string{"small"}, true) {
		t.Fatal("replacement snapshot rejected")
	}
	if retained := base.overlaySnapshot().RetainedBytes; retained != len("small") {
		t.Fatalf("replacement retained bytes = %d, want %d", retained, len("small"))
	}
	handle.closeTree()
	if retained := base.overlaySnapshot().RetainedBytes; retained != 0 {
		t.Fatalf("teardown retained bytes = %d, want 0", retained)
	}

	// Keep the reclamation assertion deterministic at the model boundary, then
	// force two collections so race/leak runs also exercise final reachability.
	runtime.GC()
	debug.FreeOSMemory()
}
