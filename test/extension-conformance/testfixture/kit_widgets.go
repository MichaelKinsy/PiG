package testfixture

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// kitWidgetCases are the views of the widget kinds' Pi vectors
// (test/parity/sdk-vectors, docs/plan/extension-component-kit.md §10), built
// with the SDK's own constructors. A key is "<module>/<export>/<case name>";
// the case's wire node is its vector's `root`.
var kitWidgetCases = map[string]func() kit.View{}

func registerKitWidgetCase(key string, build func() kit.View) { kitWidgetCases[key] = build }

// kitWidgetProbe shows one case in a ui.custom overlay. It logs the keys and
// mouse events the view leaves to the extension and the callbacks its nodes
// fire, and closes with the log after want callbacks (never when want is 0).
type kitWidgetProbe struct {
	mu   sync.Mutex
	view kit.View
	want int
	log  []string
	n    int
}

func (p *kitWidgetProbe) View(int) kit.View { return p.view }

func (p *kitWidgetProbe) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, "input:"+data)
	return sdk.RemoteComponentResult{}, nil
}

func (p *kitWidgetProbe) HandleMouse(event sdk.MouseEvent) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, fmt.Sprintf("rawmouse:%s/%s/%d,%d", event.Type, event.Button, event.X, event.Y))
	return sdk.RemoteComponentResult{}, nil
}

func (p *kitWidgetProbe) HandleViewEvent(event kit.Event) (sdk.RemoteComponentResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := event.Type + ":" + event.Value
	if event.Type == kit.EventMouse && event.Mouse != nil {
		entry = fmt.Sprintf("mouse:%s/%s/%d/%d", event.Mouse.Type, event.Mouse.Button, event.Mouse.X, event.Mouse.Y)
	}
	p.log = append(p.log, entry)
	if event.Type != kit.EventChange {
		p.n++
	}
	if p.want > 0 && p.n >= p.want {
		return sdk.RemoteComponentResult{Done: true, Value: strings.Join(p.log, ",")}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

// registerKitWidgets adds kit-widget "<case key>[|<callbacks>]": it opens the
// case in an overlay and notifies "kit-widget=<log>" when it closes.
func registerKitWidgets(ext *sdk.Extension) {
	ext.Command("kit-widget", "Show a widget kind's Pi vector case (D107)", func(ctx sdk.Context, args string) error {
		key, count, _ := strings.Cut(args, "|")
		build, ok := kitWidgetCases[key]
		if !ok {
			return fmt.Errorf("kit-widget: unknown case %q", key)
		}
		want, _ := strconv.Atoi(count)
		probe := &kitWidgetProbe{view: build(), want: want}
		result, err := ctx.Custom(probe, sdk.RemoteOverlayOptions{Title: "Kit"})
		if err != nil {
			return err
		}
		text := ""
		if result != nil {
			text = fmt.Sprint(result)
		}
		ctx.Notify("kit-widget="+text, "info")
		return nil
	})
}
