package testfixture

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// D614: the redraw panel and the abilities watcher, the same way in every SDK fixture (conformance
// TestConformance_UIViewAnswerRedraw and TestConformance_UIViewAbilitiesEvent).

func textView(text string) kit.View { return kit.View{Root: &kit.Text{Text: text}} }

func redrawIntents() []sdk.ViewIntent {
	n := sdk.ViewShape{Type: "object", Properties: map[string]sdk.ViewShape{"n": {Type: "integer"}}, Required: []string{"n"}}
	rev := &sdk.ViewShape{Type: "object", Properties: map[string]sdk.ViewShape{"rev": {Type: "integer"}}, Required: []string{"rev"}}
	return []sdk.ViewIntent{{Name: "fx.redraw", Arg: n, Answer: rev}, {Name: "fx.foreign", Arg: n, Answer: rev}}
}

func describeAbilities(ab *sdk.ViewAbilities) string {
	if ab == nil {
		return "none"
	}
	slots := make([]string, len(ab.Slots))
	for i, s := range ab.Slots {
		slots[i] = string(s)
	}
	return fmt.Sprintf("slots=%s,mounts=%d", strings.Join(slots, "+"), ab.Limits.Mounts)
}

// registerViewRows registers the redraw panel and the abilities watcher and returns the handler of the panel's intents.
// Their state belongs to this extension: a fixture linked into the host's process is made once for every harness.
func registerViewRows(ext *sdk.Extension) func(sdk.ViewIntentInvocation) sdk.ViewIntentResult {
	// viewRedrawCommand is the context of the command that mounted the redraw panel. An update made through it is the
	// extension's, not an intent handler's.
	var viewRedrawCommand struct {
		sync.Mutex
		ctx sdk.Context
	}
	// viewWatch is the state of the abilities watcher: it reports and mounts only once ui_view_watch armed it, and
	// blocks on its next event after ui_view_block until ui_view_release.
	var viewWatch struct {
		sync.Mutex
		armed, block bool
		events       int
		release      chan struct{}
	}
	ext.Command("ui_view_redraw", "Mount the panel whose intents redraw it", func(ctx sdk.Context, args string) error {
		st, err := ctx.MountView(sdk.ViewMount{Key: "redraw", Slot: sdk.ViewSlotPanel, View: textView("fixture-redraw-0"), Intents: redrawIntents()})
		if err != nil {
			return err
		}
		viewRedrawCommand.Lock()
		viewRedrawCommand.ctx = ctx
		viewRedrawCommand.Unlock()
		ctx.Notify(fmt.Sprintf("view-redraw:rev=%d", st.Mount.Rev), "info")
		return nil
	})
	ext.OnViewAbilities(func(ctx sdk.Context, ab *sdk.ViewAbilities) {
		viewWatch.Lock()
		viewWatch.events++
		armed, block, release := viewWatch.armed, viewWatch.block, viewWatch.release
		viewWatch.block = false
		viewWatch.Unlock()
		if !armed {
			return
		}
		if block {
			ctx.Notify("view-event-blocked", "info")
			<-release
			ctx.Notify("view-event-released", "info")
		}
		if ab == nil {
			ctx.Notify("view-event:none", "info")
			return
		}
		ctx.Notify(fmt.Sprintf("view-event:%s,same=%t", describeAbilities(ab), describeAbilities(ab) == describeAbilities(ctx.ViewAbilities())), "info")
		st, err := ctx.MountView(sdk.ViewMount{Key: "arrived", Slot: sdk.ViewSlotPanel, View: textView(fmt.Sprintf("fixture-arrived-%d", ab.Limits.Mounts))})
		if err != nil {
			code := "none"
			if ve, ok := errors.AsType[*sdk.ViewError](err); ok {
				code = string(ve.Code)
			}
			ctx.Notify("view-arrived:"+code, "info")
			return
		}
		ctx.Notify("view-arrived:"+string(st.State), "info")
	})
	ext.Command("ui_view_watch", "Arm the abilities watcher", func(ctx sdk.Context, args string) error {
		viewWatch.Lock()
		viewWatch.armed = true
		viewWatch.Unlock()
		ctx.Notify("view-watch:armed", "info")
		return nil
	})
	ext.Command("ui_view_block", "Block the abilities watcher on its next event", func(ctx sdk.Context, args string) error {
		viewWatch.Lock()
		viewWatch.block, viewWatch.release = true, make(chan struct{})
		viewWatch.Unlock()
		ctx.Notify("view-block:set", "info")
		return nil
	})
	ext.Command("ui_view_release", "Release the blocked abilities watcher", func(ctx sdk.Context, args string) error {
		viewWatch.Lock()
		release := viewWatch.release
		viewWatch.release = nil
		viewWatch.Unlock()
		if release != nil {
			close(release)
		}
		ctx.Notify("view-release:done", "info")
		return nil
	})
	ext.Command("ui_view_events", "Report how often the abilities watcher ran", func(ctx sdk.Context, args string) error {
		viewWatch.Lock()
		events := viewWatch.events
		viewWatch.Unlock()
		ctx.Notify(fmt.Sprintf("view-events:%d", events), "info")
		return nil
	})
	// fx.redraw and fx.foreign both replace the view of the mount the intent came from and answer done with the new rev.
	// fx.redraw updates through the handler's context; fx.foreign through the context of the command that mounted the
	// panel.
	return func(in sdk.ViewIntentInvocation) sdk.ViewIntentResult {
		var arg struct {
			N int `json:"n"`
		}
		_ = json.Unmarshal(in.Arg, &arg)
		ctx, name := in.Context, "redraw"
		if in.Intent == "fx.foreign" {
			viewRedrawCommand.Lock()
			ctx, name = viewRedrawCommand.ctx, "foreign"
			viewRedrawCommand.Unlock()
		}
		limit := 0
		if ab := in.Context.ViewAbilities(); ab != nil {
			limit = ab.Limits.IntentArgBytes
		}
		st, err := ctx.UpdateView(sdk.ViewUpdate{
			Mount:   sdk.ViewRef{Key: in.Mount.Key, Epoch: in.Mount.Epoch, Rev: in.Rev},
			View:    textView(fmt.Sprintf("fixture-%s-%d", name, arg.N+limit)),
			Intents: redrawIntents(),
		})
		if err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		return sdk.ViewIntentResult{Outcome: "done", Answer: json.RawMessage(fmt.Sprintf(`{"rev":%d}`, st.Mount.Rev))}
	}
}
