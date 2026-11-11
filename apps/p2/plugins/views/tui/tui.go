// Package tui is the views plugin in the TUI: the terminal shows no panes, but a message that carried view context gets its
// note there too.
//
// Ports apps/p2/plugins/views/tui.ts and display.ts (viewInputDisplay).
package tui

import (
	"fmt"
	"strings"

	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// ViewInputDisplay is the note on a `p2.view-input` entry: how many selections and annotations it carried.
func ViewInputDisplay(data any) *chatcontract.EntryDisplay {
	object, _ := data.(*delta.JsonObject)
	if object == nil {
		return nil
	}
	views, _ := object.Value("views").(*delta.JsonObject)
	if views == nil {
		return nil
	}
	count := func(key string) int {
		value, _ := views.Value(key).(float64)
		if value > 0 && value == float64(int(value)) {
			return int(value)
		}
		return 0
	}
	plural := func(n int, word string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, word)
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	var parts []string
	if n := count("selections"); n > 0 {
		parts = append(parts, plural(n, "selection"))
	}
	if n := count("annotations"); n > 0 {
		parts = append(parts, plural(n, "annotation"))
	}
	if len(parts) == 0 {
		return nil
	}
	return &chatcontract.EntryDisplay{Note: "with " + strings.Join(parts, " and ") + " from views"}
}

// Facet is the views plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
	return chatcontract.TranscriptEntries.Contribute(self, p2runtime.Entry(contract.ViewInput, ViewInputDisplay))
})
