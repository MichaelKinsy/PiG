package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// observation is one JSON object whose keys keep the order they were added in, as JSON.stringify of the Pi probe writes them.
type observation struct {
	keys   []string
	values []any
}

func (o *observation) add(key string, value any) *observation {
	o.keys = append(o.keys, key)
	o.values = append(o.values, value)
	return o
}

func (o *observation) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		v, _ := json.Marshal(o.values[i])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.String()
}

func inputs(got []string) []string {
	if got == nil {
		return []string{}
	}
	return got
}

// TestTerminalInputPipelineParity emits the observations test/parity/scenarios/tui-components/*-terminal-input-pipeline.toml compares with Pi 1.1.0's
// TuiBase.handleTerminalInput (test/parity/testdata/terminal-input-pipeline-pi.mjs). The cases are those of the Pi probe, in its order.
func TestTerminalInputPipelineParity(t *testing.T) {
	const debugKey, release = "\x1b[100;6u", "\x1b[97;1:3u"
	newUI := func() *TuiMainScreen { return newManualRenderTUI(&bytes.Buffer{}, 80, 24) }
	emit := func(name string, o *observation) {
		named := &observation{keys: append([]string{"case"}, o.keys...), values: append([]any{name}, o.values...)}
		fmt.Printf("terminal-input-observation:%s\n", named)
	}
	{
		ui := newUI()
		focus := &recordingInput{}
		ui.SetFocus(focus)
		var seen []string
		a := TuiInputListener(func(d string) *TuiInputResult {
			seen = append(seen, "a:"+d)
			if d == "c" {
				return &TuiInputResult{Consume: true}
			}
			return &TuiInputResult{Data: new(strings.ToUpper(d))}
		})
		b := TuiInputListener(func(d string) *TuiInputResult { seen = append(seen, "b:"+d); return nil })
		ui.AddInputListener(&a)
		ui.AddInputListener(&b)
		for _, d := range []string{"x", "c"} {
			ui.HandleTerminalInput(d)
		}
		emit("listeners-transform-then-consume", (&observation{}).add("listeners", inputs(seen)).add("focus", inputs(focus.got)))
	}
	{
		ui := newUI()
		focus := &recordingInput{}
		ui.SetFocus(focus)
		drop := TuiInputListener(func(string) *TuiInputResult { return &TuiInputResult{Data: new("")} })
		ui.AddInputListener(&drop)
		ui.HandleTerminalInput("x")
		emit("listener-empty-replacement-drops-input", (&observation{}).add("focus", inputs(focus.got)))
	}
	{
		ui := newUI()
		focus := &recordingInput{}
		ui.SetFocus(focus)
		calls := 0
		ui.HandleTerminalInput(debugKey)
		ui.SetOnDebug(func() { calls++ })
		ui.HandleTerminalInput(debugKey)
		emit("debug-key-needs-a-callback", (&observation{}).add("calls", calls).add("focus", inputs(focus.got)))
	}
	{
		ui := newUI()
		focus := &recordingInput{}
		ui.SetFocus(focus)
		calls := 0
		ui.SetOnDebug(func() { calls++ })
		swap := TuiInputListener(func(string) *TuiInputResult { return &TuiInputResult{Data: new("z")} })
		ui.AddInputListener(&swap)
		ui.HandleTerminalInput(debugKey)
		emit("listener-replaces-the-debug-key", (&observation{}).add("calls", calls).add("focus", inputs(focus.got)))
	}
	{
		ui := newUI()
		plain := &recordingInput{}
		ui.SetFocus(plain)
		ui.HandleTerminalInput(release)
		aware := &releaseAware{}
		ui.SetFocus(aware)
		ui.HandleTerminalInput(release)
		emit("key-release-needs-wants-key-release", (&observation{}).add("plain", inputs(plain.got)).add("aware", inputs(aware.got)))
	}
	{
		ui := newUI()
		focus := &recordingInput{}
		ui.SetFocus(focus)
		var seen []string
		see := TuiInputListener(func(d string) *TuiInputResult { seen = append(seen, d); return nil })
		ui.AddInputListener(&see)
		ui.HandleTerminalInput(release)
		emit("listeners-see-key-releases", (&observation{}).add("listeners", inputs(seen)).add("focus", inputs(focus.got)))
	}
	{
		ui := newUI()
		base, overlay := &recordingInput{}, &recordingInput{}
		ui.SetFocus(base)
		shown := true
		ui.ShowOverlay(overlay, OverlayOptions{visible: func(int, int) bool { return shown }})
		ui.HandleTerminalInput("a")
		shown = false
		ui.HandleTerminalInput("b")
		emit("overlay-visibility-callback-redirects-input", (&observation{}).add("overlay", inputs(overlay.got)).add("base", inputs(base.got)))
	}
}
