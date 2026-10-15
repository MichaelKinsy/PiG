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
		id, err := ctx.GetSessionID()
		if err != nil {
			return err
		}
		namePtr, err := ctx.GetSessionName()
		if err != nil {
			return err
		}
		var name string
		if namePtr != nil {
			name = *namePtr
		}
		idle, err := ctx.IsIdle()
		if err != nil {
			return err
		}
		if idle {
			ctx.Notify(fmt.Sprintf("session %s (%s) is idle", id, name), "info")
		}
		return nil
	})

	ext.Tool("usage", "Report context usage", sdk.Schema{"type": "object"}, func(ctx sdk.Context, params map[string]any) (any, error) {
		usage, err := ctx.GetContextUsage()
		if err != nil {
			return nil, err
		}
		if usage != nil && usage.TokensOr(0) > 0 {
			return fmt.Sprintf("%d tokens (%.1f%%)", usage.TokensOr(0), usage.PercentOr(0)), nil
		}
		return "unknown", nil
	})

	ext.OnSessionStart(func(ctx sdk.Context, event map[string]any) (any, error) {
		var level string
		thinkingLevel, err := ctx.GetThinkingLevel()
		if err != nil {
			return nil, err
		}
		level = thinkingLevel
		systemPrompt, err := ctx.GetSystemPrompt()
		if err != nil {
			return nil, err
		}
		prompt := strings.TrimSpace(systemPrompt)
		ctx.Notify(level+": "+prompt, "info")
		return nil, nil
	})

	return ext
}

// current returns the session file the extension reads.
func current(ctx sdk.Context) string {
	sessionFilePtr, _ := ctx.GetSessionFile() // the SDK now returns an error here: handle it
	var sessionFile string
	if sessionFilePtr != nil {
		sessionFile = *sessionFilePtr
	}
	return sessionFile
}

// editorText has no error result, so the SDK's error has nowhere to go.
func editorText(ctx sdk.Context) string {
	editorText2, _ := ctx.GetEditorText() // the SDK now returns an error here: handle it
	return editorText2
}
