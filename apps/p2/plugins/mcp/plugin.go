package mcp

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	mcptui "github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/tui"
)

func init() {
	server, worker, tuiFacet := Server, Worker, mcptui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "mcp", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
