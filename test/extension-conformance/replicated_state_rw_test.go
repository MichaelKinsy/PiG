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
	go func() {
		defer close(events)
		for event := range session.Events() {
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
			session, faux := newReplicatedStateSession(t, h)
			h.ui.ClearRecorded()
			body(t, h, session, faux)
		})
	}
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
