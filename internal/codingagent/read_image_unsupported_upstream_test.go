package codingagent

// pi: packages/coding-agent/test/tools.test.ts

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
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
