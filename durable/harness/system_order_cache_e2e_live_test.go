//go:build live

// Ports packages/durable/test/system-order-cache-e2e.test.ts.

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// systemOrderInstructions is the repeated, nonce-keyed instruction text (system-order-cache-e2e.test.ts:28-39).
func systemOrderInstructions(nonce string) string {
	lines := []string{
		"This is an automated prompt-cache test. The repeated context below is intentional.",
		"Never call tools. Reply with exactly the text the user asks for.",
	}
	for index := range 250 {
		lines = append(lines, fmt.Sprintf("%s cache record %03d: alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega.", nonce, index))
	}
	return strings.Join(lines, "\n")
}

// cacheReadAfterToolChange is the share of the prompt the second turn read from cache after adding a tool
// (system-order-cache-e2e.test.ts:66-125).
func cacheReadAfterToolChange(t *testing.T, token string, legacyOrder bool) float64 {
	t.Helper()
	models := &providerSessionCacheModels{Models: ai.BuiltinModels(), token: token}
	registry := CreateRegistry()
	parameters := map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}}
	noop := func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}}, nil
	}
	first := ownTool("probe_first", "Unused probe tool.", parameters, noop)
	second := ownTool("probe_second", "Unused probe tool added on the second turn.", parameters, noop)
	probe := new(durable.Extension{Name: "probe", Tools: []*durable.ToolRegistration{first, second}})
	legacy := new(durable.Extension{Name: "legacy-order", Hooks: []durable.HookRegistration{{Task: GenerationTask.AnyDefinition().Name, Handlers: &GenerationHooks{
		BeforeRequest: func(_ context.Context, request GenerationRequest, _ HookApi) (*GenerationRequest, error) {
			return &GenerationRequest{Messages: committedOrder(request.Messages)}, nil
		},
	}}}})
	mustInstall(t, registry, probe)
	mustInstall(t, registry, legacy)
	harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
		Models:   models,
		Registry: registry,
		Settings: func() *HarnessSettings {
			return &HarnessSettings{Compaction: &CompactionPolicyPatch{Enabled: new(false)}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeHarness(t, harness)
	nonce, err := ai.UUIDv7(nil)
	if err != nil {
		t.Fatal(err)
	}
	extensions := []*durable.Extension{probe}
	if legacyOrder {
		extensions = append(extensions, legacy)
	}
	root, err := harness.Root(testContext, &RootOptions{Agent: &AgentChange{
		Model:         SetTo(durable.ModelRef{Provider: "anthropic", ModelId: "claude-sonnet-5-5"}),
		ThinkingLevel: SetTo(ai.ModelThinkingLevel("low")),
		Instructions:  SetTo(systemOrderInstructions(nonce)),
		Extensions:    SetTo(ExtensionChange{Exact: true, List: extensions}),
		Tools:         SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{first}}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	harness.Resume()
	ask := func(text string) ai.AssistantMessage {
		settled := must(submitInput(t, root, "Reply exactly: "+text).Wait(testContext))
		if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
			t.Fatalf("Unexpected %s", settled.Status)
		}
		answer := commitValue(t, root, func(tx durable.Tx) (*durable.TypedEntry[durable.Never], error) {
			return durable.TxEntry(tx, durable.AssistantEntry, *settled.Answer)
		})
		message, ok := answer.Model[0].(ai.AssistantMessage)
		if !ok || message.StopReason != ai.StopReasonStop {
			t.Fatalf("answer = %#v, want stopReason stop", answer.Model[0])
		}
		return message
	}
	before := ask("READY")
	if prompt := before.Usage.Input + before.Usage.CacheRead + before.Usage.CacheWrite; prompt < 4_000 {
		t.Fatalf("first prompt = %d tokens, want at least 4000", prompt)
	}
	mustConfigure(t, root, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: []*durable.ToolRegistration{first, second}})})
	after := ask("READY AGAIN").Usage
	return float64(after.CacheRead) / float64(after.Input+after.CacheRead+after.CacheWrite)
}

// Live regression coverage for #10542. Upstream resolves the Anthropic token from ~/.pi/agent/auth.json and skips
// without it; PiG's live tests take the resolved token from PIG_LIVE_ANTHROPIC_TOKEN.
func TestSystemMessageOrderCacheE2ELive(t *testing.T) {
	token := testenv.RequireLiveEnv(t, "PIG_LIVE_ANTHROPIC_TOKEN")
	// Before the fix: the input precedes the system message, so Anthropic sends the tool list at the top of the
	// request, and adding a tool changes the cached prefix from its first token.
	if rate := cacheReadAfterToolChange(t, token, true); rate >= 0.2 {
		t.Fatalf("cache read share with the legacy order = %f, want below 0.2", rate)
	}
	// The fix: the system message leads, the initial tools stay fixed, and the new tool arrives in the conversation.
	if rate := cacheReadAfterToolChange(t, token, false); rate <= 0.8 {
		t.Fatalf("cache read share with the system message first = %f, want above 0.8", rate)
	}
}
