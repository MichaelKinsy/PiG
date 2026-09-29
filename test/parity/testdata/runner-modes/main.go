package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func main() {
	r := inproc.NewRunner(nil, ".")
	contexts := []*extension.Context{extension.FromContext(r.DispatchContext(context.Background())), r.CreateCommandContext().Context}
	printJSON := func(value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(raw))
	}
	record := func(label string) {
		row := []any{label}
		for _, ctx := range contexts {
			mode, err := ctx.Mode()
			if err != nil {
				panic(err)
			}
			hasUI, err := ctx.HasUI()
			if err != nil {
				panic(err)
			}
			ui, err := ctx.UI()
			if err != nil {
				panic(err)
			}
			row = append(row, []any{mode, hasUI, ui == r.GetUIContext()})
		}
		printJSON(row)
	}
	ui := &struct{ extension.UIContext }{extension.NoopUIContext}
	record("initial")
	r.SetUIContext(ui)
	record("default")
	for _, mode := range []extension.ExtensionMode{extension.ModeRPC, extension.ModeTUI, extension.ModeJSON, extension.ModePrint} {
		r.SetUIContext(ui, mode)
		record(string(mode))
		r.SetUIContext(nil, mode)
		record(string(mode) + ":clear")
	}
	r.SetUIContext(r.GetUIContext())
	record("supplied-noop")
	r.Invalidate("replaced")
	failures := [][]string{}
	for _, ctx := range contexts {
		_, modeErr := ctx.Mode()
		_, hasErr := ctx.HasUI()
		_, uiErr := ctx.UI()
		failures = append(failures, []string{modeErr.Error(), hasErr.Error(), uiErr.Error()})
	}
	printJSON(failures)
}
