// Package writerstart is the Go SDK version of the writer-start fixture (../node/index.mjs).
package writerstart

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// never keeps the messages from starting a turn, as the Node fixture's triggerTurn: false does.
var never = false

func Extension() *sdk.Extension {
	ext := sdk.New("go")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := ctx.AppendEntry("writer-start", map[string]any{"text": "from-start"}); err != nil {
			return nil, err
		}
		if err := ctx.SetSessionName("start-name"); err != nil {
			return nil, err
		}
		if err := ctx.SendMessage("writer-start", "start-message", false, sdk.SendMessageOptions{TriggerTurn: &never}); err != nil {
			return nil, err
		}
		ctx.SetStatus("writer", "writer: ready")
		return nil, nil
	})
	ext.Command("writer-after", "Append, name and send from a command", func(ctx sdk.Context, _ string) error {
		if err := ctx.AppendEntry("writer-after", map[string]any{"text": "from-command"}); err != nil {
			return err
		}
		if err := ctx.SetSessionName("after-name"); err != nil {
			return err
		}
		return ctx.SendMessage("writer-after", "after-message", false, sdk.SendMessageOptions{TriggerTurn: &never})
	})
	return ext
}
