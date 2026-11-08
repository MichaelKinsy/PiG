package models

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	modelstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/tui"
)

func init() {
	tuiFacet := modelstui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "models", Server: &serverFacet, Worker: &workerFacet, TUI: &tuiFacet})
}
