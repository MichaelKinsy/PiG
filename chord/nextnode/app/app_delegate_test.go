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
		Methods: []contracts.MethodEntry{contracts.Meth("choose", contracts.NewMethod(contracts.Union([]contracts.Schema{contracts.Str(), contracts.Nul()})))},
	})},
})

// lastFrame is the last value the page's view saw of its watched member, "" before one.
func lastFrame(page *reloadPage, name string) string {
	frames := page.frames(name)
	if len(frames) == 0 {
		return ""
	}
	return jsonText(frames[len(frames)-1])
}

func TestAServerPluginsObjectHoldsTheTypesTheGoServerServesAsThePrincipalItActsAs(t *testing.T) {
	s := startReloadApp(t, map[string]map[string]string{"lang": {"server.ts": languagePlugin}})
	page := signIn(t, s)
	// The page holds the Go server's choice itself, as the owner.
	page.hold("choice", surfaces.LocaleChoice, "", 0, "pick")
	page.until("the choice's first value", reloadWait, func() bool { return len(page.frames("choice")) > 0 })
	expectEqual(t, "the choice before", lastFrame(page, "choice"), "null")
	// The plugin's object, which acts as the owner, holds that same choice through the Node host's link to the Go host: it binds.
	page.hold("lang", languageContract, "", 0, "pick")
	page.waitState("lang", "live")
	page.until("the plugin's view of the choice", reloadWait, func() bool { return lastFrame(page, "lang") == `"pick=null"` })
	// The theme choice, the other Go-served type the plugin's object holds.
	page.hold("theme", languageContract, "", 0, "theme")
	page.until("the plugin's view of the theme choice", reloadWait, func() bool { return lastFrame(page, "theme") == "\"{\\\"dark\\\":null,\\\"light\\\":null}\"" })
	// A call of the plugin's object changes the Go server's object of the same principal.
	if got := page.result(page.call("lang", "choose", "de")); got.Status != "ok" {
		t.Fatalf("choose = %+v, want ok", got)
	}
	page.until("the page's own choice follows", reloadWait, func() bool { return lastFrame(page, "choice") == `"de"` })
	page.until("the plugin's view follows", reloadWait, func() bool { return lastFrame(page, "lang") == `"pick=de"` })
}
