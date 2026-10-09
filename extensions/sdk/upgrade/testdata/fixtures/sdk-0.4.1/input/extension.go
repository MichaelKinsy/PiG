package editor

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is written for SDK 0.4.1: getters return errors, usage is nullable,
// and SetEditorComponent takes any (nil clears the editor).
func Extension() *sdk.Extension {
	ext := sdk.New("editor")

	ext.Command("reset", "Restore the host editor", func(ctx sdk.Context, args string) error {
		usage, err := ctx.GetContextUsage()
		if err != nil {
			return err
		}
		if usage != nil && usage.Tokens != nil {
			ctx.Notify("tokens known", "info")
		}
		return ctx.SetEditorComponent(nil)
	})

	return ext
}
