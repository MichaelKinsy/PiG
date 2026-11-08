package logs

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	logstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/tui"
)

func init() {
	server, tuiFacet := Server, logstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "logs", Server: &server, TUI: &tuiFacet})
}
