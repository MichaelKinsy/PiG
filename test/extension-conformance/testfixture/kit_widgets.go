package testfixture

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// kitWidgetCases are the views of the widget kinds' Pi vectors
// (test/parity/sdk-vectors, docs/plan/extension-component-kit.md §10), built
// with the SDK's own constructors. A key is "<module>/<export>/<case name>";
// the case's wire node is its vector's `root`.
var kitWidgetCases = map[string]func() kit.View{}

func registerKitWidgetCase(key string, build func() kit.View) { kitWidgetCases[key] = build }

// kitWidgetProbe shows one case in a ui.custom overlay. It takes the keys and
// mouse events the view leaves to the extension, and notifies each callback
// its nodes fire as "kit-widget-event=<type>:<value>" (a change report is not
// one). It never closes itself: the harness delivers the case's steps and then
// reads the callbacks, and a probe that closed after the callbacks Pi fired
// would drop the steps still to come.
type kitWidgetProbe struct {
	ctx  sdk.Context
	view kit.View
}

func (p *kitWidgetProbe) View(int) kit.View { return p.view }

func (p *kitWidgetProbe) HandleInput(string) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

func (p *kitWidgetProbe) HandleMouse(sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	return sdk.RemoteComponentResult{}, nil
}

func (p *kitWidgetProbe) HandleViewEvent(event kit.Event) (sdk.RemoteComponentResult, error) {
	switch {
	case event.Type == kit.EventChange:
	case event.Type == kit.EventMouse && event.Mouse != nil:
		p.ctx.Notify(fmt.Sprintf("kit-widget-event=mouse:%s/%s/%d/%d", event.Mouse.Type, event.Mouse.Button, event.Mouse.X, event.Mouse.Y), "info")
	default:
		p.ctx.Notify("kit-widget-event="+event.Type+":"+event.Value, "info")
	}
	return sdk.RemoteComponentResult{}, nil
}

// registerKitWidgets adds kit-widget "<case key>": it opens the case in an
// overlay and reports its callbacks as the probe notifies them.
func registerKitWidgets(ext *sdk.Extension) {
	ext.Command("kit-widget", "Show a widget kind's Pi vector case (D107)", func(ctx sdk.Context, key string) error {
		build, ok := kitWidgetCases[key]
		if !ok {
			return fmt.Errorf("kit-widget: unknown case %q", key)
		}
		_, err := ctx.Custom(&kitWidgetProbe{ctx: ctx, view: build()}, sdk.RemoteOverlayOptions{Title: "Kit"})
		return err
	})
}
