package codingagent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// interactiveCodemodeBuild is one Session build of the interactive mode: the Services, a Session whose runner carries the built-in codemode extension (as the CLI builds it for every mode), and a classifier provider named "scorer".
type interactiveCodemodeBuild struct {
	services *coding.Services
	runner   *inproc.Runner
	session  *coding.Session
}

func newInteractiveCodemodeBuild(t *testing.T) interactiveCodemodeBuild {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.NewServices(coding.ServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	entry, err := builtin.Resolve("builtin:codemode", builtin.Options{Codemode: codemode.Options{GetSettings: func() extension.Settings {
		return extension.Settings{"codemode": map[string]any{"mode": "on"}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	ext, err := entry.Factory()
	if err != nil {
		t.Fatal(err)
	}
	ext.Name, ext.Path, ext.ResolvedPath, ext.Replaceable, ext.Hidden = entry.Name, entry.Path(), entry.Path(), true, true
	runner := inproc.NewRunner([]extension.Extension{ext}, services.CWD())
	session, err := coding.NewSession(services, coding.SessionOptions{Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	scorer := &ai.ClassifierModel{ID: "judge", Name: "Judge", API: "test-classifier", Provider: "scorer", BaseURL: "https://classifier.test/v1", Input: []string{"text"}, ContextWindow: 1000}
	classify := func(_ context.Context, model *ai.ClassifierModel, _ ai.ClassifierContext, _ ai.ClassifierOptions) (ai.ClassifierResult, error) {
		return ai.ClassifierResult{
			API: model.API, Provider: model.Provider, Model: model.ID,
			Answers:    ai.ClassifierAnswers{{ID: "approved", Answer: ai.ClassifierBoolAnswer{Probability: 0.9}}},
			StopReason: ai.ClassifierStopReasonStop,
		}, nil
	}
	if err := session.ModelRuntime().RegisterProvider("scorer", coding.ProviderConfigInput{
		APIKey: "secret-key", Models: []ai.AnyModel{scorer}, Classifiers: ai.ProviderClassifierMap{"test-classifier": {Classify: classify}},
	}); err != nil {
		t.Fatal(err)
	}
	session.SetActiveToolsByName([]string{"codemode"})
	return interactiveCodemodeBuild{services: services, runner: runner, session: session}
}

func (b interactiveCodemodeBuild) options() icodingagent.InteractiveOptions {
	return icodingagent.InteractiveOptions{
		CWD: b.services.CWD(), AgentDir: b.services.AgentDir(), SessionHandle: b.session, SettingsManager: b.services.SettingsManager(),
		Settings: b.services.SettingsManager().Get(), ExtensionRunner: b.runner, ModelRegistry: b.services.Registry().ModelRegistry,
		NoSkills: true, NoThemes: true, NoPromptTemplates: true,
	}
}

// run executes a codemode script as the Session's codemode tool and returns the text blocks after the header.
func (b interactiveCodemodeBuild) run(t *testing.T, code string) string {
	t.Helper()
	params, err := json.Marshal(map[string]any{"code": code})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range b.session.Tools() {
		if tool.Name() != "codemode" {
			continue
		}
		result, err := tool.Execute(t.Context(), "call-1", params, nil)
		if err != nil {
			t.Fatalf("codemode: %v", err)
		}
		var text []string
		for _, block := range result.Content {
			if block, ok := block.(ai.TextContent); ok {
				text = append(text, block.Text)
			}
		}
		if result.IsError {
			t.Fatalf("codemode isError: %v", text)
		}
		return strings.Join(text[1:], "\n")
	}
	t.Fatal("the Session has no active codemode tool")
	return ""
}

// requireSessionRegistry checks the registry the other in-process built-ins read through ctx.modelRegistry: the builtin:mcp extension reads provider tokens from it (coding/mcpext/register.go ProviderToken), and pi.registerProvider reaches it (runner.ts:468-541).
func (b interactiveCodemodeBuild) requireSessionRegistry(t *testing.T, when string) {
	t.Helper()
	registry, err := b.runner.CreateCommandContext().ModelRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if registry != any(b.services.Registry()) {
		t.Fatalf("%s: ctx.modelRegistry = %T, want the Session's Services registry", when, registry)
	}
	if _, ok := registry.(interface {
		GetAPIKeyForProvider(context.Context, string) *string
	}); !ok {
		t.Fatalf("%s: ctx.modelRegistry = %T has no GetAPIKeyForProvider, so builtin:mcp reads no provider token", when, registry)
	}
}

const codemodeModelsScript = `
	const [judge] = await models.getAvailableOfType("classifier", "scorer");
	const verdict = await models.classify(judge, { state: { text: "good" }, questions: { approved: { type: "bool", instructions: "Approval?", criteria: { true: "yes", false: "no" } } } });
	return [typeof models, judge.id, verdict.answers.approved.probability].join(" ");
`

// Pi binds modelRegistry in every mode: the runner owns it from construction (runner.ts:363, 399-406, 835, 893-895) and a mode's bindExtensions reaches bindCore only through _bindExtensionCore (agent-session.ts:3185, 3287-3313; interactive-mode.ts:1916). A codemode script therefore sees `models` in the interactive mode, and again after /new or a resume builds a replacement Session.
func TestInteractiveCodemodeScriptsReachModelsThroughReplacement(t *testing.T) {
	first := newInteractiveCodemodeBuild(t)
	h := icodingagent.NewTestHarness(t, first.options(), nil)
	if err := first.session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.requireSessionRegistry(t, "initial interactive Session")
	const want = "object judge 0.9"
	if got := first.run(t, codemodeModelsScript); got != want {
		t.Fatalf("initial interactive Session: codemode script = %q, want %q", got, want)
	}

	second := newInteractiveCodemodeBuild(t)
	replacement := icodingagent.InteractiveReplacement{
		CWD: second.services.CWD(), SessionDir: second.session.Inner().GetSessionDir(), Settings: second.services.SettingsManager().Get(),
		SettingsManager: second.services.SettingsManager(), ModelRegistry: second.services.Registry().ModelRegistry,
		ExtensionRunner: second.runner, SessionStartEvent: extension.SessionStartEvent{Type: "session_start", Reason: "new"},
	}
	if err := h.RebindToReplacement(t.Context(), second.session, replacement); err != nil {
		t.Fatal(err)
	}
	second.requireSessionRegistry(t, "replacement Session after /new")
	if got := second.run(t, codemodeModelsScript); got != want {
		t.Fatalf("replacement Session after /new: codemode script = %q, want %q", got, want)
	}
}
