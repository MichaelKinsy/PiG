//go:build !race

package tui

import (
	"runtime/metrics"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// editorKeystrokeBytesCeiling is the heap bytes one scripted keystroke (editor input, frame render, restoring backspace) allocates, measured over 10,000 keystrokes (about 2,300 when measured; 15,000 before the keystroke allocation work). Bytes allocated on the render goroutine set how often the collector runs and how much mark-assist work the goroutine does while it runs, which is what a frame feels; the count only goes down.
const editorKeystrokeBytesCeiling = 2_500

// Over 10,000 scripted keystrokes the allocated bytes per key stay under the ceiling. The collector's cycle count, pause total and mark-assist CPU of the run are logged for the record: they depend on machine load, so only the deterministic byte count is gated. The counters are process-wide, so the test counts in its own process (testenv.InOwnProcess).
func TestEditorKeystrokeHeapBytesAndCollectorWork(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
	editor := NewEditor()
	editor.SetText(strings.Repeat("a line of the draft with several words in it\n", 40))
	editor.Render(100)
	names := []string{"/gc/heap/allocs:bytes", "/gc/cycles/total:gc-cycles", "/cpu/classes/gc/mark/assist:cpu-seconds"}
	read := func() []metrics.Sample {
		samples := make([]metrics.Sample, len(names))
		for i, name := range names {
			samples[i].Name = name
		}
		metrics.Read(samples)
		return samples
	}
	const keystrokes = 10_000
	const letters = "abcdefghij"
	before := read()
	for i := range keystrokes {
		editor.HandleInput(letters[i%len(letters) : i%len(letters)+1])
		editor.Render(100)
		editor.HandleInput("\x7f")
	}
	after := read()
	perKey := float64(after[0].Value.Uint64()-before[0].Value.Uint64()) / keystrokes
	t.Logf("%.0f bytes per key; %d collector cycles, %.4f s of mark assist over %d keystrokes", perKey,
		after[1].Value.Uint64()-before[1].Value.Uint64(), after[2].Value.Float64()-before[2].Value.Float64(), keystrokes)
	if perKey > editorKeystrokeBytesCeiling {
		t.Fatalf("a keystroke allocates %.0f bytes, ceiling %d", perKey, editorKeystrokeBytesCeiling)
	}
}
