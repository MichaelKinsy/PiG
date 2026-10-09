package search

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension is written for the SDK before 0.2.0: optional booleans are bool.
func Extension() *sdk.Extension {
	ext := sdk.New("search")

	ext.Command("search", "Search the web", func(ctx sdk.Context, args string) error {
		leafPtr, err := ctx.GetLeafID()
		if err != nil {
			return err
		}
		var leaf string
		if leafPtr != nil {
			leaf = *leafPtr
		}
		followUp := len(args) > 0
		if err := ctx.SendMessage("search-result", "found "+leaf, true, sdk.SendMessageOptions{TriggerTurn: sdk.Bool(followUp), DeliverAs: "followUp"}); err != nil {
			return fmt.Errorf("send: %w", err)
		}
		return ctx.SendMessage("search-note", "done", true,
			sdk.SendMessageOptions{TriggerTurn: sdk.Bool(true), DeliverAs: "followUp"})
	})

	return ext
}
