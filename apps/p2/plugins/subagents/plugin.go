package subagents

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	subagentstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/tui"
)

func init() {
	server := serverFacet
	worker := workerFacet()
	tuiFacet := subagentstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "subagents", Server: &server, Worker: &worker, TUI: &tuiFacet})
}
