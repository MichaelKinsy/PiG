package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi 1.2.0 runner.ts:1426-1450: a before_agent_start handler's event.systemPrompt and ctx.getSystemPrompt() render the
// shared options when read, so the handler's own edit shows at once. The Node runtime holds the options, so it asks the
// host to render the copy it edited.
func TestUIBridgeRenderSystemPromptRendersTheOptionsItIsSent(t *testing.T) {
	bridge := NewUIBridge(func() {})
	render := func(options map[string]any) *CallResultPayload {
		t.Helper()
		args, err := json.Marshal(map[string]any{"options": options})
		if err != nil {
			t.Fatal(err)
		}
		result, err := bridge.HandleCall("ext", &CallPayload{Method: "renderSystemPrompt", Args: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	prompt := func(result *CallResultPayload) string {
		t.Helper()
		if result.Error != nil {
			t.Fatalf("renderSystemPrompt error = %+v", result.Error)
		}
		var reply struct {
			Prompt string `json:"prompt"`
		}
		if err := json.Unmarshal(result.Result, &reply); err != nil {
			t.Fatal(err)
		}
		return reply.Prompt
	}

	edited := map[string]any{
		"customPrompt": "BASE PROMPT", "appendSystemPrompt": "APPENDED", "cwd": "/work",
		"selectedTools": []string{"read"}, "hiddenTools": []string{}, "toolSnippets": map[string]string{"read": "Read files"},
		"toolGuidelines": map[string][]string{}, "promptGuidelines": []string{"be brief"}, "sections": map[string]string{"zeta": "z", "alpha": "a"},
		"contextFiles": []any{}, "skills": []any{},
	}
	raw, err := json.Marshal(edited)
	if err != nil {
		t.Fatal(err)
	}
	var options extension.BuildSystemPromptOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		t.Fatal(err)
	}
	want, err := inproc.RenderSystemPrompt(extension.NormalizeBuildSystemPromptOptions(options))
	if err != nil {
		t.Fatal(err)
	}
	if got := prompt(render(edited)); got != want {
		t.Fatalf("rendered prompt = %q, want %q", got, want)
	}

	edited["forceSystemPrompt"] = "FORCED"
	if got := prompt(render(edited)); got != "FORCED" {
		t.Fatalf("forced prompt = %q, want FORCED", got)
	}

	// system-prompt.ts:71-75: an invalid section name makes the render, and so the getter, throw.
	delete(edited, "forceSystemPrompt")
	edited["sections"] = map[string]string{"preamble": "x"}
	if result := render(edited); result.Error == nil {
		t.Fatalf("invalid section rendered %s", result.Result)
	}
}
