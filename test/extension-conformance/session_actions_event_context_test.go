package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestConformance_CommandOnlySessionActionsRefusedFromEventContext pins Pi v1.5.2's runner.ts:1004-1038: waitForIdle, newSession, fork, navigateTree, switchSession and
// reload exist only on createCommandContext(). The ExtensionContext every event, tool and shortcut handler receives (createContext(), runner.ts:877-987) has no such member,
// so a handler that calls one gets a TypeError ("ctx.switchSession is not a function") in its own handler and the session carries on. Each SDK's handler must see that
// refusal, and the host action must never run, where the same calls from a command (and waitForIdle from a withSession callback) run it. waitForIdle is the one whose
// refusal also keeps a run alive: a handler that waits for the run it is part of (a tool_call or agent_end handler) waits for itself.
func TestConformance_CommandOnlySessionActionsRefusedFromEventContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			var mu sync.Mutex
			var calls []string
			record := func(name string) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, name)
			}
			// waitRuns counts the waitForIdle calls the host ran; the wait of a command holds in the gate until the test releases it.
			var waitRuns atomic.Int32
			var gate atomic.Pointer[chan struct{}]
			entered := make(chan struct{}, 1)
			h.bridge.SetHostAction("waitForIdle", func(ctx context.Context) error {
				waitRuns.Add(1)
				if g := gate.Load(); g != nil {
					entered <- struct{}{}
					select {
					case <-*g:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return nil
			})
			h.bridge.SetHostAction("newSession", func(context.Context, *extension.NewSessionOptions) (extension.CancelledResult, error) {
				record("new")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("fork", func(context.Context, string, *extension.ForkOptions) (extension.CancelledResult, error) {
				record("fork")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("navigateTree", func(context.Context, string, *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
				record("navigate")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("switchSession", func(context.Context, string, *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
				record("switch")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("reload", func(context.Context) error {
				record("reload")
				return nil
			})

			// noticeWith is the last notice that starts with prefix, "" before there is one.
			noticeWith := func(prefix string) string {
				var got string
				for _, notice := range h.ui.Recorded() {
					if strings.HasPrefix(notice, prefix) {
						got = notice
					}
				}
				return got
			}
			noHostActions := func(what string) {
				t.Helper()
				mu.Lock()
				ran := slices.Clone(calls)
				mu.Unlock()
				if len(ran) != 0 {
					t.Fatalf("host session actions ran from %s: %v", what, ran)
				}
				if got := waitRuns.Load(); got != 0 {
					t.Fatalf("the host waited for the run %d time(s) for %s", got, what)
				}
			}

			// Pi's JavaScript shows no such member on an event context: typeof is "undefined" and a call throws the TypeError of a missing function. The Node runtime does not
			// hand the members to an event context at all; the other SDKs have one static Context type, so the host's refusal (D617) is all they can show.
			const wantShape = "undefined,undefined,undefined,undefined,undefined,undefined;TypeError:ctx.waitForIdle is not a function;TypeError:ctx.switchSession is not a function:info"
			const wantActions = "wait=rejected,new=rejected,fork=rejected,navigate=rejected,switch=rejected,reload=rejected:info"
			isNode := strings.Contains(tc.name, "node")

			// A handler of each kind of event: session_start runs while the session is idle (the call would have returned at once), tool_call and agent_end run inside the run
			// (the call waited for the run that waits for it).
			emits := []struct {
				event string
				emit  func() error
			}{
				{"session_start", func() error {
					_, err := h.runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"})
					return err
				}},
				{"tool_call", func() error {
					_, err := h.runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: "c1", ToolName: "probe", Input: map[string]any{}})
					return err
				}},
				{"agent_end", func() error {
					_, err := h.runner.Emit(t.Context(), extension.AgentEndEvent{Type: "agent_end"})
					return err
				}},
			}
			for _, step := range emits {
				h.ui.ClearRecorded()
				runConformanceCommandArgs(t, h, "event_session_actions_on", step.event)
				if err := step.emit(); err != nil {
					t.Fatalf("%s emit: %v", step.event, err)
				}
				pollUntilConformance(t, "the SDK's "+step.event+" handler never reported its session actions", func() bool {
					return noticeWith("event_actions:") != ""
				})
				if got := noticeWith("event_actions:"); got != "event_actions:"+wantActions {
					t.Fatalf("session actions from a %s handler answered %q, want %q", step.event, got, "event_actions:"+wantActions)
				}
				if isNode {
					if got := noticeWith("event_shape:"); got != "event_shape:"+wantShape {
						t.Fatalf("a %s context in the Node runtime shows %q, want %q", step.event, got, "event_shape:"+wantShape)
					}
				}
				noHostActions("a " + step.event + " handler")
			}

			// The handler's request has ended. A context it kept is still an event context: Pi's createContext() object never gains the members, so a call through it
			// later is refused as well, and no host action runs.
			h.ui.ClearRecorded()
			runConformanceCommand(t, h, "event_session_actions_stored")
			pollUntilConformance(t, "the SDK never reported the session actions of the event context a handler kept", func() bool {
				return noticeWith("stored_actions:") != ""
			})
			if got := noticeWith("stored_actions:"); got != "stored_actions:"+wantActions {
				t.Fatalf("session actions through an event context kept after its request ended answered %q, want %q", got, "stored_actions:"+wantActions)
			}
			if isNode {
				if got := noticeWith("stored_shape:"); got != "stored_shape:"+wantShape {
					t.Fatalf("a kept event context in the Node runtime shows %q, want %q", got, "stored_shape:"+wantShape)
				}
			}
			noHostActions("an event context kept after its request ended")

			// A tool's execute context is createContext() as well.
			tool, ok := h.runner.GetToolDefinition("session_actions_tool")
			if !ok {
				t.Fatal("session_actions_tool is not registered")
			}
			result, err := tool.Execute(t.Context(), "call", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatalf("session_actions_tool: %v", err)
			}
			if got, want := result.Text(), "tool_actions:"+strings.TrimSuffix(wantActions, ":info"); got != want {
				t.Fatalf("session actions from a tool's execute answered %q, want %q", got, want)
			}
			noHostActions("a tool's execute context")

			// From a command context the wait is served, and the call returns when the host's wait does: the command has not finished while the run it waits for has not.
			h.ui.ClearRecorded()
			release := make(chan struct{})
			gate.Store(&release)
			commandDone := make(chan error, 1)
			cmd, ok := findCommand(h.runner, "wait_idle_command")
			if !ok {
				t.Fatal("wait_idle_command is not registered")
			}
			go func() { commandDone <- cmd.Handler(context.Background(), "") }()
			select {
			case <-entered:
			case err := <-commandDone:
				t.Fatalf("wait_idle_command returned (%v) without its waitForIdle reaching the host; notice %q", err, noticeWith("wait_idle:"))
			}
			if got := noticeWith("wait_idle:"); got != "" {
				t.Fatalf("the command reported %q before the host's wait returned", got)
			}
			close(release)
			if err := <-commandDone; err != nil {
				t.Fatalf("wait_idle_command: %v", err)
			}
			pollUntilConformance(t, "the command never reported its wait", func() bool { return noticeWith("wait_idle:") != "" })
			if got := noticeWith("wait_idle:"); got != "wait_idle:done:info" {
				t.Fatalf("a command's waitForIdle answered %q, want %q", got, "wait_idle:done:info")
			}
			gate.Store(nil)

			// From a withSession callback, whose context is the replacement Session's command context, the wait is served by the replacement Session's host.
			h.host.SetReplacementHost(func() *subprocess.Host { return h.host })
			h.bridge.SetHostAction("switchSession", func(_ context.Context, _ string, options *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
				if options == nil || options.WithSession == nil {
					return extension.CancelledResult{}, nil
				}
				return extension.CancelledResult{}, options.WithSession(&extension.ReplacedSessionContext{})
			})
			h.ui.ClearRecorded()
			before := waitRuns.Load()
			runConformanceCommand(t, h, "wait_idle_with_session")
			pollUntilConformance(t, "the withSession callback never reported its wait", func() bool { return noticeWith("with_session_wait:") != "" })
			if got := noticeWith("with_session_wait:"); got != "with_session_wait:done:info" {
				t.Fatalf("a withSession callback's waitForIdle answered %q, want %q", got, "with_session_wait:done:info")
			}
			if got := waitRuns.Load(); got != before+1 {
				t.Fatalf("a withSession callback's waitForIdle reached the host %d time(s), want 1", got-before)
			}

			// The same calls from a command context run every host action.
			mu.Lock()
			calls = nil
			mu.Unlock()
			h.bridge.SetHostAction("switchSession", func(context.Context, string, *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
				record("switch")
				return extension.CancelledResult{}, nil
			})
			runConformanceCommand(t, h, "session_actions_unbound")
			pollUntilConformance(t, "the command's session actions never reached the host", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(calls) >= 5
			})
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"new", "fork", "navigate", "switch", "reload"}; !slices.Equal(calls, want) {
				t.Fatalf("a command's session actions reached the host as %v, want %v", calls, want)
			}
		})
	}
}
