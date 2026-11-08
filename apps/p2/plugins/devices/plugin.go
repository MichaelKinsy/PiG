package devices

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	devicestui "github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/tui"
)

func init() {
	server := Server
	tuiFacet := devicestui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "devices", Server: &server, TUI: &tuiFacet})
}
