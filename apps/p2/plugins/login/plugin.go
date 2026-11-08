package login

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	logintui "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/tui"
)

func init() {
	server, tuiFacet := Server, logintui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "login", Server: &server, TUI: &tuiFacet})
}
