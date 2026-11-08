package sharing

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	sharingtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/tui"
)

func init() {
	server, tuiFacet := Server, sharingtui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "sharing", Server: &server, TUI: &tuiFacet})
}
