package surfaces

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/surfaces/theme-choice.ts. The user's theme choice (slice 11 §4b): one dark and one light
// theme id per principal, a preference the server keeps and every page and terminal of that principal holds, so choosing
// a theme restyles them all at once. An id is at most 128 characters.

var themeID = contracts.Union([]contracts.Schema{contracts.Str(contracts.Max(128)), contracts.Nul()})

var themePick = contracts.Obj(contracts.Prop("dark", themeID), contracts.Prop("light", themeID))

// ThemeChoice is the caller's theme choice, on the server: its pick, and `choose` to change it.
var ThemeChoice = func() *contracts.Contract {
	contract, err := PreferenceContract("chord.themes.choice", themePick)
	if err != nil {
		panic(err)
	}
	return contract
}()

// ThemeChoiceFacet is the kit's server facet for theme choices: the store of picks and each principal's ThemeChoice
// (no choice yet: the first contributed theme of each mode).
func ThemeChoiceFacet() (host.Facet, error) {
	return PreferenceFacet(ThemeChoice, "chord.themes.picks", themePick, delta.JsonObjectOf("dark", nil, "light", nil))
}
