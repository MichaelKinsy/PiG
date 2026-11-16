package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// agentAlreadyProcessing is Pi's refusal of a prompt that names no streamingBehavior while the agent streams (agent-session.ts:2241-2244).
const agentAlreadyProcessing = "Agent is already processing. Specify streamingBehavior ('steer' or 'followUp') to queue the message."

// TestSendUserMessageDeliveryAcrossSDKs pins what pi.sendUserMessage(content, options?) does with each delivery an extension names, or none, in both agent
// states, as the production Session answers it in every SDK, isolated and packed (types.ts:1790-1792; agent-session.ts:2545-2573 sendUserMessage passes
// `streamingBehavior: options?.deliverAs` to prompt(), which refuses a streaming prompt without one at :2240-2244, and the bound action at :3565-3572
// reports that refusal as the extension error event `send_user_message`):
//
//   - idle, any delivery: the message runs at once as a user turn; nothing is queued and nothing is reported;
//   - streaming, no delivery: nothing is queued, the extension error `send_user_message` carries the refusal above and the message never reaches the
//     transcript; the extension's call itself returns without an error, as Pi's void call does;
//   - streaming, steer: one steering message is queued and nothing is reported;
//   - streaming, followUp: one follow-up is queued and nothing is reported.
//
// The command names its delivery as absent (the author gives none: no option member in Node, "" in Go and Rust, no argument in Python and Lisp), steer or
// followUp. An SDK that fills in a follow-up when the author names none queues the message and reports nothing, which is not the refusal the streaming
// absent row expects (Python and Lisp did before, with the default `followUp`).
func TestSendUserMessageDeliveryAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
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
			for _, streaming := range []bool{false, true} {
				for _, mode := range []string{"absent", "steer", "followUp"} {
					state := "idle"
					if streaming {
						state = "streaming"
					}
					t.Run(state+"/"+mode, func(t *testing.T) { checkSendUserMessageDelivery(t, h, mode, streaming) })
				}
			}
		})
	}
}

func checkSendUserMessageDelivery(t *testing.T, h *harness, mode string, streaming bool) {
	t.Helper()
	text := "delivery-" + mode
	gate, started := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)

	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	reply := func() ai.FauxResponseStep {
		return ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("reply")}, ai.FauxAssistantMessageOptions{}), nil
		})
	}
	steps := []ai.FauxResponseStep{reply(), reply(), reply()}
	if streaming {
		hold := ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
			startedOnce.Do(func() { close(started) })
			select {
			case <-gate:
			case <-t.Context().Done():
			}
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("first reply")}, ai.FauxAssistantMessageOptions{}), nil
		})
		steps = append([]ai.FauxResponseStep{hold}, steps...)
	}
	provider.SetResponses(steps)

	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	configureFauxAuth(t, services)
	runner := inproc.NewRunner(nil, t.TempDir())
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 1000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	var mu sync.Mutex
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, err.Event+": "+err.Error)
	})
	reports := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(reported)
	}
	// The host action is the production Session's: what the Session binds for a subprocess extension (session_extension_host.go).
	h.bridge.SetHostAction("sendUserMessage", func(content any, options subprocess.SendUserMessageOptions) error {
		return session.SendExtensionUserMessage(content, &extension.SendUserMessageOptions{DeliverAs: extension.DeliverAs(options.DeliverAs), ExpandPromptTemplates: options.ExpandPromptTemplates})
	})

	prompted := make(chan error, 1)
	if streaming {
		go func() { prompted <- session.Prompt(t.Context(), "first") }()
		<-started
		if !session.IsStreaming() {
			t.Fatal("the model call is in flight but the Session is not streaming")
		}
	}

	command, ok := findCommand(h.runner, "send_user_message_delivery")
	if !ok {
		t.Fatal("send_user_message_delivery command missing")
	}
	if err := command.Handler(context.Background(), mode); err != nil {
		t.Fatalf("the extension's call returned %q, want no error: Pi's sendUserMessage returns void and reports a refusal as an extension error", err)
	}

	inTranscript := func() bool {
		for _, entry := range session.Entries() {
			raw := string(entry.Raw())
			if strings.Contains(raw, `"role":"user"`) && strings.Contains(raw, `"`+text+`"`) {
				return true
			}
		}
		return false
	}
	if !streaming {
		pollUntilConformance(t, "the Session never ran "+text+" as a user turn", func() bool { return inTranscript() && !session.IsStreaming() })
		if got := reports(); len(got) != 0 {
			t.Fatalf("extension errors %q for an idle send, want none", got)
		}
		if n := session.PendingMessageCount(); n != 0 {
			t.Fatalf("%d messages queued by an idle send, want none", n)
		}
		return
	}

	// Streaming: the host call has an effect either way, an error event or a queued message; wait for whichever comes.
	pollUntilConformance(t, "a streaming "+mode+" send had no effect: no extension error and nothing queued", func() bool {
		return len(reports()) > 0 || session.PendingMessageCount() > 0
	})
	steering, followUps, errs := session.GetSteeringMessages(), session.GetFollowUpMessages(), reports()
	var wantSteering, wantFollowUps, wantErrs []string
	switch mode {
	case "absent":
		wantErrs = []string{"send_user_message: " + agentAlreadyProcessing}
	case "steer":
		wantSteering = []string{text}
	case "followUp":
		wantFollowUps = []string{text}
	}
	if !slices.Equal(steering, wantSteering) || !slices.Equal(followUps, wantFollowUps) || !slices.Equal(errs, wantErrs) {
		t.Fatalf("streaming %s send: steering %q, follow-ups %q, extension errors %q; want %q, %q, %q", mode, steering, followUps, errs, wantSteering, wantFollowUps, wantErrs)
	}

	release()
	if err := <-prompted; err != nil {
		t.Fatalf("the streaming prompt: %v", err)
	}
	if mode == "absent" {
		if inTranscript() {
			t.Fatalf("the refused %s reached the transcript", text)
		}
		return
	}
	pollUntilConformance(t, "the queued "+text+" was never delivered", func() bool { return inTranscript() && session.PendingMessageCount() == 0 && !session.IsStreaming() })
}
