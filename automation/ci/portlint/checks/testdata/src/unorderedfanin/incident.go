package unorderedfanin

import "github.com/MichaelKinsy/PiG/coding/extension"

// The shape of ui_bridge.go: when a new UI context binds, the bridge re-applies every status it holds by ranging the map, so the statuses reach the new UI in Go's random map order. Pi's setStatus keeps insertion order. (A candidate: the order Pi shows statuses in was not verified against the footer.)

type uiBridge struct{ statuses map[string]string }

func (b *uiBridge) bind(ctx extension.UIContext) {
	for key, text := range b.statuses { // want `range over a map calls extension.UIContext.SetStatus`
		ctx.SetStatus(key, text)
	}
}

// The repair carries insertion order in a slice.
type statusEntry struct{ key, text string }

type orderedBridge struct{ statuses []statusEntry }

func (b *orderedBridge) bind(ctx extension.UIContext) {
	for _, s := range b.statuses {
		ctx.SetStatus(s.key, s.text)
	}
}
