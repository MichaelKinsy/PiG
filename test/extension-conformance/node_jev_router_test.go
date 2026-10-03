package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Ports .upstream/v0.99.1/packages/coding-agent/test/jev-router-example.test.ts (2 cases) with the same inputs and expectations. Upstream loads the example as an in-process factory next to a faux OpenAI Codex provider and a scripted TypeSafe classifier; here the unchanged example file runs as a Node extension in the real Host, the providers are registered in the Go model runtime, and the Session is the real one.
//
// Both cases route their first request through `ctx.modelRegistry.findOfType("classifier", ...)` and `ctx.modelRegistry.classify(...)` (jev-router.ts:78-102), which lane port-99-f6f-model-types (port-99-f6h-model-types) implements in the Host and the SDKs. The Node runtime side of those two calls waits for its wire, so the cases skip until then. Every other branch of the example runs in coding/extension/host/subprocess/upstream_jev_router_test.go.

// jevTool is upstream's `createTool(name, fails)` (:17-27): a tool that answers "<name> ok", or throws "<name> failed".
type jevTool struct {
	name  string
	fails bool
}

func (t jevTool) Name() string  { return t.name }
func (t jevTool) Label() string { return t.name }
func (t jevTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Description: "Fake " + t.name + " tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}, "required": []string{"path"}}}
}
func (jevTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (t jevTool) Execute(context.Context, string, json.RawMessage, agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	if t.fails {
		return agent.AgentToolResult{}, errors.New(t.name + " failed")
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: t.name + " ok"}}, Details: map[string]any{}}, nil
}

func TestNodeJevRouterExample(t *testing.T) {
	call := func(tool string) ai.FauxResponseStep {
		return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(tool, map[string]any{"path": "a.ts"}, "")}, StopReason: "toolUse"})
	}
	text := func(value string) ai.FauxResponseStep {
		return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(value)}, StopReason: "stop"})
	}
	t.Run("lets Sol make the first edit and switches to Luna for the rest", func(t *testing.T) { // jev-router-example.test.ts:98-113
		t.Skip("pending port-99-f6h-model-types: the Node runtime's ctx.modelRegistry.findOfType and classify need the Host wire that lane defines")
		s := newJevRouterRig(t, 0.8, []jevTool{{name: "read"}, {name: "edit"}})
		s.respond(call("read"), call("edit"), text("done"))
		s.prompt("Refactor the cache layer.")
		s.respond(text("added"))
		s.prompt("Also add tests.")

		// Sol reads and makes the first edit; the tool-result follow-up of the same turn goes to Luna.
		assertEqualNode(t, "dispatched", s.dispatched(), []string{"sol", "sol", "luna", "luna"})
		assertEqualNode(t, "phases", s.phases(), []string{"planning", "implementation"})
		if model := s.session.Model(); model.ProviderMeta.ProviderID != "jev" || model.ID != "auto" {
			t.Fatalf("selection = %s/%s", model.ProviderMeta.ProviderID, model.ID)
		}
	})
	t.Run("plans on Terra and ignores failed edits", func(t *testing.T) { // jev-router-example.test.ts:115-127
		t.Skip("pending port-99-f6h-model-types: the Node runtime's ctx.modelRegistry.findOfType and classify need the Host wire that lane defines")
		s := newJevRouterRig(t, 0.2, []jevTool{{name: "edit", fails: true}, {name: "write"}})
		s.respond(call("edit"), call("write"), text("done"))
		s.prompt("Add a verbose flag.")

		assertEqualNode(t, "dispatched", s.dispatched(), []string{"terra", "terra", "luna"})
		assertEqualNode(t, "phases", s.phases(), []string{"planning", "implementation"})
	})
}

type jevRouterRig struct {
	t       *testing.T
	session *coding.Session
	codex   interface{ AppendResponses([]ai.FauxResponseStep) }
}

// newJevRouterRig is upstream's `setup(complex, tools)` (:52-96): a faux OpenAI Codex provider, a TypeSafe provider whose Jev classifier rates the complexity as scripted, the example, and a session on jev/auto.
func newJevRouterRig(t *testing.T, complexity float64, tools []jevTool) *jevRouterRig {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts Node subprocesses)")
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime := services.ModelRuntime()

	codex := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "openai-codex", Models: []ai.FauxModelDefinition{{ID: "gpt-5.6-sol", Reasoning: true}, {ID: "gpt-5.6-terra", Reasoning: true}, {ID: "gpt-5.6-luna", Reasoning: true}}})
	if err := runtime.RegisterNativeProvider(codex.Provider()); err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("openai-codex", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	jev := &ai.ClassifierModel{ID: "jev-latest", Name: "Jev", API: "typesafe-system-one", Provider: "typesafe", Input: []string{"text"}, ContextWindow: 64_000}
	typesafe := ai.CreateProvider(ai.CreateProviderOptions{
		ID:     "typesafe",
		Auth:   ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "TypeSafe", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return &ai.AuthResult{}, nil }}},
		Models: []ai.AnyModel{jev},
		Classifiers: ai.ProviderClassifierMap{"typesafe-system-one": {Classify: func(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, _ ai.ClassifierOptions) (ai.ClassifierResult, error) {
			choice := "standard"
			if complexity >= 0.5 {
				choice = "complex"
			}
			return ai.ClassifierResult{API: model.API, Provider: model.Provider, Model: model.ID, StopReason: ai.ClassifierStopReasonStop, Timestamp: time.Now().UnixMilli(),
				Answers: ai.ClassifierAnswers{{ID: "complexity", Answer: ai.ClassifierChoiceAnswer{Choice: choice, Probabilities: []ai.ClassifierProbability{{Key: "standard", Probability: 1 - complexity}, {Key: "complex", Probability: complexity}}, Confidence: max(complexity, 1-complexity)}}}}, nil
		}}},
	})
	if err := runtime.RegisterNativeProvider(typesafe); err != nil {
		t.Fatal(err)
	}

	notify, status := &[]string{}, &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	detach := codingagent.WireModelOperations(bridge, codingagent.ModelOperationBindings{ModelLookup: runtime.GetModel, ModelCatalog: runtime.GetModels, Registry: services.Registry().ModelRegistry})
	t.Cleanup(detach)
	h := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetUIBridge(bridge)
	t.Cleanup(func() { h.Shutdown("test complete") })
	entry := filepath.Join(findModuleRoot(t), ".upstream", "current", "packages", "coding-agent", "examples", "extensions", "jev-router.ts")
	loaded, failures := h.LoadAll(t.Context(), []subprocess.ExtConfig{{Name: "jev-router", Source: entry, Enabled: true}})
	if len(failures) != 0 {
		t.Fatalf("load: %v", failures)
	}
	runner := inproc.NewRunner(loaded, t.TempDir(), h.Runtime())
	agentTools := make([]agent.AgentTool, len(tools))
	for i, tool := range tools {
		agentTools[i] = tool
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: runtime.GetModel("openai-codex", "gpt-5.6-luna"), Runner: runner, Tools: agentTools, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			coding.AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-done })
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := session.SetModel(runtime.GetModel("jev", "auto")); err != nil {
		t.Fatal(err)
	}
	return &jevRouterRig{t: t, session: session, codex: codex}
}

func (r *jevRouterRig) respond(steps ...ai.FauxResponseStep) { r.codex.AppendResponses(steps) }

func (r *jevRouterRig) prompt(text string) {
	r.t.Helper()
	if _, err := r.session.Prompt(r.t.Context(), text); err != nil {
		r.t.Fatal(err)
	}
}

// dispatched is the physical model of each response, without the gpt-5.6- prefix (:87-88).
func (r *jevRouterRig) dispatched() []string {
	out := []string{}
	for _, message := range r.session.Messages() {
		if a := message.Assistant; a != nil {
			out = append(out, strings.TrimPrefix(a.ModelID, "gpt-5.6-"))
		}
	}
	return out
}

// phases is the router phase of each state entry on the branch (:89-95).
func (r *jevRouterRig) phases() []string {
	out := []string{}
	for _, entry := range r.session.Inner().GetBranch() {
		if entry.Base.Type != "custom" {
			continue
		}
		var custom struct {
			CustomType string `json:"customType"`
			Data       struct {
				State struct {
					Phase string `json:"phase"`
				} `json:"state"`
			} `json:"data"`
		}
		if err := json.Unmarshal(entry.Raw(), &custom); err == nil && custom.CustomType == coding.VirtualModelStateEntry {
			out = append(out, custom.Data.State.Phase)
		}
	}
	return out
}
