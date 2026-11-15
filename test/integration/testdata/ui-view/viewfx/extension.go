// Package viewfx is the factory form of the view fixture (../fixture), for the realizations a standalone cannot take: the Piglet fused.yaml requires it to fuse into a Piglet Binary. Its command /vmount mounts the fixture's panel and widget, and its intent handler redraws the panel with the picked value.
package viewfx

import (
	"encoding/json"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

var pick = sdk.ViewIntent{
	Name: "viewfx.pick", NodeKey: "pick", TimeoutMs: 5000,
	Arg: sdk.ViewShape{Type: "object", Properties: map[string]sdk.ViewShape{"value": {Type: "string"}}, Required: []string{"value"}},
}

func panel(title string) sdk.ViewContent {
	list := kit.NewSelectList("pick", []kit.SelectItem{{Value: "alpha", Label: "Alpha item"}, {Value: "beta", Label: "Beta item"}}, 4)
	return sdk.ViewContent{
		View:    kit.View{Root: kit.NewContainer(kit.NewText(title, 1, 0), list), Focus: "pick"},
		Intents: []sdk.ViewIntent{pick},
		Focus:   []sdk.ViewFocus{{Node: "pick", Request: "mount"}},
	}
}

// Extension returns a new instance of the fixture. A fused member's instance is made again by every reload.
func Extension() *sdk.Extension {
	ext := sdk.New("viewfx")
	ext.Command("vmount", "Mount the fixture's panel and widget", func(ctx sdk.Context, _ string) error {
		note := sdk.ViewContent{View: kit.View{Root: kit.NewText("fixture widget row", 1, 0)}}
		_, err := ctx.MountView(sdk.ViewMount{Key: "note", Slot: sdk.ViewSlotWidgetAbove, ViewContent: note})
		if err == nil {
			_, err = ctx.MountView(sdk.ViewMount{Key: "panel", Slot: sdk.ViewSlotPanel, ViewContent: panel("fixture panel")})
		}
		if err != nil {
			ctx.Notify("viewfx vmount: "+err.Error(), "error")
		}
		return nil
	})
	// vping answers with a notice that repeats its argument: the extension is loaded and serving once it is seen.
	ext.Command("vping", "Answer with a notice", func(ctx sdk.Context, args string) error {
		ctx.Notify("viewfx-pong"+args, "info")
		return nil
	})
	// vpanic fails the way a handler fails: the SDK recovers the panic and reports it, with its stack, to the extension's log (the fused log of a Piglet Binary).
	ext.Command("vpanic", "Panic in a handler, as a failing extension does", func(sdk.Context, string) error { panic("viewfx-probe-boom") })
	ext.OnViewIntent(func(in sdk.ViewIntentInvocation) sdk.ViewIntentResult {
		var arg struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(in.Arg, &arg); err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		at := sdk.ViewRef{Key: in.Mount.Key, Epoch: in.Mount.Epoch, Rev: in.Rev}
		if _, err := in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: panel("picked " + arg.Value)}); err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		return sdk.ViewIntentResult{Outcome: "done"}
	})
	return ext
}
