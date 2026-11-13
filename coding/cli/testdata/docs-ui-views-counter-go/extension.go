// counter/extension.go: written from the documentation page ui-views alone (TestDocsUIViewsPageAloneTeachesAGoCounter), then
// corrected in four places for the Go names the page did not give: the node that takes an id, sdk.ViewDrawShape, and the
// answer as JSON. A counter for the terminal and a web page.
package counter

import (
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// Extension returns a new instance. PiG calls it at every load and reload.
func Extension() *sdk.Extension {
	var (
		mu    sync.Mutex
		mount *sdk.ViewRef
		count int
		scene bool
	)
	content := func() sdk.ViewContent {
		web := sdk.ViewWhen{Any: []sdk.ViewWhenAll{{All: []sdk.ViewClause{{Claim: "client.kind", Op: "equals", Value: "web"}}}}}
		rich := kit.NewMarkdown(fmt.Sprintf("# Counter\n\nThe count is **%d**.", count), 1, 0)
		alt := sdk.ViewAlternative{When: web, View: kit.View{Root: rich}}
		if scene {
			// A box takes no id in Go, so a scroll-view is the node that the drawing fills.
			chart := kit.NewScrollView("chart", kit.NewSpacer(4))
			alt.View = kit.View{Root: kit.NewContainer(rich, chart)}
			alt.Drawing = &sdk.ViewDrawing{
				Profile:  "scene",
				Node:     "chart",
				Viewport: &sdk.ViewViewport{X: 0, Y: 0, Width: 100, Height: 10, Fit: "contain"},
				Shapes: []sdk.ViewDrawShape{
					{Kind: "rect", X: 0, Y: 0, Width: float64(count*10 + 10), Height: 10, Fill: "#3366ff"},
				},
				Regions: []sdk.ViewRegion{{
					Key:      "add",
					Shape:    sdk.ViewDrawShape{Kind: "rect", X: 0, Y: 0, Width: 100, Height: 10},
					Gestures: []string{"activate"},
					Intent:   "counter.add",
					Name:     "Add one",
				}},
			}
		}
		return sdk.ViewContent{
			Title:        "Counter",
			View:         kit.View{Root: kit.NewText(fmt.Sprintf("Count: %d. Press a to add one.", count), 1, 0)},
			Alternatives: []sdk.ViewAlternative{alt},
			// The binding and the region raise the intent, so it has no NodeKey.
			Intents: []sdk.ViewIntent{{
				Name:   "counter.add",
				Arg:    sdk.ViewShape{Type: "object"},
				Answer: &sdk.ViewShape{Type: "object", Properties: map[string]sdk.ViewShape{"count": {Type: "integer"}}, Required: []string{"count"}},
			}},
			Bindings: []sdk.ViewBinding{{Key: "a", Intent: "counter.add", Name: "Add"}},
			Fallback: kit.NewText(fmt.Sprintf("Count: %d", count), 1, 0),
		}
	}
	// Mount once, when a client that draws panels is attached.
	show := func(ctx sdk.Context, abilities *sdk.ViewAbilities) {
		mu.Lock()
		defer mu.Unlock()
		// A drawing is sent only while a client draws the scene profile.
		scene = abilities != nil && abilities.Drawing != nil && slices.Contains(abilities.Drawing.Profiles, "scene")
		if abilities == nil || mount != nil || !slices.Contains(abilities.Slots, sdk.ViewSlotPanel) {
			return
		}
		status, err := ctx.MountView(sdk.ViewMount{Key: "counter", Slot: sdk.ViewSlotPanel, ViewContent: content()})
		if err != nil {
			ctx.Notify("counter: "+err.Error(), "error")
			return
		}
		mount = &status.Mount
	}

	ext := sdk.New("counter")
	ext.OnViewAbilities(show)
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		show(ctx, ctx.ViewAbilities())
		return nil, nil
	})
	ext.OnViewIntent(func(in sdk.ViewIntentInvocation) sdk.ViewIntentResult {
		if in.Intent != "counter.add" {
			return sdk.ViewIntentResult{Outcome: "refused", Error: "unknown intent"}
		}
		mu.Lock()
		defer mu.Unlock()
		if mount == nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: "not mounted"}
		}
		count++
		// Update through the handler's context, then answer.
		status, err := in.Context.UpdateView(sdk.ViewUpdate{Mount: *mount, ViewContent: content()})
		if err != nil {
			return sdk.ViewIntentResult{Outcome: "failed", Error: err.Error()}
		}
		mount = &status.Mount
		return sdk.ViewIntentResult{Outcome: "done", Answer: json.RawMessage(fmt.Sprintf(`{"count":%d}`, count))}
	})
	return ext
}
