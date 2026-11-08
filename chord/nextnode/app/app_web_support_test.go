package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	nextsurfaces "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
	"github.com/MichaelKinsy/PiG/internal/browsertest"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Test support for the web surface in a real browser (Pi's test/node/web/support.ts; slice 11 §6): a kit app with the web
// surface whose entry imports the fixture's web entry and a version (changing it changes the base bundle), its page template, a
// web plugin, and the browser signed in as the owner through the owner link. The browser is headless Chromium over the DevTools
// protocol (internal/browsertest); the tests are skipped without one (`CHROMIUM_BIN`).

// panelPlugin is a web plugin contributing the panel title and the themes accent colours.
func panelPlugin(title, accent string) map[string]string {
	colors := `{ accent: "` + accent + `", bg: "#000000", fg: "#ffffff", muted: "#888888", error: "#ff0000", warning: "#ffff00", success: "#00ff00" }`
	return map[string]string{
		"web.ts": strings.Join([]string{
			`import { defineFacet, Themes } from "@test/app/v1";`,
			`import { Panels } from "@test/app/v1/web";`,
			`export default defineFacet((_host, self) => {`,
			"\tself.contribute(Panels, " + quoted(title) + ");",
			`	self.contribute(Themes, { id: "test", name: "Test", mode: "dark", colors: ` + colors + ` });`,
			`});`,
		}, "\n"),
	}
}

// quoted is s as a JavaScript string literal.
func quoted(s string) string {
	text, err := jsstring.MarshalJSON(s)
	if err != nil {
		panic(err)
	}
	return string(text)
}

// versionedEntry writes a web entry importing the fixture's with version in dir (a new directory when empty): its path.
func versionedEntry(t testing.TB, version, dir string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	main := surfacesFixture(t, "web", "main.ts")
	text := "import { start } from " + quoted(main) + ";\nglobalThis.version = " + quoted(version) + ";\nstart();\n"
	path := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// freePort is a free port on loopback.
func freePort(t testing.TB) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

// browserPage is the tests' page template.
func browserPage(request chordhttp.HttpRequest, assets nodesurfaces.PageAssets) (string, error) {
	return `<!doctype html><html><head><meta charset="utf-8">` + nodesurfaces.PageScripts(assets, request.Nonce()) + `</head><body><div id="root"></div></body></html>`, nil
}

// webBrowserApp is a kit app with the web surface at port (fixed: a restarted server keeps it), the plugins, and more options.
func webBrowserApp(t testing.TB, entry string, port int, files map[string]map[string]string, more func(*AppOptions)) started {
	t.Helper()
	return startTestApp(t, files, func(o *AppOptions) {
		withWeb(entry, false)(o)
		// The web kind keeps React and the tests' web API as host packages: the page provides them (the fixture's entry).
		last := &o.Kinds[len(o.Kinds)-1]
		last.Kind.Packages = plugins.HostPackages{"react": "", "react/jsx-runtime": "", "@test/app/v1/web": surfacesFixture(t, "web", "api.ts")}
		o.Web.Page = browserPage
		// The vendored sources are the packages' `source` condition (as the terminal's tests).
		o.Web.Conditions = []string{"source"}
		o.Listen.Port = port
		if more != nil {
			more(o)
		}
	})
}

// signIn signs browser in with link (the owner link) and waits for the page's app.
func signIn(browser *browsertest.Browser, link string) {
	browser.Open(link)
	browser.Wait(`document.getElementById("panels")`)
}

// chooseLocale has the owner choose locale.
func chooseLocale(t testing.TB, s started, locale string) {
	t.Helper()
	_, err := s.app.Host.Root(func(ctx host.Ctx) (any, error) {
		choice, err := ctx.Get(nextsurfaces.LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		defer choice.Release()
		return choice.Call("choose", locale, ctx)
	}, context.Background(), host.RootOptions{Principal: testOwner})
	if err != nil {
		t.Fatal(err)
	}
}

// browserNumber is expression's value (a number) in the page.
func browserNumber(t testing.TB, b *browsertest.Browser, expression string) float64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		t.Fatal(err)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %s (%v)", expression, raw, err)
	}
	return value
}

// browserStrings is expression's value (an array of strings) in the page.
func browserStrings(t testing.TB, b *browsertest.Browser, expression string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		t.Fatal(err)
	}
	var value []string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %s (%v)", expression, raw, err)
	}
	return value
}

// browserBool is expression's truth in the page.
func browserBool(t testing.TB, b *browsertest.Browser, expression string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		t.Fatal(err)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %s (%v)", expression, raw, err)
	}
	return value
}

// browserStringTable is expression's value (an array of arrays of strings) in the page.
func browserStringTable(t testing.TB, b *browsertest.Browser, expression string) [][]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := b.Evaluate(ctx, expression)
	if err != nil {
		t.Fatal(err)
	}
	var value [][]string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("%s: %s (%v)", expression, raw, err)
	}
	return value
}
