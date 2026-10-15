package codemode

// pi: packages/coding-agent/src/extensions/codemode/execute.ts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func executeJoinResult(t *testing.T, code string) []ai.ToolResultMessageContent {
	t.Helper()
	useSaveTempDir(t)
	base := extension.NewContext(t.TempDir(), nil, func() error { return nil }, extension.ContextActions{SessionManager: emptyBranch{}})
	ctx := extension.WithToolContext(context.Background(), extension.NewToolContext(base, "call", context.Background(), extension.ToolActions{}))
	params, _ := json.Marshal(map[string]string{"code": code})
	result, err := Execute(ctx, "call", params, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result.Content
}

// execute.ts (Pi 1.1.0): the text items of a result are joined before truncation, so the script's text and its error
// text are one item, each part on its own line.
func TestExecuteJoinsTheScriptTextAndItsErrorIntoOneItem(t *testing.T) {
	content := executeJoinResult(t, `text("before"); throw new Error("boom");`)
	if len(content) != 2 {
		t.Fatalf("content = %#v, want the header and one joined text item", content)
	}
	joined := labelText(t, content[1])
	if !strings.HasPrefix(joined, "before\nScript error:\n") || !strings.Contains(joined, "boom") {
		t.Fatalf("joined = %q, want the text and the script error on separate lines", joined)
	}
}

// execute.ts (Pi 1.1.0): the saved-path labels are joined after saveImages, so a text item and the label of the image
// that follows it are one item.
func TestExecuteJoinsTheTextAndTheSavedImageLabel(t *testing.T) {
	content := executeJoinResult(t, `text("shown"); image("data:image/png;base64,`+savePNG+`");`)
	if len(content) != 3 {
		t.Fatalf("content = %#v, want the header, one joined text item and the image", content)
	}
	joined := labelText(t, content[1])
	if !strings.HasPrefix(joined, "shown\n[Image saved to ") {
		t.Fatalf("joined = %q, want the text and the saved-path label on separate lines", joined)
	}
	if _, ok := content[2].(ai.ImageContent); !ok {
		t.Fatalf("content[2] = %#v, want the image", content[2])
	}
}
