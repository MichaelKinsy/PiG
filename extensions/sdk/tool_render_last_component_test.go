package sdk

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

// ToolRenderContext.LastComponent is what the card's renderer of the same phase returned on its previous run: tool-execution.ts keeps one slot per
// phase, sets it to the returned component and clears it when the renderer throws. A run that fails leaves the next run no component.
func TestToolRenderLastComponentIsThePreviousRunAndNilAfterAFailedRun(t *testing.T) {
	h := startViewHost(t, `{"hasUI":true}`, func(ext *Extension) {
		ext.Tool("probe", "probe", Schema{"type": "object"}, func(Context, map[string]any) (any, error) { return nil, nil })
		ext.SetToolRenderers("probe", ToolRenderers{
			Call: func(_ Context, args map[string]any, render ToolRenderContext, _ int) ([]string, error) {
				last := "none"
				if render.LastComponent != nil {
					last = strings.Join(render.LastComponent.Lines, "|")
				}
				if args["fail"] == true {
					return nil, errors.New("boom")
				}
				return []string{fmt.Sprintf("run%v:last=%s", args["n"], last)}, nil
			},
			ResultView: func(_ Context, _ ToolRenderResult, _ ToolRenderResultOptions, render ToolRenderContext, _ int) (kit.View, error) {
				kind := "none"
				if last := render.LastComponent; last != nil {
					kind = "lines"
					if last.View != nil {
						kind = "view"
					}
				}
				return kit.View{Root: kit.NewText("result:last="+kind, 0, 0)}, nil
			},
		})
	})
	render := func(card, phase string, args map[string]any) viewFrame {
		h.t.Helper()
		h.request("render_tool", "probe", map[string]any{"card": card, "phase": phase, "width": 40, "args": args})
		return h.next(msgResponse, "")
	}
	call := func(card string, args map[string]any) string {
		h.t.Helper()
		frame := render(card, "call", args)
		if frame.errMsg != "" {
			return "error"
		}
		return string(frame.body)
	}
	for step, want := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"n": 1}, `{"lines":["run1:last=none"]}`},
		{map[string]any{"n": 2}, `{"lines":["run2:last=run1:last=none"]}`},
		{map[string]any{"n": 3, "fail": true}, "error"},
		// the failed run left no component: the next run starts from nothing, not from run 2's lines
		{map[string]any{"n": 4}, `{"lines":["run4:last=none"]}`},
		{map[string]any{"n": 5}, `{"lines":["run5:last=run4:last=none"]}`},
	} {
		if got := call("c1", want.args); got != want.want {
			t.Fatalf("call run %d = %s, want %s", step+1, got, want.want)
		}
	}
	// a card of its own starts from nothing
	if got, want := call("c2", map[string]any{"n": 1}), `{"lines":["run1:last=none"]}`; got != want {
		t.Fatalf("another card's first run = %s, want %s", got, want)
	}
	// the result phase has a slot of its own, and a view stands in for the component it returned
	for step, want := range []string{"result:last=none", "result:last=view"} {
		frame := render("c1", "result", nil)
		if frame.errMsg != "" || !strings.Contains(string(frame.body), want) {
			t.Fatalf("result run %d = %s (%s), want it to carry %q", step+1, frame.body, frame.errMsg, want)
		}
	}
}
