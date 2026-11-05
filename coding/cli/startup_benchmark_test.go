package cli

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type benchmarkModeRecorder struct{ calls *[]string }

func (m benchmarkModeRecorder) Init(context.Context) error {
	*m.calls = append(*m.calls, "init")
	return nil
}

func (m benchmarkModeRecorder) Stop(...codingagent.FullscreenExitOutput) error {
	*m.calls = append(*m.calls, "stop")
	return nil
}

// Pi efb45b51d (#11736): PI_STARTUP_BENCHMARK stops the TUI and then disposes the session runtime, the order of a normal interactive quit, so extensions
// receive session_shutdown.
func TestStartupBenchmarkDisposesTheRuntimeAfterStoppingInteractiveMode(t *testing.T) {
	var calls []string
	runStartupBenchmark(t.Context(), benchmarkModeRecorder{&calls}, func() error {
		calls = append(calls, "dispose")
		return nil
	})
	if want := []string{"init", "stop", "dispose"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
