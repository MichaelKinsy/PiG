// Package shutdownwriter is the Go SDK version of the shutdown-writer fixture (../node/index.mjs), without the label (the Go SDK has no leaf read).
package shutdownwriter

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// never keeps the message from starting a turn, as the Node fixture's triggerTurn: false does.
var never = false

func Extension() *sdk.Extension {
	ext := sdk.New("go")
	ext.OnSessionShutdown(func(ctx sdk.Context, event map[string]any) (any, error) {
		if err := ctx.AppendEntry("shutdown-entry", map[string]any{"text": "from-shutdown", "reason": event["reason"]}); err != nil {
			return nil, err
		}
		if err := ctx.SetSessionName("shutdown-name"); err != nil {
			return nil, err
		}
		if err := ctx.SendMessage("shutdown-message", "shutdown-text", false, sdk.SendMessageOptions{TriggerTurn: &never}); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return ext
}
