package sendfile

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	sendfiletui "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/tui"
)

func init() {
	worker, tuiFacet := Facet(), sendfiletui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "send-file", Worker: &worker, TUI: &tuiFacet})
}
