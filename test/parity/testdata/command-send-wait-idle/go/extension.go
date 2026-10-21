// Package sendwaitidle is the Go SDK version of rpc/testdata/command-send-wait-idle.mjs: a command that sends a user message and then waits for the Session to be idle.
package sendwaitidle

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("go")
	ext.Command("ask", "Send a user message and wait for idle", func(ctx sdk.Context, _ string) error {
		if err := ctx.SendUserMessage("Say hello", ""); err != nil {
			return err
		}
		return ctx.WaitForIdle()
	})
	return ext
}
