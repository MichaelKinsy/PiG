package workspace

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	workspacetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/tui"
)

func init() {
	server, worker, tuiFacet := ServerFacet, WorkerFacet, workspacetui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "workspace", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
