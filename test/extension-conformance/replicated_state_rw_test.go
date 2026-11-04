package extensionconformance

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// replicatedStateCases are every realization of an SDK the row compares: isolated, packed and fused.
func replicatedStateCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// newReplicatedStateSession builds a Session over the harness's extension runner and a faux model, and binds the host actions an SDK reads and writes its replicated state through, as every mode does (cmd/pig bindSessionExtensionActions, coding.BindExtensionHostSessionActions).
func newReplicatedStateSession(t *testing.T, h *harness) (*coding.Session, *ai.FauxProviderHandle) {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	configureFauxAuth(t, services)
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: h.runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	h.runner.AddErrorListener(func(err *extension.ExtensionError) { t.Logf("extension error: %s: %s", err.Event, err.Error) })
	events := make(chan struct{})
	eventsCh := session.Events()
	go func() {
		defer close(events)
		for event := range eventsCh {
			coding.AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-events })
	coding.BindExtensionHostSessionActions(h.bridge, func() *coding.Session { return session })
	h.bridge.SetHostAction("refreshTools", session.RefreshTools)
	h.bridge.SetHostAction("isIdle", session.IsIdle)
	h.bridge.SetHostAction("getSystemPrompt", session.SystemPrompt)
	h.bridge.SetHostAction("getSystemPromptOptions", func() extension.BuildSystemPromptOptions { return *session.GetSystemPromptOptions() })
	if err := session.BindExtensions(t.Context(), coding.ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	return session, faux
}

// runReplicatedStateRow runs body for every realization against a Session over the harness.
func runReplicatedStateRow(t *testing.T, body func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range replicatedStateCases() {
		t.Run(tc.name, func(t *testing.T) { runReplicatedStateCase(t, tc, body) })
	}
}

// runReplicatedStateCase runs body for one realization against a Session over the harness.
func runReplicatedStateCase(t *testing.T, tc harnessCase, body func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle)) {
	t.Helper()
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
	session, faux := newReplicatedStateSession(t, h)
	h.ui.ClearRecorded()
	body(t, h, session, faux)
}

// reportedLine waits for the notification of the SDK that starts with prefix and returns it without its level.
func reportedLine(t *testing.T, h *harness, prefix string) string {
	t.Helper()
	var line string
	pollUntilConformance(t, "the SDK never reported "+prefix, func() bool {
		for _, recorded := range h.ui.Recorded() {
			if strings.HasPrefix(recorded, prefix) {
				line = strings.TrimSuffix(recorded, ":info")
				return true
			}
		}
		return false
	})
	return line
}

// Pi's ctx.getSystemPrompt() and ctx.getSystemPromptOptions() read the Session (agent-session.ts:1474-1476, 3495-3496), and setActiveTools and registerTool rebuild the prompt before they return (agent-session.ts:1523-1536, 1705-1731), so a read in the same handler answers the write before it. Every SDK registers two tools with prompt snippets, activates one, and reads after each write. The tool names and snippets are values none of the SDKs holds as a default, so an SDK that answers from a snapshot taken before the handler reports another line.
func TestReplicatedStatePromptReadsAnswerWritesInTheSameHandler(t *testing.T) {
	const want = "rw_prompt_state=registered:rw-alpha-snippet;selected:rw-beta;beta:true;alpha:false"
	runReplicatedStateRow(t, func(t *testing.T, h *harness, _ *coding.Session, _ *ai.FauxProviderHandle) {
		runConformanceCommand(t, h, "rw_prompt_state")
		if got := reportedLine(t, h, "rw_prompt_state="); got != want {
			t.Fatalf("the handler read %q, want %q", got, want)
		}
	})
}

// Pi's sendCustomMessage with triggerTurn awaits the run it starts (agent-session.ts:2344-2349 -> _runAgentPrompt), so ctx.isIdle() right after is false. The run's only response waits for the test, so the read cannot see a run that already ended.
func TestReplicatedStateIsIdleAfterAMessageThatStartsARun(t *testing.T) {
	runReplicatedStateRow(t, func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle) {
		release := make(chan struct{})
		faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				<-release
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
			}),
		})
		runConformanceCommand(t, h, "rw_idle_after_trigger")
		got := reportedLine(t, h, "rw_idle_after_trigger=")
		close(release)
		pollUntilConformance(t, "the run the message started never ended", session.IsIdle)
		if want := "rw_idle_after_trigger=false"; got != want {
			t.Fatalf("the handler read %q, want %q", got, want)
		}
	})
}

// Pi's sendCustomMessage with triggerTurn marks the Session busy (_startRun sets _isAgentRunActive before its first await, agent-session.ts:1895, 2404), and the handler's next statements run before anything else on Pi's one event loop, so ctx.isIdle() reads busy through the handler's synchronous part whatever the run it started would do. The host runs that run concurrently with the extension, and a run that settles while a read is in flight answers idle: the host below answers idle at every read, as that run does, so only an SDK that holds the busy answer itself reads false right after the send. Pi's loop runs the run's continuations once the handler awaits, so a read after `await ctx.waitForIdle()` is live: the host's answer, true. A constant (true, false or a fallback) or a snapshot never reports sync:false,false;later:true. Print and RPC mode, every isolated, packed and fused realization.
//
// The code that sent the message keeps the loop until it awaits or returns, so the answer ends where that code yields, and nowhere before:
//   - rw_idle_busy_until_yield: two reads right after the send are busy; a read after `await ctx.waitForIdle()` is live.
//   - rw_idle_send_then_await: the handler awaits the run with no read between the send and the await, so the hold has to end at the await however the host orders it against the send (true), reads right after a second send are busy again (false), and a read after the second await is live (true). An SDK that never holds, that never releases, or that answers a constant reports another line.
//   - rw_idle_blocking: the handler blocks on something that is no host call (a sleep) while the run ends, and reads: busy, as Pi's synchronous wait is, whatever the Session says by then. The Session itself answers here: the run, which needs no extension and finishes within the sleep, ends meanwhile, so a hold that ended when the triggered turn started (the Session is busy from the send, so releasing there leaves nothing but the Session's own answer) reports true.
//   - rw_idle_yield_poll: the handler polls `while !isIdle: sleep` against the Session's own answer, with the SDK's sleep helper that yields first (ctx.Sleep, ctx.sleep, ctx-sleep; Node awaits a timer): the first read after the send is busy, and the poll sees idle and returns once the run ends. An SDK whose helper does not yield stays busy until its deadline and reports final:false.
//   - rw_idle_callback: the send comes from a callback (a timer's in Node, a goroutine or thread elsewhere) that the handler waits for; its reads are busy, and a read after its own await is live. A callback that runs after the handler returned holds only in Node, whose event loop reports where it yields (TestNodeHoldAfterATriggeringSendEndsWhereTheSendingCodeYields).
func TestReplicatedStateIsIdleStaysBusyThroughTheSynchronousRunAfterATriggeringSend(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	rows := []struct {
		command string
		want    string
		runs    int
		// liveSession leaves isIdle bound to the Session: the row reads the answer a real run gives, not one the host fakes.
		liveSession bool
	}{
		{command: "rw_idle_busy_until_yield", want: "rw_idle_busy_until_yield=sync:false,false;later:true", runs: 1},
		{command: "rw_idle_send_then_await", want: "rw_idle_send_then_await=awaited:true;second:false;awaited-again:true", runs: 2},
		{command: "rw_idle_blocking", want: "rw_idle_blocking=before:true;after:false", runs: 1, liveSession: true},
		{command: "rw_idle_yield_poll", want: "rw_idle_yield_poll=first:false;final:true", runs: 1, liveSession: true},
		{command: "rw_idle_callback", want: "rw_idle_callback=before:true;sync:false,false;later:true", runs: 1},
	}
	// rpcRowMode is read by every harness factory: this test is not parallel, so it owns the value while it runs.
	for _, mode := range []string{"print", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			rpcRowMode = mode
			t.Cleanup(func() { rpcRowMode = "" })
			for _, tc := range replicatedStateCases() {
				t.Run(tc.name, func(t *testing.T) {
					for _, row := range rows {
						t.Run(row.command, func(t *testing.T) {
							runReplicatedStateCase(t, tc, func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle) {
								steps := make([]ai.FauxResponseStep, row.runs)
								for i := range steps {
									steps[i] = ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"})
								}
								faux.SetResponses(steps)
								if !row.liveSession {
									h.bridge.SetHostAction("isIdle", func() bool { return true })
								}
								runConformanceCommand(t, h, row.command)
								got := reportedLine(t, h, row.command+"=")
								pollUntilConformance(t, "the run the message started never ended", session.IsIdle)
								if got != row.want {
									t.Fatalf("the code read %q, want %q", got, row.want)
								}
							})
						})
					}
				})
			}
		})
	}
}

// Pi holds a custom message sent while the agent_settled handlers run until they return (agent-session.ts:2344-2349), so the Session is still idle for the rest of the handler: ctx.isIdle() right after a triggering sendMessage is true.
func TestReplicatedStateIsIdleAfterAMessageFromAgentSettled(t *testing.T) {
	runReplicatedStateRow(t, func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle) {
		faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("first")}, StopReason: "stop"}),
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("second")}, StopReason: "stop"}),
		})
		runConformanceCommand(t, h, "rw_idle_in_settled")
		if err := session.Prompt(t.Context(), "go"); err != nil {
			t.Fatal(err)
		}
		if got, want := reportedLine(t, h, "rw_idle_in_settled="), "rw_idle_in_settled=true"; got != want {
			t.Fatalf("the handler read %q, want %q", got, want)
		}
	})
}

// Pi's runner.ts createContext reads the Session's isIdle live on every call (agent-session.ts isIdle), so a callback that runs after the extension's last subscribed event reads the Session as it is then. agent_end fires while the run is still busy and no event follows it, so an SDK that answers from a snapshot pushed with the last event keeps reporting a busy Session after the run ended. Every SDK polls isIdle from a background callback it starts in its agent_end handler and reports the last answer.
func TestReplicatedStateIsIdleIsLiveInABackgroundCallback(t *testing.T) {
	runReplicatedStateRow(t, func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle) {
		faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
		})
		runConformanceCommand(t, h, "rw_idle_background")
		if err := session.Prompt(t.Context(), "go"); err != nil {
			t.Fatal(err)
		}
		if got, want := reportedLine(t, h, "rw_idle_background="), "rw_idle_background=true"; got != want {
			t.Fatalf("the background callback read %q, want %q", got, want)
		}
	})
}

// Pi's runner.ts createContext reads the Session's isIdle live on every call (agent-session.ts isIdle), so an SDK reads the Session as it is when the call runs, with no event in between. The command starts a poll that subscribes to nothing and notifies the sequence of answers it has seen at each change. The run's only response waits for the test, which advances only once the poll has reported the previous answer: idle before the run, busy while it is held, idle once it ends. An SDK that answers from a snapshot or with a constant (a fallback, or a value it never refreshes) never reports the whole sequence.
func TestReplicatedStateIsIdleLiveSequenceAcrossSDKs(t *testing.T) {
	runReplicatedStateRow(t, func(t *testing.T, h *harness, session *coding.Session, faux *ai.FauxProviderHandle) {
		release := make(chan struct{})
		faux.SetResponses([]ai.FauxResponseStep{
			ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
				<-release
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}.AssistantMessage(), nil
			}),
		})
		waitForLine := func(want string) {
			t.Helper()
			pollUntilConformance(t, "the SDK never reported "+want, func() bool {
				for _, recorded := range h.ui.Recorded() {
					if strings.TrimSuffix(recorded, ":info") == want {
						return true
					}
				}
				return false
			})
		}
		runConformanceCommand(t, h, "rw_idle_live")
		waitForLine("rw_idle_live=true")
		prompted := make(chan error, 1)
		go func() { prompted <- session.Prompt(t.Context(), "go") }()
		waitForLine("rw_idle_live=true,false")
		close(release)
		if err := <-prompted; err != nil {
			t.Fatal(err)
		}
		waitForLine("rw_idle_live=true,false,true")
	})
}
