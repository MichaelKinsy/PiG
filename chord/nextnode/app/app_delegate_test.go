package app

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/surfaces"
)

// Not upstream's (D605): the server host the kit gives the app is two hosts with the server's address, the Go host (the app's
// own facets: the locale and theme choices, the telemetry ingest) and the Node host (the plugins). Pi's one server host serves
// both, so a server plugin's object that holds a type the Go host serves binds. The Go host delegates the types it does not serve
// to the Node host (reload_server_test.go's rows), and the Node host hands the types the Go host serves back over its link: these
// tests are the second direction, the Node host's plugin holding the Go server's own remote types as the principal its object acts
// as. Oracle: Pi's host, where `self.get(LocaleChoice, {})` in a server plugin's factory binds the one server host's object.

// languagePlugin is a server plugin serving `probe.lang`, whose object holds the Go server's LocaleChoice: its `pick` is the
// choice's, its `choose` changes it; and `probe.theme`, which holds the ThemeChoice.
const languagePlugin = `import { defineFacet, hostKind, method, nul, obj, remote, str, union } from "@test/app/v1";
const Server = hostKind("server");
const Pick = union([str({ max: 35, pattern: "^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" }), nul()]);
const LocaleChoice = remote("chord.locale.choice", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { pick: Pick }, methods: { choose: method(Pick) } } } });
const ThemeId = union([str({ max: 128 }), nul()]);
const ThemePick = obj({ dark: ThemeId, light: ThemeId });
const ThemeChoice = remote("chord.themes.choice", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { pick: ThemePick }, methods: { choose: method(ThemePick) } } } });
const Lang = remote("probe.lang", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { pick: str(), theme: str() }, methods: { choose: method(Pick) } } } });
export default defineFacet((host) => host.serve(Lang, (_params, _principal, self) => {
	const choice = self.get(LocaleChoice, {});
	const theme = self.get(ThemeChoice, {});
	return { variant: "main", members: {
		pick: self.derive([choice.pick], ([pick]) => "pick=" + String(pick)),
		theme: self.derive([theme.pick], ([pick]) => JSON.stringify(pick)),
		choose: (value, ctx) => choice.choose(value, ctx),
	} };
}));
`

// languageContract is `probe.lang` as the page declares it.
var languageContract = mustRemote("probe.lang", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: counterAddress("server"),
	Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
		State:   []contracts.Property{contracts.Prop("pick", contracts.Str()), contracts.Prop("theme", contracts.Str())},
		Methods: []contracts.MethodEntry{contracts.Meth("choose", contracts.NewMethod(contracts.Union([]contracts.Schema{
			contracts.Str(contracts.Max(35), contracts.Pattern("^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$")), contracts.Nul(),
		})))},
	})},
})

// memberText is the member's current value of the page's view as JSON, "" while it has none.
func memberText(page *reloadPage, name, member string) string {
	value, err := page.view(name).ref.Member(member).Value()
	if err != nil {
		return ""
	}
	return jsonText(value)
}

func TestAServerPluginsObjectHoldsTheTypesTheGoServerServesAsThePrincipalItActsAs(t *testing.T) {
	s := startReloadApp(t, map[string]map[string]string{"lang": {"server.ts": languagePlugin}})
	page := signIn(t, s)
	// The page holds the Go server's choice itself, as the owner.
	page.hold("choice", surfaces.LocaleChoice, "", 0, "pick")
	page.waitState("choice", "live")
	page.until("the choice's first value", reloadWait, func() bool { return memberText(page, "choice", "pick") == "null" })
	// The plugin's object, which acts as the owner, holds that same choice through the Node host's link to the Go host: it binds.
	page.hold("lang", languageContract, "", 0, "pick")
	page.waitState("lang", "live")
	page.until("the plugin's view of the choice", reloadWait, func() bool { return memberText(page, "lang", "pick") == `"pick=null"` })
	// The theme choice, the other Go-served type the plugin's object holds.
	page.until("the plugin's view of the theme choice", reloadWait, func() bool {
		return memberText(page, "lang", "theme") == "\"{\\\"dark\\\":null,\\\"light\\\":null}\""
	})
	// The page's own call on the Go server's choice reaches the plugin's object, which holds the same object (the same principal)...
	if got := page.result(page.call("choice", "choose", "fr")); got.Status != "ok" {
		t.Fatalf("the page's choose = %+v, want ok", got)
	}
	page.until("the plugin's view follows the page's choice", reloadWait, func() bool { return memberText(page, "lang", "pick") == `"pick=fr"` })
	// ... and a call of the plugin's object reaches the page's.
	if got := page.result(page.call("lang", "choose", "de")); got.Status != "ok" {
		t.Fatalf("the plugin's choose = %+v, want ok", got)
	}
	page.until("the plugin's view follows its own call", reloadWait, func() bool { return memberText(page, "lang", "pick") == `"pick=de"` })
	page.until("the page's own choice follows the plugin's call", reloadWait, func() bool { return memberText(page, "choice", "pick") == `"de"` })
}
