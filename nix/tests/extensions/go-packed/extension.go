// Package gopacked is the Go factory (packed cell) of the Nix acceptance check.
package gopacked

import (
	"encoding/json"
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("go-packed")
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("go-packed", "go-packed: ready")
		return nil, nil
	})
	ext.Command("go-packed-probe", "Append an entry and count it", func(ctx sdk.Context, _ string) error {
		if err := ctx.AppendEntry("nix-probe", map[string]any{"by": "go-packed"}); err != nil {
			return err
		}
		entries, err := ctx.GetEntries()
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("go-packed: entries=%d", count(entries)), "info")
		return nil
	})
	ext.Command("nix-reload", "Reload every extension", func(ctx sdk.Context, _ string) error {
		return ctx.Reload()
	})
	return ext
}

func count(entries []json.RawMessage) int {
	n := 0
	for _, raw := range entries {
		var entry struct {
			Type       string `json:"type"`
			CustomType string `json:"customType"`
			Data       struct {
				By string `json:"by"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &entry) == nil && entry.Type == "custom" && entry.CustomType == "nix-probe" && entry.Data.By == "go-packed" {
			n++
		}
	}
	return n
}
