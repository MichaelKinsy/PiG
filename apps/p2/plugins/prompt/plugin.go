package prompt

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	prompttui "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt/tui"
)

func init() {
	worker, tuiFacet := WorkerFacet, prompttui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "prompt", Worker: &worker, TUI: &tuiFacet})
}
