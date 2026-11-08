package chatmode

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	chatmodetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/tui"
)

func init() {
	worker, tuiFacet := Worker, chatmodetui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "chat-mode", Worker: &worker, TUI: &tuiFacet})
}
