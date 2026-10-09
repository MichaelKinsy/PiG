package subprocess

import (
	"encoding/json"
	"testing"
)

// The no-frontend cost of the component kit (D107) on the widget lines path:
// a widget_push frame of lines decoded off the wire, applied to its proxy
// and drawn. Compare with the same benchmark before the kit (spec §1: the
// cost of every existing extension is unchanged).
func BenchmarkWidgetPushLinesFrame(b *testing.B) {
	bridge := newTestBridge(&mockUIContext{})
	frames := [2][]byte{}
	for i, row := range []string{"\x1b[36m▶ playing\x1b[0m  Blue in Green", "\x1b[36m▶ playing\x1b[0m  So What"} {
		frame, err := json.Marshal(Envelope{Type: MsgWidgetPush, WidgetPush: &WidgetPushPayload{Key: "player", Lines: []string{row, "━━━━━━━━━━──────── 2:31 / 5:37"}, Width: 80}})
		if err != nil {
			b.Fatal(err)
		}
		frames[i] = frame
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		var env Envelope
		if err := json.Unmarshal(frames[i&1], &env); err != nil {
			b.Fatal(err)
		}
		pushWidgetForBenchmark(bridge, &env)
		bridge.mu.RLock()
		proxy := bridge.widgets["fixture:player"]
		bridge.mu.RUnlock()
		_ = proxy.Render(80)
		i++
	}
}

func pushWidgetForBenchmark(bridge *UIBridge, env *Envelope) {
	bridge.HandleWidgetPush("fixture", nil, env.WidgetPush)
}
