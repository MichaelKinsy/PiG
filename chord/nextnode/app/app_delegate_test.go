package app

import (
	"strconv"
	"testing"
	"time"
)

// Not upstream's (D605): the server host the kit gives the app is two hosts with the server's address, the Go host (the app's
// own facets: the locale and theme choices, the telemetry ingest) and the Node host (the plugins). Pi's one server host serves
// both, so a server plugin's object that holds a type the Go host serves binds. The Go host delegates the types it does not serve
// to the Node host (the server rows of the reload suite), and the Node host hands the types the Go host serves back over its link: this
// test is the second direction, the Node host's plugin holding the Go server's own remote types. Oracle: Pi's host, where
// `self.get(LocaleChoice, {})` in a server plugin's factory binds the one server host's object (as the object's own principal there;
// over the link it is the link's, the server's: D605 records it and the test pins it).

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

// languageWeb is the page's side of languagePlugin: the contracts it holds, registered for the probe (a page's own copies of them).
const languageWeb = `import { defineFacet, hostKind, method, nul, remote, str, union } from "@test/app/v1";
import { probe } from "@test/app/v1/web";
const Server = hostKind("server");
const Pick = union([str({ max: 35, pattern: "^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$" }), nul()]);
probe.contracts.choice = remote("chord.locale.choice", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { pick: Pick }, methods: { choose: method(Pick) } } } });
probe.contracts.lang = remote("probe.lang", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { pick: str(), theme: str() }, methods: { choose: method(Pick) } } } });
export default defineFacet(() => {});
`

// lastFrame is the last value the probe recorded of the view's watched member, as JSON ("" while it has none).
func lastFrame(tab *reloadTab, name string) string {
	return evalAs[string](tab, `JSON.stringify(globalThis.reload.frames[`+quoted(name)+`]?.at(-1)) ?? ""`)
}

// waitFrame waits until the view's last frame is the JSON text want.
func waitFrame(tab *reloadTab, name, want string) {
	tab.t.Helper()
	tab.browser.Wait(`JSON.stringify(globalThis.reload.frames[` + quoted(name) + `]?.at(-1)) === ` + quoted(want))
}

func TestAServerPluginsObjectHoldsTheTypesTheGoServerServesOverTheNodeHostsLinkAsTheServer(t *testing.T) {
	tab := startReloadTab(t)
	s := startReload(t, map[string]map[string]string{"lang": {"server.ts": languagePlugin, "web.ts": languageWeb}})
	tab.signIn(s)
	// The page holds the Go server's choice itself, as the owner.
	tab.hold("choice", "choice", 10*time.Second, "", "pick")
	tab.waitState("choice", "live")
	waitFrame(tab, "choice", "null")
	// The plugin's object holds the Go server's choice through the Node host's link to the Go host: it binds.
	tab.hold("lang", "lang", 10*time.Second, "", "pick")
	tab.waitState("lang", "live")
	waitFrame(tab, "lang", `"pick=null"`)
	// The theme choice, the other Go-served type the plugin's object holds.
	tab.hold("theme", "lang", 10*time.Second, "", "theme")
	waitFrame(tab, "theme", strconv.Quote(`{"dark":null,"light":null}`))
	// A call of the plugin's object reaches the Go server's object it holds and its view follows.
	if got := tab.result(tab.call("lang", "choose", "de")); got.Status != "ok" {
		t.Fatalf("the plugin's choose = %+v, want ok", got)
	}
	waitFrame(tab, "lang", `"pick=de"`)
	// The difference from Pi's one host that D605 records: a hold over the Node host's link acts as the link's principal, the
	// server's (a child cannot claim to be someone else, §4), where Pi's one host acts as the object's own principal. The plugin
	// object's choice is the server principal's, so the owner's, which the page holds, is untouched.
	tab.waitState("choice", "live")
	expectEqual(t, "the owner's choice", lastFrame(tab, "choice"), "null")
}
