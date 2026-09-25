package codingagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// terminalRecorder is the renderer's terminal: it records every byte written
// and lets the test take the bytes of one frame at a time.
type terminalRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *terminalRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *terminalRecorder) take() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.buf.String()
	r.buf.Reset()
	return s
}

// newTickRenderProbe builds the interactive component tree the way Run does
// (header, chat, pending messages, status, widgets, the status-embedding editor
// in its container, footer) on the production renderer for mode, writing to a
// recorder.
func newTickRenderProbe(t testing.TB, mode string) (*InteractiveMode, *terminalRecorder) {
	t.Helper()
	model := &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir(), Model: model, Settings: Settings{TuiMode: mode}})
	term := &terminalRecorder{}
	m.rendererOut = term
	m.runCtx = t.Context()
	m.createInteractiveTui(t.Context())
	t.Cleanup(m.teardownCurrentTui)
	m.extHeader = newSpecialLinesComponent(m.renderNow)
	m.chatContainer = tui.NewContainer()
	m.extFooter = newSpecialLinesComponent(m.renderNow)
	m.editor = tui.NewEditor()
	m.editor.EmbedWorkingStatus = true
	m.editorContainer = tui.NewContainer()
	m.editorContainer.Add(m.editor)
	m.statusLine = NewStatusLine(nil, "", nil)
	m.statusContainer = tui.NewContainer()
	m.pendingMessagesContainer = tui.NewContainer()
	m.widgetContainer = tui.NewContainer()
	m.agent = agent.NewAgent(agent.AgentOptions{})
	m.keybindings = NewKeybindingsManager(t.TempDir())
	m.mountInteractiveTui()
	m.installRenderDispatcher()
	return m, term
}

// Pi starts the UI before the optional welcome header and extension initialization.
// An empty chat (quietStartup) must still paint the editor and footer without input.
func TestInteractiveMountPaintsBeforeInput(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				time.Sleep(100 * time.Millisecond)
				synctest.Wait()
				m.drainMainLoopOnce()
				frame := widthx.StripAnsi(term.take())
				for _, want := range []string{"─", "0.0%/0", "unknown"} {
					if !strings.Contains(frame, want) {
						t.Fatalf("mounted UI lacks %q before input: %q", want, frame)
					}
				}
				if strings.Contains(frame, "Ready. Type a message") {
					t.Fatal("empty startup chat unexpectedly contains a welcome banner")
				}
			})
		})
	}
}

// The owner's frozen session: a long bash tool call runs, a steering message
// is queued behind it, and nobody types. Pi's Loader interval advances the
// "Working" spinner every 80ms and requests a render, and bash.ts re-renders its
// "Elapsed" footer on a timer, so both reach the terminal with no input. Pig
// drives the same frames from tickSpinner through the owner loop. Every tick
// must write the next spinner frame and the new elapsed time; a spinner that
// only moves when a key invalidates the editor makes a working session look
// hung.
func TestTimerDrivenFramesReachTerminalWithoutInput(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				m.handleAgentEvent(agent.AgentStartEvent{})
				args, err := json.Marshal(map[string]any{"command": "sleep 600"})
				if err != nil {
					t.Fatal(err)
				}
				m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "t1", ToolName: "bash", Args: args})
				m.steerMessageWithImages("please also check the logs", nil)
				m.tuiInst.Render()
				screen := widthx.StripAnsi(term.take())
				for _, want := range []string{"Steering: please also check the logs", "to edit all queued messages", tui.DefaultSpinnerFrames[0] + " Working"} {
					if !strings.Contains(screen, want) {
						t.Fatalf("initial frame lacks %q:\n%s", want, screen)
					}
				}

				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan struct{})
				go func() { defer close(done); m.tickSpinner(ctx) }()
				lastElapsed := ""
				for tick := 1; tick <= 2*len(tui.DefaultSpinnerFrames); tick++ {
					time.Sleep(80 * time.Millisecond)
					synctest.Wait()
					m.drainMainLoopOnce()
					frame := widthx.StripAnsi(term.take())
					spinner := tui.DefaultSpinnerFrames[tick%len(tui.DefaultSpinnerFrames)] + " Working"
					if !strings.Contains(frame, spinner) {
						t.Fatalf("tick %d wrote no %q; the spinner is frozen until input. frame=%q", tick, spinner, frame)
					}
					elapsed := "Elapsed " + tui.FormatToolDuration(time.Duration(tick)*80*time.Millisecond)
					if elapsed != lastElapsed && !strings.Contains(frame, elapsed) {
						t.Fatalf("tick %d wrote no %q. frame=%q", tick, elapsed, frame)
					}
					lastElapsed = elapsed
				}
				cancel()
				<-done
			})
		})
	}
}

// Loader.restartAnimation uses Node's setInterval, which rearms from callback entry.
// A late UI dispatch must not leave the Go ticker on its earlier deadline grid.
func TestSpinnerCadenceSurvivesDispatchJitter(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				m.handleAgentEvent(agent.AgentStartEvent{})
				m.tuiInst.Render()
				term.take()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go m.tickSpinner(ctx)
				synctest.Wait()
				for tick := 1; tick <= 2*len(tui.DefaultSpinnerFrames); tick++ {
					// The previous callback ran 20ms late. Node's next callback is due 80ms after it, not on the old ticker's 60ms deadline.
					if tick%2 == 0 {
						time.Sleep(60 * time.Millisecond)
						synctest.Wait()
						if len(m.uiTaskCh) != 0 {
							t.Fatal("spinner queued an early callback after dispatch jitter")
						}
						time.Sleep(20 * time.Millisecond)
					} else {
						time.Sleep(100 * time.Millisecond)
					}
					synctest.Wait()
					m.drainMainLoopOnce()
					want := tui.DefaultSpinnerFrames[tick%len(tui.DefaultSpinnerFrames)] + " Working"
					if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, want) {
						t.Fatalf("tick %d after dispatch jitter wrote %q, want %q", tick, frame, want)
					}
				}
			})
		})
	}
}

// A queued callback belongs to the old indicator. Replacing it starts a fresh
// Loader interval; delivering the old callback must not postpone that interval.
func TestSpinnerReplacementKeepsItsFirstDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		m.handleAgentEvent(agent.AgentStartEvent{})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go m.tickSpinner(ctx)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		m.startWorkingLoader()
		m.tuiInst.Render()
		term.take()
		synctest.Wait()
		time.Sleep(20 * time.Millisecond)
		m.drainMainLoopOnce()
		if m.activeStatusIndicator.Frame != 0 {
			t.Fatal("old callback advanced the replacement early")
		}
		time.Sleep(60 * time.Millisecond)
		synctest.Wait()
		m.drainMainLoopOnce()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "⠙ Working") {
			t.Fatalf("old callback postponed the replacement's first frame: %q", frame)
		}
	})
}

// Status text set from a timer or an extension reaches the terminal without
// input. Pi's Loader.setMessage and setIndicator update the text and request a
// render; the retry countdown's setInterval relabels the loader every second.
// These updates may not advance a spinner frame (a hidden or single-frame
// indicator never ticks), so the relabel itself must dirty the status.
func TestStatusIndicatorRelabelsReachTerminalWithoutInput(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, term := newTickRenderProbe(t, mode)
				// settle runs the owner loop across d: posted status updates run,
				// then the throttled render they request fires and paints.
				settle := func(d time.Duration) string {
					synctest.Wait()
					m.drainMainLoopOnce()
					time.Sleep(d)
					synctest.Wait()
					m.drainMainLoopOnce()
					return widthx.StripAnsi(term.take())
				}
				m.handleAgentEvent(agent.AgentStartEvent{})
				m.tuiInst.Render()
				term.take()

				ui := &ExtUIContext{m: m}
				ui.SetWorkingIndicator(map[string]any{"frames": []string{"*"}})
				if frame := settle(100 * time.Millisecond); !strings.Contains(frame, "* Working") {
					t.Fatalf("custom indicator not painted: %q", frame)
				}
				ui.SetWorkingMessage("Indexing")
				if frame := settle(100 * time.Millisecond); !strings.Contains(frame, "* Indexing") {
					t.Fatalf("working message not painted: %q", frame)
				}
				ui.SetWorkingIndicator(map[string]any{"frames": []string{}})
				if frame := settle(100 * time.Millisecond); !strings.Contains(frame, "── Indexing") {
					t.Fatalf("hidden indicator not painted: %q", frame)
				}

				m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 3000})
				if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "Retrying (1/3) in 3s") {
					t.Fatalf("retry status not painted: %q", frame)
				}
				for _, remaining := range []string{"2s", "1s"} {
					if frame := settle(time.Second); !strings.Contains(frame, "Retrying (1/3) in "+remaining) {
						t.Fatalf("retry countdown did not reach %s: %q", remaining, frame)
					}
				}
				m.handleAgentEvent(agent.AutoRetryEndEvent{Success: true})
			})
		})
	}
}

// Footer changes made off the owner loop reach the terminal while the session
// is idle, when no spinner tick repaints the screen. Pi's setExtensionStatus
// and its git-branch watcher both call ui.requestRender().
func TestBackgroundFooterChangesReachTerminalWhileIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		m.isIdle = true
		m.tuiInst.Render()
		term.take()

		ui := &ExtUIContext{m: m}
		timer := time.AfterFunc(time.Second, func() { ui.SetStatus("indexer", "indexed 3/10") })
		defer timer.Stop()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		m.drainMainLoopOnce()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "indexed 3/10") {
			t.Fatalf("extension status set from a timer was not painted: %q", frame)
		}

		inProcess := NewTUIUIContext(m.tuiInst)
		inProcess.interactiveMode = m
		inProcessTimer := time.AfterFunc(time.Second, func() { inProcess.SetStatus("indexer", "indexed 4/10") })
		defer inProcessTimer.Stop()
		time.Sleep(2 * time.Second)
		synctest.Wait()
		m.drainMainLoopOnce()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "indexed 4/10") {
			t.Fatalf("in-process extension status set from a timer was not painted: %q", frame)
		}
	})
}

// A branch switch made outside pig reaches the idle footer. Pi's footer data
// provider notifies onBranchChange subscribers and interactive mode requests a
// render; pig's watcher only invalidated the footer, so the new branch showed
// on the next keystroke.
func TestGitBranchChangeReachesTerminalWhileIdle(t *testing.T) {
	repo := t.TempDir()
	// The footer abbreviates the home directory; keep the repo path short so
	// the branch suffix fits the 80-column probe. Windows reads the home
	// directory from USERPROFILE.
	t.Setenv("HOME", filepath.Dir(repo))
	t.Setenv("USERPROFILE", filepath.Dir(repo))
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "trunk")
	git("-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "one")

	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		m.opts.CWD = repo
		m.statusLine.SetCwd(repo)
		m.isIdle = true
		m.tuiInst.Render()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "(trunk)") {
			t.Fatalf("initial footer lacks the branch: %q", frame)
		}

		ctx, cancel := context.WithCancel(t.Context())
		m.startGitBranchWatcher(ctx)
		git("checkout", "-q", "-b", "feature")
		time.Sleep(6 * time.Second) // past one HEAD poll
		synctest.Wait()
		m.drainMainLoopOnce()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "(feature)") {
			t.Fatalf("the idle footer did not show the new branch without input: %q", frame)
		}
		cancel()
	})
}

// Every retry countdown second reaches the screen, in order, even when the
// owner loop stalls behind a full UI task queue. Upstream CountdownTimer's
// setInterval callback runs on the event loop for each second (late under load,
// never skipped), decrementing and relabelling each time.
func TestRetryCountdownSkipsNoSecondUnderSaturatedQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		m.handleAgentEvent(agent.AgentStartEvent{})
		m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 5000})
		screen := widthx.StripAnsi(term.take())

		// The owner loop stalls for 3s with the task queue full.
		for len(m.uiTaskCh) < cap(m.uiTaskCh) {
			m.postUITask(func() {})
		}
		time.Sleep(3 * time.Second)
		synctest.Wait()
		for range 8 {
			m.drainMainLoopOnce()
			screen += widthx.StripAnsi(term.take())
			time.Sleep(time.Second)
			synctest.Wait()
		}
		m.handleAgentEvent(agent.AutoRetryEndEvent{Success: true})

		rest := screen
		for _, second := range []string{"5s", "4s", "3s", "2s", "1s"} {
			label := "Retrying (1/3) in " + second
			_, after, ok := strings.Cut(rest, label)
			if !ok {
				t.Fatalf("countdown skipped or reordered %q; painted:\n%s", label, screen)
			}
			rest = after
		}
	})
}

// A countdown waiting on a full queue exits as soon as a new status replaces
// it, without waiting for the owner loop to catch up.
func TestRetryCountdownWaitingOnFullQueueStopsWithItsStatus(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newTickRenderProbe(t, "regular")
		// Only the countdown's own stop may release it: a waiting sender left
		// behind fails the bubble with a blocked goroutine.
		m.runCtx = context.Background()
		m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 5000})
		for len(m.uiTaskCh) < cap(m.uiTaskCh) {
			m.postUITask(func() {})
		}
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		m.handleAgentEvent(agent.CompactionStartEvent{Reason: "manual"})
		synctest.Wait()
		if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "compaction" {
			t.Fatal("a stopped countdown's second overwrote the replacement status")
		}
	})
}
