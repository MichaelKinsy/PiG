// Command go-isolated is the Go standalone (isolated cell) of the Nix acceptance check.
package main

import (
	"encoding/json"
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func main() {
	ext := sdk.New("go-isolated")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("go-isolated", "go-isolated: ready")
		return nil, nil
	})
	ext.Command("go-isolated-probe", "Append an entry and count it", func(ctx sdk.Context, _ string) error {
		if err := ctx.AppendEntry("nix-probe", map[string]any{"by": "go-isolated"}); err != nil {
			return err
		}
		entries, err := ctx.GetEntries()
		if err != nil {
			return err
		}
		n := 0
		for _, raw := range entries {
			var entry struct {
				CustomType string `json:"customType"`
				Data       struct {
					By string `json:"by"`
				} `json:"data"`
			}
			if json.Unmarshal(raw, &entry) == nil && entry.CustomType == "nix-probe" && entry.Data.By == "go-isolated" {
				n++
			}
		}
		ctx.Notify(fmt.Sprintf("go-isolated: entries=%d", n), "info")
		return nil
	})
	if err := ext.Run(); err != nil {
		panic(err)
	}
}
