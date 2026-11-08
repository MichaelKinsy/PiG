package surfaces

import (
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/test/surfaces/active-theme.test.ts

func solid(id, mode string) Theme {
	return Theme{ID: id, Name: id, Mode: mode, Colors: map[string]string{
		"accent": "#111111", "bg": "#000000", "fg": "#ffffff", "muted": "#888888", "error": "#f00", "warning": "#ff0", "success": "#0f0",
	}}
}

// themeSetup is a server with theme choices, and a client of user with themes contributed by plugins; server is returned
// for a second client.
func themeSetup(t testing.TB, user string, themes ...Theme) (server, client host.Host) {
	t.Helper()
	facet, err := ThemeChoiceFacet()
	if err != nil {
		t.Fatal(err)
	}
	clients, server, closeAll := serverAndClients(t, ":memory:", facet, "chord.themes.choice", user)
	t.Cleanup(closeAll)
	client = clients[0]
	return server, client
}

func TestFollowsTheRegistryThePrincipalsPickAndTheMode(t *testing.T) {
	facet, err := ThemeChoiceFacet()
	if err != nil {
		t.Fatal(err)
	}
	clients, _, closeAll := serverAndClients(t, ":memory:", facet, "chord.themes.choice", "ada")
	defer closeAll()
	client := clients[0]
	var plugins []host.HostFacet
	plugins = append(plugins, host.HostFacet{Name: "chord.themes", Facet: ThemesFacet()})
	for i, theme := range []Theme{solid("night", "dark"), solid("dusk", "dark"), solid("paper", "light")} {
		plugins = append(plugins, host.HostFacet{Name: "@test/theme-" + string(rune('0'+i)), Facet: host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return self.Contribute(registries.Registry[any, any](Themes), theme)
		})})
	}
	if report, err := client.Load(plugins, bg); err != nil || len(report.Failed) != 0 {
		t.Fatalf("load: %v %v", err, report.Failed)
	}
	var mu sync.Mutex
	var seen []string
	last := func() string { mu.Lock(); defer mu.Unlock(); return seen[len(seen)-1] }
	follower, err := FollowActiveTheme(client, "dark", func(active Theme) { mu.Lock(); seen = append(seen, active.ID); mu.Unlock() }, bg)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Stop()
	until(t, func() bool { return last() == "night" })
	choice, err := client.Get(bg, ThemeChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer choice.Release()
	if _, err := choice.Call("choose", delta.JsonObjectOf("dark", "dusk", "light", nil), bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return last() == "dusk" })
	follower.SetMode("light")
	if got := last(); got != "paper" {
		t.Errorf("after the mode changed = %q, want paper", got)
	}
}

func TestThemeChoicesRefuseAnIDOver128Characters(t *testing.T) {
	_, client := themeSetup(t, "ada")
	choice, err := client.Get(bg, ThemeChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer choice.Release()
	_, err = choice.Call("choose", delta.JsonObjectOf("dark", strings.Repeat("x", 129), "light", nil), bg)
	requireInvalidArgs(t, err)
	if _, err := choice.Call("choose", delta.JsonObjectOf("dark", strings.Repeat("x", 128), "light", nil), bg); err != nil {
		t.Fatal(err)
	}
}

func TestThemeChoicesAreKeptPerPrincipal(t *testing.T) {
	server, client := themeSetup(t, "ada")
	choice, err := client.Get(bg, ThemeChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer choice.Release()
	none := func(v delta.JsonValue) bool {
		o, ok := v.(*delta.JsonObject)
		return ok && o.Value("dark") == nil && o.Value("light") == nil
	}
	if !none(pickOf(t, choice)) {
		t.Fatalf("pick = %v, want none", pickOf(t, choice))
	}
	if _, err := choice.Call("choose", delta.JsonObjectOf("dark", "dusk", "light", "paper"), bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { o, ok := pickOf(t, choice).(*delta.JsonObject); return ok && o.Value("dark") == "dusk" })
	other := connectUser(t, server, "grace")
	defer other.Close(bg)
	graces, err := other.Get(bg, ThemeChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer graces.Release()
	if !none(pickOf(t, graces)) {
		t.Errorf("grace's pick = %v, want none", pickOf(t, graces))
	}
}
