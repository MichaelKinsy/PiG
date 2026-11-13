// Command fixture is the extension of the terminal view client's integration test (test/integration). Its commands mount
// a panel with a list and a widget, update, hide, show, remove and finish them, and its intent handler redraws the panel
// with the picked value. With PIG_UI_VIEW_FIXTURE_MOUNTS=0 every command runs and makes no ui.view call, so a session
// that never mounted takes the same keys.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

const (
	panelKey  = "panel"
	widgetKey = "note"
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

func note(text string) sdk.ViewContent {
	return sdk.ViewContent{View: kit.View{Root: kit.NewText(text, 1, 0)}}
}

func main() {
	ext := sdk.New("viewfx")
	mounts := os.Getenv("PIG_UI_VIEW_FIXTURE_MOUNTS") != "0"
	var mu sync.Mutex
	refs := map[string]sdk.ViewRef{}
	keep := func(st sdk.ViewStatus, err error) error {
		if err != nil {
			return err
		}
		mu.Lock()
		refs[st.Mount.Key] = st.Mount
		mu.Unlock()
		return nil
	}
	ref := func(key string) sdk.ViewRef {
		mu.Lock()
		defer mu.Unlock()
		return refs[key]
	}
	command := func(name, description string, run func(ctx sdk.Context) error) {
		ext.Command(name, description, func(ctx sdk.Context, _ string) error {
			if !mounts {
				return nil
			}
			if err := run(ctx); err != nil {
				ctx.Notify("viewfx "+name+": "+err.Error(), "error")
			}
			return nil
		})
	}
	both := func(op func(sdk.ViewRef) (sdk.ViewStatus, error)) error {
		for _, key := range []string{panelKey, widgetKey} {
			if err := keep(op(ref(key))); err != nil {
				return err
			}
		}
		return nil
	}
	command("vmount", "Mount the fixture's panel and widget", func(ctx sdk.Context) error {
		if err := keep(ctx.MountView(sdk.ViewMount{Key: widgetKey, Slot: sdk.ViewSlotWidgetAbove, ViewContent: note("fixture widget row")})); err != nil {
			return err
		}
		return keep(ctx.MountView(sdk.ViewMount{Key: panelKey, Slot: sdk.ViewSlotPanel, ViewContent: panel("fixture panel")}))
	})
	command("vupdate", "Update the fixture's widget", func(ctx sdk.Context) error {
		return keep(ctx.UpdateView(sdk.ViewUpdate{Mount: ref(widgetKey), ViewContent: note("fixture widget updated")}))
	})
	command("vhide", "Hide the fixture's views", func(ctx sdk.Context) error { return both(ctx.HideView) })
	command("vshow", "Show the fixture's views", func(ctx sdk.Context) error { return both(ctx.ShowView) })
	command("vremove", "Remove the fixture's views", func(ctx sdk.Context) error { return both(ctx.RemoveView) })
	command("vfinish", "Finish the fixture's views", func(ctx sdk.Context) error { return both(ctx.FinishView) })
	ext.OnViewIntent(func(in sdk.ViewIntentInvocation) sdk.ViewIntentResult {
		var arg struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(in.Arg, &arg); err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		at := sdk.ViewRef{Key: in.Mount.Key, Epoch: in.Mount.Epoch, Rev: in.Rev}
		if err := keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: panel("picked " + arg.Value)})); err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		return sdk.ViewIntentResult{Outcome: "done"}
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
