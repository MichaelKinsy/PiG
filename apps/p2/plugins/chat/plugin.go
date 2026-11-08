package chat

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
)

func init() {
	worker, tuiFacet := Facet(), chattui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "chat", Worker: &worker, TUI: &tuiFacet})
}
