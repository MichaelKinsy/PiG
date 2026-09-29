package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// system-prompt-updates.test.ts:114-171: run sections are recorded as patches and removed by an unmodified later run, including a caller-supplied preamble.
func TestSessionBeforeAgentStartSectionsArePerRun(t *testing.T) {
	for _, custom := range []string{"", "configured instructions"} {
		t.Run(custom, func(t *testing.T) {
			turn := 0
			runner := inproc.NewRunner([]extension.Extension{{Handlers: map[string][]extension.HandlerFn{"before_agent_start": {func(args ...any) (any, error) {
				turn++
				if turn == 2 {
					options := args[0].(extension.BeforeAgentStartEvent).SystemPromptOptions
					*options.Sections = append(*options.Sections, ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")})
				}
				return nil, nil
			}}}}}, t.TempDir())
			provider := &scriptedProvider{responses: []scriptedResponse{fauxReply("one", ai.StopReasonStop, 0), fauxReply("two", ai.StopReasonStop, 0), fauxReply("three", ai.StopReasonStop, 0)}}
			services := newTestServices(t)
			if err := services.Auth().Set(provider.ID(), ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
				t.Fatal(err)
			}
			session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(provider), Runner: runner, SystemPrompt: custom, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			drainSessionEvents(t, session)
			t.Cleanup(func() { _ = session.Close() })
			for _, prompt := range []string{"first", "second", "third"} {
				if _, err := session.Prompt(t.Context(), prompt); err != nil {
					t.Fatal(err)
				}
			}
			var patches []ai.OrderedSections
			for _, message := range session.Messages() {
				if message.System != nil {
					patches = append(patches, message.System.Sections)
				}
			}
			if len(patches) != 3 || len(patches[1]) != 1 || patches[1][0].Name != "plan_mode" || patches[1][0].Value == nil || *patches[1][0].Value != "<plan_mode>\nPlan only.\n</plan_mode>" || len(patches[2]) != 1 || patches[2][0].Name != "plan_mode" || patches[2][0].Value != nil {
				t.Fatalf("patches=%+v", patches)
			}
			if sections := session.GetSystemPromptOptions().Sections; sections != nil && len(*sections) != 0 {
				t.Fatal("per-run mutation reached the base options")
			}
		})
	}
}

// Invalid section names fail before any agent run or persistence; base-option sections also apply when no extension handler is registered.
func TestPreparedPromptSectionValidationAndBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	base := ai.OrderedSections{{Name: "preamble", Value: new("invalid")}}
	h.session.GetSystemPromptOptions().Sections = &base
	if _, err := h.session.Prompt(t.Context(), "rejected"); err == nil || err.Error() != "Invalid system prompt section name: preamble" {
		t.Fatalf("validation=%v", err)
	}
	if len(h.session.Messages()) != 0 {
		t.Fatal("invalid prompt changed Session history")
	}
	base[0] = ai.PromptSection{Name: "plan_mode", Value: new("Plan only.")}
	request := context9789Capture(h, "done")
	context9789Prompt(t, h, "hello")
	if prompt := ai.GetCurrentSystemPrompt(request(t)); !strings.Contains(prompt, "<plan_mode>\nPlan only.\n</plan_mode>") {
		t.Fatalf("base option absent from provider request: %q", prompt)
	}
}
