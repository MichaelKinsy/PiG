package codingagent

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi interactive-mode.ts:7090 stop(): the teardowns of init() and run() are released in reverse order, once, and a second stop() does nothing.
// mutation-checked: running the teardowns first-in-first-out, or keeping them after a stop, fails it.
func TestInteractiveModeStopReleasesTeardownsLastInFirstOutOnce(t *testing.T) {
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	var order []string
	failure := errors.New("second teardown failed")
	m.onStop(func() error { order = append(order, "first"); return nil })
	m.onStop(func() error { order = append(order, "second"); return failure })
	m.onStop(func() error { order = append(order, "third"); return nil })
	m.isInitialized = true
	if err := m.Stop(); !errors.Is(err, failure) {
		t.Fatalf("Stop = %v, want the teardown failure joined", err)
	}
	if want := []string{"third", "second", "first"}; !slices.Equal(order, want) {
		t.Fatalf("teardown order = %q, want %q", order, want)
	}
	if m.isInitialized {
		t.Fatal("Stop left the mode initialized")
	}
	if err := m.Stop(); err != nil || len(order) != 3 {
		t.Fatalf("second Stop = %v after %q, want a no-op", err, order)
	}
}

// Pi stop(fullscreenExitOutput = settingsManager.getFullscreenExitOutput()) passes its argument to stopInteractiveTui: the argument wins for that
// stop, the setting applies otherwise, and the argument does not outlive the stop.
// mutation-checked: ignoring the argument, or keeping it after the stop, fails it.
func TestInteractiveModeStopTakesTheFullscreenExitOutputArgument(t *testing.T) {
	settings := Settings{FullscreenExitOutput: FullscreenExitOutputTranscript}
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), Settings: settings})
	var seen []FullscreenExitOutput
	m.onStop(func() error { seen = append(seen, m.fullscreenExitOutput()); return nil })
	if err := m.Stop(FullscreenExitOutputResumeHint); err != nil {
		t.Fatal(err)
	}
	m.onStop(func() error { seen = append(seen, m.fullscreenExitOutput()); return nil })
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if want := []FullscreenExitOutput{FullscreenExitOutputResumeHint, FullscreenExitOutputTranscript}; !slices.Equal(seen, want) {
		t.Fatalf("exit output seen = %q, want %q", seen, want)
	}
}

// Pi init() returns at once when isInitialized, and a failed init leaves the teardowns registered so far for stop().
// mutation-checked: an Init that rebuilds when initialized, or a Stop that skips a partial Init, fails it.
func TestInteractiveModeInitIsGuardedAndAFailedInitIsStoppable(t *testing.T) {
	restoreStartupTheme(t)
	t.Setenv("PIG_HOME", t.TempDir())
	done := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true})
	done.isInitialized = true
	if err := done.Init(t.Context()); err != nil || done.backgroundCtx != nil {
		t.Fatalf("Init on an initialized mode = %v with background %v, want no work", err, done.backgroundCtx)
	}

	m := NewInteractiveMode(nil, InteractiveModeOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true,
		StartupMark: func(name string) {
			if name == "pre-raw-mode" {
				panic("init stopped here")
			}
		},
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Init did not reach the failing mark")
			}
		}()
		_ = m.Init(t.Context())
	}()
	if m.backgroundCtx == nil || m.isInitialized {
		t.Fatalf("a failed Init left background %v initialized %v", m.backgroundCtx, m.isInitialized)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	if m.backgroundCtx != nil {
		t.Fatalf("Stop after a failed Init left background %v", m.backgroundCtx)
	}
	if len(m.teardowns) != 0 {
		t.Fatalf("Stop kept %d teardowns", len(m.teardowns))
	}
}

// Pi handleFatalRuntimeError (interactive-mode.ts:2118-2131) calls stop("transcript"): a fullscreen session that ends on a fatal runtime error
// prints the transcript with the error even when the setting is resume-hint. An ordinary exit keeps the setting.
func TestInteractiveModeFatalRuntimeErrorStopsWithTheTranscript(t *testing.T) {
	for _, fatal := range []bool{true, false} {
		t.Run(map[bool]string{true: "fatal", false: "ordinary"}[fatal], func(t *testing.T) {
			var output bytes.Buffer
			model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
			mode := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{
				CWD: t.TempDir(), Model: model, AgentDir: t.TempDir(),
				Settings: Settings{TuiMode: "fullscreen", FullscreenExitOutput: FullscreenExitOutputResumeHint},
			}, &output)
			mode.mountInteractiveTui(true)
			mode.onStop(func() error { mode.stopInteractiveTui(); return nil })
			mode.chatContainer.Add(tui.NewText("transcript row"))
			mode.tuiInst.Render()
			if fatal {
				if err := mode.handleFatalRuntimeError("Failed to create session", errors.New("disk exploded")); !errors.Is(err, ErrInteractiveCrashed) {
					t.Fatalf("handleFatalRuntimeError = %v", err)
				}
			}
			output.Reset()
			if err := mode.stopRun(); err != nil {
				t.Fatal(err)
			}
			got := output.String()
			leave := strings.Index(got, "\x1b[?1049l")
			if leave < 0 {
				t.Fatalf("stop did not leave the alternate screen; output=%q", got)
			}
			if printed := strings.Contains(got[leave:], "transcript row"); printed != fatal {
				t.Fatalf("transcript printed after the exit = %t, want %t; output=%q", printed, fatal, got)
			}
		})
	}
}
