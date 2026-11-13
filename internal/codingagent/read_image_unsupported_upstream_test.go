package codingagent

// pi: packages/coding-agent/test/tools.test.ts

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi 4be4cb4f5e9c packages/coding-agent/test/tools.test.ts:240 "should keep a text-only model's image warning out of what the model gets".
func TestReadKeepsTextOnlyImageWarningOutOfWhatTheModelGetsUpstream(t *testing.T) {
	const png1x1Base64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg=="
	testDir := t.TempDir()
	testFile := filepath.Join(testDir, "text-model.png")
	data, err := base64.StdEncoding.DecodeString(png1x1Base64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// Pi's ctx is { cwd: testDir, model: { id: "text-only", input: ["text"] } }: the tool environment carries the model's id and that it takes no images.
	supportsImages := false
	ctx := agent.WithToolEnvironment(t.Context(), agent.ToolEnvironment{Model: "text-only", SupportsImages: &supportsImages})
	args, err := json.Marshal(map[string]any{"path": testFile})
	if err != nil {
		t.Fatal(err)
	}

	result, err := (&tools.ReadTool{CWD: testDir}).Execute(ctx, "test-call-img-text-model", args, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The result is replayed after a switch to a vision model, so it keeps the image and no
	// note saying the model cannot see it; pi-ai replaces the image per request instead.
	if got, want := result.Text(), "Read image file [image/png]"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if len(result.Images()) == 0 {
		t.Errorf("content = %+v, want an image block", result.Content)
	}

	// The user still sees the warning, collapsed and expanded.
	f := toolComponentRaw(t, "read", "test-call-img-text-model", args, nil)
	f.update(result, false)
	const warning = "[text-only does not support images; it saw a placeholder instead]"
	if collapsed := f.plain(120); !strings.Contains(collapsed, warning) {
		t.Errorf("collapsed card lacks %q: %q", warning, collapsed)
	}
	f.card.SetExpanded(true)
	if expanded := f.plain(120); !strings.Contains(expanded, warning) {
		t.Errorf("expanded card lacks %q: %q", warning, expanded)
	}
}

// renderers/read.ts formatReadResult reads `result.details?.imageUnsupportedByModel` and draws the warning when it is truthy: from the
// tool's own details, from the JSON a resumed session carries, and not for the id "" (which read.ts still writes).
func TestReadCardDrawsTheImageWarningFromDetails(t *testing.T) {
	emptyID, modelID := "", "text-only"
	const warning = "[text-only does not support images; it saw a placeholder instead]"
	for _, tc := range []struct {
		name    string
		details any
		want    bool
	}{
		{"the tool's details", &tools.ReadDetails{ImageUnsupportedByModel: &modelID}, true},
		{"a stored result's details", map[string]any{"imageUnsupportedByModel": "text-only"}, true},
		{"an empty id", &tools.ReadDetails{ImageUnsupportedByModel: &emptyID}, false},
		{"a stored empty id", map[string]any{"imageUnsupportedByModel": ""}, false},
		{"a truncation only", map[string]any{"truncation": map[string]any{"truncated": false}}, false},
		{"no details", nil, false},
	} {
		f := toolComponentRaw(t, "read", "read-image-details", json.RawMessage(`{"path":"shot.png"}`), nil)
		f.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Read image file [image/png]"}}, Details: tc.details}, false)
		for _, expanded := range []bool{false, true} {
			f.card.SetExpanded(expanded)
			text := f.plain(120)
			if got := strings.Contains(text, warning); got != tc.want {
				t.Errorf("%s, expanded %v: warning drawn %v, want %v: %q", tc.name, expanded, got, tc.want, text)
			}
			if !tc.want && strings.Contains(text, "does not support images") {
				t.Errorf("%s, expanded %v: a warning without a model: %q", tc.name, expanded, text)
			}
		}
	}
}

// A session written before Pi 4be4cb4f5e9c, by an older Pi or PiG, holds the old note in the read result's text and no details. Pi
// has no migration (the commit touches no session code), so neither has PiG: the card shows the stored text as it is when expanded and
// draws no warning (nothing names the model), and the next request is built from the stored text as it is, with the image for a vision
// model and pi-ai's placeholder for one without image input.
func TestReadResultStoredWithTheOldImageNoteStaysAsWritten(t *testing.T) {
	const oldNote = "[Current model does not support images. The image will be omitted from this request.]"
	stored := []ai.ToolResultMessageContent{
		ai.TextContent{Text: "Read image file [image/png]\n" + oldNote},
		ai.ImageContent{MimeType: "image/png", Data: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNgYGD4DwABBAEAX+XDSwAAAABJRU5ErkJggg=="},
	}
	f := toolComponentRaw(t, "read", "stored-old-note", json.RawMessage(`{"path":"shot.png"}`), nil)
	f.update(agent.AgentToolResult{Content: stored}, false)
	if collapsed := f.plain(120); strings.Contains(collapsed, "does not support images") {
		t.Errorf("collapsed card of a stored result shows a warning: %q", collapsed)
	}
	f.card.SetExpanded(true)
	if expanded := f.plain(120); !strings.Contains(expanded, oldNote) || strings.Contains(expanded, "it saw a placeholder instead") {
		t.Errorf("expanded card of a stored result = %q, want the stored note and no warning", expanded)
	}

	message := ai.ToolResultMessage{ToolCallID: "call-1", ToolName: "read", Content: stored}
	vision := ai.TransformMessages([]ai.Message{message}, &ai.Model{ID: "vision", Input: []string{"text", "image"}}, nil)
	if got := vision[0].(ai.ToolResultMessage).Content; !reflect.DeepEqual(got, stored) {
		t.Errorf("replayed to a vision model = %+v, want the stored content %+v", got, stored)
	}
	textOnly := ai.TransformMessages([]ai.Message{message}, &ai.Model{ID: "text-only", Input: []string{"text"}}, nil)
	want := []ai.ToolResultMessageContent{stored[0], ai.TextContent{Text: "(tool image omitted: model does not support images)"}}
	if got := textOnly[0].(ai.ToolResultMessage).Content; !reflect.DeepEqual(got, want) {
		t.Errorf("replayed to a text-only model = %+v, want %+v", got, want)
	}
}
