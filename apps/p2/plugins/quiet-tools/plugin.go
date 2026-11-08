// Package quiettools is p2's quiet tool cards plugin (apps/p2/plugins/quiet-tools): it has a TUI facet only.
package quiettools

import (
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	quiettui "github.com/MichaelKinsy/PiG/apps/p2/plugins/quiet-tools/tui"
)

func init() {
	tuiFacet := quiettui.Facet
	hosts.RegisterPlugin(hosts.Plugin{Name: "quiet-tools", TUI: &tuiFacet})
}
