package sessions

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
)

func init() {
	server, worker, tuiFacet := Server, Worker, sessionstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "sessions", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
