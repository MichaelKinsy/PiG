package ask

import (
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is written for SDK 0.2.0: every Context getter returns one value.
func Extension() *sdk.Extension {
	ext := sdk.New("ask")

	ext.Command("where", "Show the session", func(ctx sdk.Context, args string) error {
		id := ctx.GetSessionID()
		name := ctx.GetSessionName()
		if ctx.IsIdle() {
			ctx.Notify(fmt.Sprintf("session %s (%s) is idle", id, name), "info")
		}
		return nil
	})

	ext.Tool("usage", "Report context usage", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		usage := ctx.GetContextUsage()
		if usage != nil && usage.Tokens > 0 {
			return fmt.Sprintf("%d tokens (%.1f%%)", usage.Tokens, usage.Percent), nil
		}
		return "unknown", nil
	})

	ext.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		var level string
		level = ctx.GetThinkingLevel()
		prompt := strings.TrimSpace(ctx.GetSystemPrompt())
		ctx.Notify(level+": "+prompt, "info")
		return nil, nil
	})

	return ext
}

// current returns the session file the extension reads.
func current(ctx sdk.Context) string {
	return ctx.GetSessionFile()
}

// editorText has no error result, so the SDK's error has nowhere to go.
func editorText(ctx sdk.Context) string {
	return ctx.GetEditorText()
}
