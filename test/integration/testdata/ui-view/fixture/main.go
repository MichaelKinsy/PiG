// Command fixture is the extension of the terminal view client's integration test (test/integration). Its commands mount
// a panel with a list and a widget, update, hide, show, remove and finish them, and its intent handler redraws the panel
// with the picked value. vtabs mounts three titled panels and hides the third, which the user can open; vfocus updates
// the second with a focus request; vbind mounts a panel whose keys are bindings; vdialog mounts a panel whose key m
// opens a dialog, whose keys close it, open a second dialog over it and raise an intent that only ends when it is
// cancelled, and whose keys o, d and u open dialogs that declare a dismiss intent: the memo hides itself when that is
// raised, the deaf one answers and stays, and the mute one never answers;
// vlater mounts the first dialog two seconds later, and vdiag shows the notices the client left on it. With
// PIG_UI_VIEW_FIXTURE_MOUNTS=0 every command runs and makes no ui.view call, so a session
// that never mounted takes the same keys. With PIG_UI_VIEW_FIXTURE_ABILITIES=panel or =widget-above it mounts one view
// in that slot when it is told the abilities of the client that attached (OnViewAbilities), before any command; with
// =listen it takes that event and makes no ui.view call.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

const (
	panelKey   = "panel"
	widgetKey  = "note"
	arrivalKey = "arrival"
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

// open is the intent a hidden tab takes: its handler shows the tab.
var open = sdk.ViewIntent{Name: "viewfx.open", Arg: sdk.ViewShape{Type: "object"}, TimeoutMs: 5000, WhenHidden: true}

var tabTitles = map[string]string{"cal": "Calendar", "notes": "Notes"}

// tab is a titled panel with a list; request is the focus request of its list.
func tab(key, text, request string) sdk.ViewContent {
	list := kit.NewSelectList("pick", []kit.SelectItem{{Value: "alpha", Label: "Alpha item"}, {Value: "beta", Label: "Beta item"}}, 4)
	return sdk.ViewContent{
		Title:   tabTitles[key],
		View:    kit.View{Root: kit.NewContainer(kit.NewText(text, 1, 0), list), Focus: "pick"},
		Intents: []sdk.ViewIntent{pick, open},
		Focus:   []sdk.ViewFocus{{Node: "pick", Request: request}},
	}
}

// goTo is the intent the bound panel's keys raise.
var goTo = sdk.ViewIntent{
	Name: "viewfx.go", TimeoutMs: 5000,
	Arg: sdk.ViewShape{Type: "object", Properties: map[string]sdk.ViewShape{"n": {Type: "integer"}}},
}

// bound is a panel without a node that takes keys: d raises goTo with an argument and has a name, w raises it without
// one, and a swipe is bound too.
func bound(text string) sdk.ViewContent {
	return sdk.ViewContent{
		View:    kit.View{Root: kit.NewText(text, 1, 0)},
		Intents: []sdk.ViewIntent{goTo},
		Bindings: []sdk.ViewBinding{
			{Key: "d", Intent: goTo.Name, Arg: map[string]any{"n": 7}, Name: "Day"},
			{Key: "w", Intent: goTo.Name},
			{Gesture: "swipe-left", Intent: goTo.Name},
		},
	}
}

// The intents of the dialogs: a key of the places panel opens the map, and the keys of a dialog close it, open the
// legend over it and raise slow, whose handler ends only when the intent is cancelled.
var (
	object       = sdk.ViewShape{Type: "object"}
	openMap      = sdk.ViewIntent{Name: "viewfx.map", Arg: object, TimeoutMs: 5000}
	openLegend   = sdk.ViewIntent{Name: "viewfx.legend", Arg: object, TimeoutMs: 5000}
	closeDialog  = sdk.ViewIntent{Name: "viewfx.close", Arg: object, TimeoutMs: 5000}
	slow         = sdk.ViewIntent{Name: "viewfx.slow", Arg: object, TimeoutMs: 120000, Cancellable: true}
	openMemo     = sdk.ViewIntent{Name: "viewfx.memo", Arg: object, TimeoutMs: 5000}
	openDeaf     = sdk.ViewIntent{Name: "viewfx.deaf", Arg: object, TimeoutMs: 5000}
	openMute     = sdk.ViewIntent{Name: "viewfx.mute", Arg: object, TimeoutMs: 5000}
	dismiss      = sdk.ViewIntent{Name: "viewfx.dismiss", Arg: object, TimeoutMs: 120000, Dismiss: true}
	dialogTitles = map[string]string{"map": "Map", "legend": "Legend", "memo": "Memo", "deaf": "Deaf", "mute": "Mute"}
	dialogOpener = map[string]string{openMap.Name: "map", openLegend.Name: "legend", openMemo.Name: "memo", openDeaf.Name: "deaf", openMute.Name: "mute"}
	pickItems    = []kit.SelectItem{{Value: "alpha", Label: "Alpha item"}, {Value: "beta", Label: "Beta item"}}
)

// places is the panel a dialog is opened from: a list, and the key m.
func places(text string) sdk.ViewContent {
	return sdk.ViewContent{
		View:    kit.View{Root: kit.NewContainer(kit.NewText(text, 1, 0), kit.NewSelectList("pick", pickItems, 4)), Focus: "pick"},
		Intents: []sdk.ViewIntent{pick, openMap, openMemo, openDeaf, openMute},
		Bindings: []sdk.ViewBinding{
			{Key: "m", Intent: openMap.Name, Name: "map"}, {Key: "o", Intent: openMemo.Name, Name: "memo"},
			{Key: "d", Intent: openDeaf.Name}, {Key: "u", Intent: openMute.Name},
		},
		Focus: []sdk.ViewFocus{{Node: "pick", Request: "mount"}},
	}
}

// dialog is the content of the dialog key: a title, a list, and its keys. The memo, the deaf and the mute dialog bind
// no key that closes them: their second intent is the one the client raises when the user dismisses the dialog.
func dialog(key, text string) sdk.ViewContent {
	content := sdk.ViewContent{
		Title:   dialogTitles[key],
		View:    kit.View{Root: kit.NewContainer(kit.NewText(text, 1, 0), kit.NewSelectList("pick", pickItems, 4)), Focus: "pick"},
		Intents: []sdk.ViewIntent{pick, closeDialog, openLegend, slow},
		Bindings: []sdk.ViewBinding{
			{Key: "q", Intent: closeDialog.Name, Name: "close"},
			{Key: "l", Intent: openLegend.Name, Name: "legend"},
			{Key: "s", Intent: slow.Name, Name: "slow"},
		},
	}
	if key == "memo" || key == "deaf" || key == "mute" {
		content.Intents = []sdk.ViewIntent{pick, dismiss, slow}
		content.Bindings = []sdk.ViewBinding{{Key: "s", Intent: slow.Name, Name: "slow"}}
	}
	return content
}

func note(text string) sdk.ViewContent {
	return sdk.ViewContent{View: kit.View{Root: kit.NewText(text, 1, 0)}}
}

func main() {
	ext := sdk.New("viewfx")
	mounts := os.Getenv("PIG_UI_VIEW_FIXTURE_MOUNTS") != "0"
	var mu sync.Mutex
	var cancelled, dismissed atomic.Int64
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
	if arrival := os.Getenv("PIG_UI_VIEW_FIXTURE_ABILITIES"); arrival != "" {
		var once sync.Once
		ext.OnViewAbilities(func(ctx sdk.Context, abilities *sdk.ViewAbilities) {
			if arrival == "listen" || abilities == nil {
				return
			}
			once.Do(func() {
				if err := keep(ctx.MountView(sdk.ViewMount{Key: arrivalKey, Slot: sdk.ViewSlot(arrival), ViewContent: note("fixture arrival view")})); err != nil {
					ctx.Notify("viewfx abilities: "+err.Error(), "error")
				}
			})
		})
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
	command("vtabs", "Mount three titled panels and hide the third", func(ctx sdk.Context) error {
		mail := sdk.ViewContent{Title: "Mail", View: kit.View{Root: kit.NewText("mail rows", 1, 0)}}
		if err := keep(ctx.MountView(sdk.ViewMount{Key: "mail", Slot: sdk.ViewSlotPanel, ViewContent: mail})); err != nil {
			return err
		}
		for _, key := range []string{"cal", "notes"} {
			if err := keep(ctx.MountView(sdk.ViewMount{Key: key, Slot: sdk.ViewSlotPanel, ViewContent: tab(key, key+" rows", "")})); err != nil {
				return err
			}
		}
		return keep(ctx.HideView(ref("notes")))
	})
	command("vfocus", "Update the second tab with a focus request", func(ctx sdk.Context) error {
		return keep(ctx.UpdateView(sdk.ViewUpdate{Mount: ref("cal"), ViewContent: tab("cal", "cal asked", "update")}))
	})
	command("vbind", "Mount a panel with key bindings", func(ctx sdk.Context) error {
		return keep(ctx.MountView(sdk.ViewMount{Key: "bind", Slot: sdk.ViewSlotPanel, ViewContent: bound("bind rows")}))
	})
	command("vdialog", "Mount a panel whose key opens a dialog", func(ctx sdk.Context) error {
		return keep(ctx.MountView(sdk.ViewMount{Key: "places", Slot: sdk.ViewSlotPanel, ViewContent: places("places rows")}))
	})
	command("vlater", "Mount the map dialog two seconds from now", func(ctx sdk.Context) error {
		time.Sleep(2 * time.Second)
		return keep(ctx.MountView(sdk.ViewMount{Key: "map", Slot: sdk.ViewSlotDialog, ViewContent: dialog("map", "map rows")}))
	})
	command("vdiag", "Show the notices of the map dialog", func(ctx sdk.Context) error {
		notices, err := ctx.ViewDiagnostics("map")
		if err != nil {
			return err
		}
		said := make([]string, len(notices))
		for i, n := range notices {
			said[i] = n.Code + " " + n.Path + ": " + n.Message
		}
		ctx.Notify("map notices ["+strings.Join(said, "; ")+"]", "info")
		return nil
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
		failed := func(err error) sdk.ViewIntentResult {
			if err != nil {
				return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
			}
			return sdk.ViewIntentResult{Outcome: "done"}
		}
		switch in.Intent {
		case openMap.Name, openLegend.Name, openMemo.Name, openDeaf.Name, openMute.Name:
			// A key of the panel opens a dialog and a key of the map the legend: the first press mounts the dialog, a
			// later one shows the dialog that was hidden.
			key := dialogOpener[in.Intent]
			if hidden := ref(key); hidden.Key != "" {
				return failed(keep(in.Context.ShowView(hidden)))
			}
			return failed(keep(in.Context.MountView(sdk.ViewMount{Key: key, Slot: sdk.ViewSlotDialog, ViewContent: dialog(key, key+" rows")})))
		case closeDialog.Name:
			return failed(keep(in.Context.HideView(at)))
		case dismiss.Name:
			// The user dismissed a dialog: closing it is this handler's, and it says what the client sent. The memo's
			// closes it; the deaf and the mute dialog say it in their own rows and stay open, the deaf one answering
			// and the mute one never.
			asked := fmt.Sprintf("%s asked %d with %s", dialogTitles[in.Mount.Key], dismissed.Add(1), in.Arg)
			if in.Mount.Key == "memo" {
				in.Context.Notify(asked, "info")
				return failed(keep(in.Context.HideView(at)))
			}
			if err := keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: dialog(in.Mount.Key, asked)})); err != nil || in.Mount.Key == "deaf" {
				return failed(err)
			}
			<-in.Context.Done()
			return sdk.ViewIntentResult{Outcome: "failed", Error: "cancelled"}
		case slow.Name:
			// Ends only when the client cancels it. The next pick of any dialog shows how many were cancelled.
			<-in.Context.Done()
			cancelled.Add(1)
			return sdk.ViewIntentResult{Outcome: "failed", Error: "cancelled"}
		}
		if title, isDialog := dialogTitles[in.Mount.Key]; isDialog && in.Intent == pick.Name {
			text := fmt.Sprintf("%s picked %s after %d cancelled", title, arg.Value, cancelled.Load())
			return failed(keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: dialog(in.Mount.Key, text)})))
		}
		if in.Mount.Key == "places" {
			return failed(keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: places("places picked " + arg.Value)})))
		}
		if in.Intent == goTo.Name {
			// A key of the bound panel: the panel shows the argument the client raised the intent with.
			if err := keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: bound("bound " + string(in.Arg))})); err != nil {
				return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
			}
			return sdk.ViewIntentResult{Outcome: "done"}
		}
		if in.Intent == open.Name {
			// The user opened the hidden tab.
			if err := keep(in.Context.ShowView(at)); err != nil {
				return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
			}
			return sdk.ViewIntentResult{Outcome: "done"}
		}
		content := panel("picked " + arg.Value)
		if _, titled := tabTitles[in.Mount.Key]; titled {
			content = tab(in.Mount.Key, in.Mount.Key+" picked "+arg.Value, "")
		}
		if err := keep(in.Context.UpdateView(sdk.ViewUpdate{Mount: at, ViewContent: content})); err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		return sdk.ViewIntentResult{Outcome: "done"}
	})
	if err := ext.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
