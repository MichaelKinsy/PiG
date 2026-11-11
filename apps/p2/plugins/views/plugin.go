package views

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	viewstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/views/tui"
)

func init() {
	server, worker, tuiFacet := Server, Worker, viewstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "views", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
