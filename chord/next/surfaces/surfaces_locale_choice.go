package surfaces

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/src/surfaces/locale-choice.ts. The user's locale choice (decision "Messages and languages in the
// app kit" (3)): one locale per principal, a preference the server keeps and every page and terminal of that principal
// holds. null until the user chooses: the device's languages decide.

// localePick is a language tag (`de`, `pt-BR`; at most 35 characters, BCP 47's practical bound), or null: the device's.
var localePick = contracts.Union([]contracts.Schema{
	contracts.Str(contracts.Max(35), contracts.Pattern("^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$")),
	contracts.Nul(),
})

// LocaleChoice is the caller's locale choice, on the server: its pick, and `choose` to change it.
var LocaleChoice = func() *contracts.Contract {
	contract, err := PreferenceContract("chord.locale.choice", localePick)
	if err != nil {
		panic(err)
	}
	return contract
}()

// LocaleChoiceFacet is the kit's server facet for locale choices: the store of picks and each principal's LocaleChoice.
func LocaleChoiceFacet() (host.Facet, error) {
	return PreferenceFacet(LocaleChoice, "chord.locale.picks", localePick, nil)
}
