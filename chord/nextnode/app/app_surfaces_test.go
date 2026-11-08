package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	nextsurfaces "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
)

// Ports packages/chord/test/node/surfaces/web.test.ts (Pi main c23b9ec1: the web surface's server side through the app kit) and
// tests the kit's surface steps (app_surfaces.go, Pi's `surfacesOf` and the `web`/`tui` options of app.ts): the surfaces' base
// hashes, their order in the rollout (the server's step, the workers', then the web's and the terminal's), the base marks their
// bundles' inputs give, and a start that fails when a base bundle does not build.
//
// Forced differences: none beyond the app kit's (D601: the client tree is vendored; the Go app serves it through the same
// routes). The tests' page template is Pi's, the HTTP requests go through net/http, and the dirty mark is awaited through the
// plugins state's watch (Pi polls it).

// surfacesFixture is the vendored fixtures' dir (the terminal entry the kit's terminal surface bundles).
func surfacesFixture(t testing.TB, path ...string) string {
	t.Helper()
	full, err := filepath.Abs(filepath.Join(append([]string{"..", "surfaces", "client", "pi", "chord", "test", "node", "fixtures"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return full
}

// webEntry writes a web entry (with a stylesheet) in a new directory: its path.
func webEntry(t testing.TB, text string) string {
	t.Helper()
	dir := t.TempDir()
	for file, body := range map[string]string{"style.css": ":root { --brand: red; }\n", "main.ts": "import \"./style.css\";\n" + text} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "main.ts")
}

const webStarted = "globalThis.started = true;\n"

// webPlugin is a web plugin's files: a facet with a stylesheet.
var webPlugin = map[string]string{
	"web.ts":    "import { defineFacet } from \"@test/app/v1\";\nimport \"./notes.css\";\nexport default defineFacet(() => {});",
	"notes.css": ".notes { color: var(--accent); }",
}

// webPage is the page template of these tests: the assets' tags in a minimal document.
func webPage(request chordhttp.HttpRequest, assets nodesurfaces.PageAssets) (string, error) {
	return "<!doctype html><title>t</title>" + nodesurfaces.PageScripts(assets, request.Nonce()) + "<div id=root></div>", nil
}

// withWeb adds the web kind and the web surface (entry, public or not) to the options.
func withWeb(entry string, public bool) func(*AppOptions) {
	return func(o *AppOptions) {
		o.Kinds = append(o.Kinds, AppKind{Name: "web", Platform: plugins.PlatformBrowser})
		o.Web = &AppWeb{Entry: entry, Page: webPage, Public: public}
	}
}

// surfaceGet is a GET of path on port with headers.
func surfaceGet(t testing.TB, port int, path string, headers map[string]string) reply {
	t.Helper()
	ctx, cancel := context.WithTimeout(background, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return reply{status: response.StatusCode, headers: response.Header, body: body}
}

// ownerHeaders is a bearer header of a new owner session of app.
func ownerHeaders(t testing.TB, app *App) map[string]string {
	t.Helper()
	sessions, err := app.Host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer sessions.Release()
	issued, err := sessions.Call("issue", delta.JsonObjectOf("principal", delta.JsonObjectOf("kind", "owner"), "label", "t", "kind", "device"), background)
	if err != nil {
		t.Fatal(err)
	}
	return bearer(issued.(*delta.JsonObject).Value("token").(string))
}

// webScripts is the current set of the web scripts object (release it with the returned function).
func webScripts(t testing.TB, app *App) (func() nextsurfaces.ScriptSet, func()) {
	t.Helper()
	scripts, err := app.Host.Get(background, nextsurfaces.WebScripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current := func() nextsurfaces.ScriptSet {
		value, err := scripts.Member("set").Value()
		if err != nil {
			t.Fatal(err)
		}
		return nextsurfaces.ScriptSetOf(value)
	}
	return current, scripts.Release
}

// urlsOf are the `src`s and `href`s of a page, in order.
func urlsOf(html string) []string {
	var urls []string
	for _, found := range regexp.MustCompile(`(?:src|href)="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		urls = append(urls, found[1])
	}
	return urls
}

// untilDirty returns once plugin is among the dirty plugins; the plugins state's watch wakes it.
func untilDirty(t testing.TB, app *App, plugin string) {
	t.Helper()
	untilMarks(t, app, plugin+" dirty", func(m plugins.PluginsState) bool {
		for _, name := range m.Dirty {
			if name == plugin {
				return true
			}
		}
		return false
	})
}

// untilBase returns once the plugins' base mark is set.
func untilBase(t testing.TB, app *App) {
	t.Helper()
	untilMarks(t, app, "the base mark", func(m plugins.PluginsState) bool { return m.Base })
}

func TestWebSurfaceServesThePageWithTheBootstrapTheScriptsAndTheStylesheetsTheFilesForever(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": webPlugin}, withWeb(webEntry(t, webStarted), false))
	headers := ownerHeaders(t, s.app)
	answer := surfaceGet(t, s.port, "/", headers)
	if answer.status != 200 {
		t.Fatalf("page = %d %s", answer.status, answer.body)
	}
	html := string(answer.body)
	csp := answer.headers.Get("Content-Security-Policy")
	found := regexp.MustCompile(`script-src [^;]*'nonce-([^']+)'`).FindStringSubmatch(csp)
	if found == nil {
		t.Fatalf("no script nonce in the CSP %q", csp)
	}
	nonce := found[1]
	base := s.app.Web.Base
	if want := `<script nonce="` + nonce + `">globalThis.chordPage={"base":"` + base + `"`; !strings.Contains(html, want) {
		t.Fatalf("the page has no bootstrap %q: %s", want, html)
	}
	if !strings.Contains(csp, "style-src 'self'") {
		t.Fatalf("csp = %q", csp)
	}
	// Scripts from the surface's files and the nonce only, not any same-origin response (Opus L8).
	if want := "script-src http://127.0.0.1:" + strconv.Itoa(s.port) + "/_chord/web/ 'nonce-" + nonce + "'"; !strings.Contains(csp, want) {
		t.Fatalf("csp = %q, want it to contain %q", csp, want)
	}
	urls := urlsOf(html)
	if len(urls) != 4 || urls[0] != "/_chord/web/base."+base+".css" || urls[3] != "/_chord/web/base."+base+".js" ||
		!regexp.MustCompile(`^/_chord/web/plugin/@test~notes\.[0-9a-f]+\.[0-9a-f]{16}\.css$`).MatchString(urls[1]) ||
		!regexp.MustCompile(`^/_chord/web/plugins\.[0-9a-f]{16}\.js$`).MatchString(urls[2]) {
		t.Fatalf("urls = %v", urls)
	}
	for _, url := range urls {
		file := surfaceGet(t, s.port, url, headers)
		if file.status != 200 || file.headers.Get("Cache-Control") != "private, max-age=31536000, immutable" {
			t.Fatalf("%s = %d, cache-control %q", url, file.status, file.headers.Get("Cache-Control"))
		}
	}
	script := surfaceGet(t, s.port, urls[3], headers)
	if got := script.headers.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if !strings.Contains(string(script.body), "globalThis.started=!0") {
		t.Fatalf("the base script does not start the app: %.200s", script.body)
	}
	if style := surfaceGet(t, s.port, urls[0], headers); !strings.Contains(string(style.body), "--brand:") {
		t.Fatalf("the base stylesheet: %s", style.body)
	}
}

func TestWebSurfaceAnswersTheCurrentBaseHashPubliclyAndRefusesThePageAndFilesToARequestWithoutASession(t *testing.T) {
	s := startTestApp(t, nil, withWeb(webEntry(t, webStarted), false))
	base := surfaceGet(t, s.port, "/_chord/web/base", nil)
	if base.status != 200 || string(base.body) != `"`+s.app.Web.Base+`"` || base.headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("base = %d %s (cache-control %q)", base.status, base.body, base.headers.Get("Cache-Control"))
	}
	if status := surfaceGet(t, s.port, "/", nil).status; status != 401 {
		t.Fatalf("page without a session = %d, want 401", status)
	}
	if status := surfaceGet(t, s.port, "/_chord/web/base."+s.app.Web.Base+".js", nil).status; status != 401 {
		t.Fatalf("base script without a session = %d, want 401", status)
	}
	// One app at a time: the leak check of each app's cleanup would see the other's goroutines.
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	open := startTestApp(t, nil, withWeb(webEntry(t, webStarted), true))
	if status := surfaceGet(t, open.port, "/", nil).status; status != 200 {
		t.Fatalf("public page = %d, want 200", status)
	}
}

func TestWebSurfaceRebuildsTheBaseBundleAtEachStartTheSameCodeGivesTheSameHashOtherCodeAnother(t *testing.T) {
	main := webEntry(t, webStarted)
	first := startTestApp(t, nil, withWeb(main, false))
	hash := first.app.Web.Base
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	restart := func() string {
		again := startTestApp(t, nil, func(o *AppOptions) {
			withWeb(main, false)(o)
			o.DataDir, o.Root = first.dataDir, first.root
		})
		hash := again.app.Web.Base
		if err := again.app.Close(background); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	if got := restart(); got != hash {
		t.Fatalf("the same code gave %s, want %s", got, hash)
	}
	if err := os.WriteFile(main, []byte("import \"./style.css\";\nglobalThis.started = \"changed\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := restart(); got == hash {
		t.Fatalf("other code gave the same hash %s", hash)
	}
}

func TestWebSurfaceServesEachWebFacetAsAScriptThatRegistersItselfAndTheSetAsOneCombinedFileNoEval(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": webPlugin}, withWeb(webEntry(t, webStarted), false))
	headers := ownerHeaders(t, s.app)
	current, release := webScripts(t, s.app)
	defer release()
	set := current()
	if set.Base != s.app.Web.Base || len(set.Plugins) != 1 {
		t.Fatalf("set = %+v, want the base %s and one plugin", set, s.app.Web.Base)
	}
	entry := set.Plugins[0]
	if entry.Name != "@test/notes" || !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(entry.Digest) ||
		entry.URL != "/_chord/web/plugin/@test~notes."+entry.Digest+".js" || entry.CSS == nil ||
		!regexp.MustCompile(`^/_chord/web/plugin/@test~notes\.`+entry.Digest+`\.[0-9a-f]{16}\.css$`).MatchString(*entry.CSS) {
		t.Fatalf("entry = %+v", entry)
	}
	script := string(surfaceGet(t, s.port, entry.URL, headers).body)
	if !strings.HasPrefix(script, `chordFacet("@test/notes","`+entry.Digest+`",(function (require, module, exports) {`) {
		t.Fatalf("script = %.160s", script)
	}
	if regexp.MustCompile(`\beval\(|new Function`).MatchString(script) {
		t.Fatal("the script evaluates a string")
	}
	combined := string(surfaceGet(t, s.port, set.Combined, headers).body)
	// The combined file without source maps (a browser reads one per file); each script keeps its own (Fable L8).
	if !regexp.MustCompile(`\n//# sourceMappingURL=data:application/json;base64,`).MatchString(script) {
		t.Fatal("the script has no source map of its own")
	}
	if strings.Contains(combined, "sourceMappingURL") {
		t.Fatal("the combined file has a source map")
	}
	if want := regexp.MustCompile(`\n//# sourceMappingURL=data:[^\n]*`).ReplaceAllString(script, ""); combined != want {
		t.Fatal("the combined file is not the script without its source map")
	}
}

func TestWebSurfaceKeepsAPluginsAssetsWhileItKeepsItsOldScriptAfterFailedBuildsOpusL4FableM3(t *testing.T) {
	pictured := map[string]string{
		"web.ts":   "import { defineFacet } from \"@test/app/v1\";\nimport icon from \"./icon.svg?url\";\nexport default defineFacet(() => { void icon; });",
		"icon.svg": "<svg/>",
	}
	s := startTestApp(t, map[string]map[string]string{"pictured": pictured, "notes": webPlugin}, withWeb(webEntry(t, webStarted), false))
	headers := ownerHeaders(t, s.app)
	current, release := webScripts(t, s.app)
	defer release()
	var script string
	for _, entry := range current().Plugins {
		if entry.Name == "@test/pictured" {
			script = string(surfaceGet(t, s.port, entry.URL, headers).body)
		}
	}
	found := regexp.MustCompile(`"(/_chord/web/assets/icon-[^"]+)"`).FindStringSubmatch(script)
	if found == nil {
		t.Fatalf("the pictured script names no asset: %.300s", script)
	}
	if err := os.WriteFile(filepath.Join(s.root, "pictured", "web.ts"), []byte("export default {"), 0o644); err != nil {
		t.Fatal(err)
	}
	for round := range 6 {
		if err := os.WriteFile(filepath.Join(s.root, "notes", "notes.css"), []byte(".notes { order: "+strconv.Itoa(round)+"; }"), 0o644); err != nil {
			t.Fatal(err)
		}
		untilDirty(t, s.app, "@test/notes")
		if _, err := s.app.ApplyChanges(background); err != nil {
			t.Fatal(err)
		}
	}
	if status := surfaceGet(t, s.port, found[1], headers).status; status != 200 {
		t.Fatalf("the asset %s = %d after six rollouts with the plugin's build failing", found[1], status)
	}
}

func TestWebSurfacePublishesANewSetOnARolloutWithoutWaitingForClientsThePreviousFilesStayServed(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"notes": webPlugin}, withWeb(webEntry(t, webStarted), false))
	headers := ownerHeaders(t, s.app)
	current, release := webScripts(t, s.app)
	defer release()
	before := current()
	if err := os.WriteFile(filepath.Join(s.root, "notes", "notes.css"), []byte(".notes { color: var(--fg); }"), 0o644); err != nil {
		t.Fatal(err)
	}
	untilDirty(t, s.app, "@test/notes")
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, step := range result.Steps {
		names = append(names, step.Name)
	}
	if !reflect.DeepEqual(names, []string{"server", "web"}) {
		t.Fatalf("steps = %v, want [server web]", names)
	}
	hosts := result.Steps[1].Hosts
	if len(hosts) != 1 || hosts[0].Host != "web" || hosts[0].Error != nil ||
		!reflect.DeepEqual(hosts[0].Report.Plugins.Changed, []string{"@test/notes"}) || len(hosts[0].Report.Plugins.Added) != 0 ||
		len(hosts[0].Report.Plugins.Removed) != 0 || len(hosts[0].Report.Failed) != 0 || len(hosts[0].Report.Objects) != 0 {
		t.Fatalf("web step = %+v", hosts)
	}
	after := current()
	if after.Plugins[0].Digest == before.Plugins[0].Digest {
		t.Fatalf("the plugin's digest did not change: %s", after.Plugins[0].Digest)
	}
	if dirty := s.app.Plugins.Marks().Dirty; len(dirty) != 0 {
		t.Fatalf("dirty after the rollout = %v", dirty)
	}
	for _, url := range []string{before.Plugins[0].URL, before.Combined, after.Plugins[0].URL, after.Combined} {
		if status := surfaceGet(t, s.port, url, headers).status; status != 200 {
			t.Fatalf("%s = %d, want 200", url, status)
		}
	}
}

// surfaceApp is an app with the web surface and the terminal surface (the vendored fixtures' terminal entry), a web plugin and a
// terminal plugin.
func surfaceApp(t testing.TB) started {
	t.Helper()
	tuiAPI := surfacesFixture(t, "tui", "api.ts")
	files := map[string]map[string]string{
		"notes": webPlugin,
		"lines": {"tui.ts": "import { defineFacet } from \"@test/app/v1\";\nexport default defineFacet(() => {});"},
	}
	return startTestApp(t, files, func(o *AppOptions) {
		withWeb(webEntry(t, webStarted), false)(o)
		o.Kinds = append(o.Kinds, AppKind{Name: "tui", Platform: plugins.PlatformNode, Packages: map[string]string{"@test/app/v1/tui": tuiAPI}})
		o.Tui = &AppTui{Entry: surfacesFixture(t, "tui", "main.ts"), Conditions: []string{"source"}}
	})
}

func TestAppRunsTheWebAndTheTerminalSurfacesWithTheirOwnBaseBundles(t *testing.T) {
	s := surfaceApp(t)
	if s.app.Web == nil || s.app.Tui == nil || s.app.Web.Base == "" || s.app.Tui.Base == "" || s.app.Web.Base == s.app.Tui.Base {
		t.Fatalf("surfaces: web %+v, tui %+v", s.app.Web, s.app.Tui)
	}
	if got := surfaceGet(t, s.port, "/_chord/web/base", nil); string(got.body) != `"`+s.app.Web.Base+`"` {
		t.Fatalf("web base = %s", got.body)
	}
	if got := surfaceGet(t, s.port, "/_chord/tui/base", nil); string(got.body) != `"`+s.app.Tui.Base+`"` {
		t.Fatalf("tui base = %s", got.body)
	}
	if err := s.app.Close(background); err != nil {
		t.Fatal(err)
	}
	bare := startTestApp(t, nil, nil)
	if bare.app.Web != nil || bare.app.Tui != nil {
		t.Fatalf("an app without surfaces has %+v, %+v", bare.app.Web, bare.app.Tui)
	}
	if status := surfaceGet(t, bare.port, "/_chord/web/base", nil).status; status != 404 {
		t.Fatalf("web base of an app without a web surface = %d, want 404", status)
	}
}

func TestAppAppliesChangesToTheServerThenTheWebAndTheTerminalSurfacesEachWithItsOwnPlugins(t *testing.T) {
	s := surfaceApp(t)
	if err := os.WriteFile(filepath.Join(s.root, "lines", "tui.ts"), []byte("import { defineFacet } from \"@test/app/v1\";\nexport default defineFacet(() => { void 1; });"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "notes", "notes.css"), []byte(".notes { order: 1; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	untilDirty(t, s.app, "@test/lines")
	untilDirty(t, s.app, "@test/notes")
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stopped != nil {
		t.Fatalf("rollout stopped: %+v", result.Stopped)
	}
	changed := map[string][]string{}
	var names []string
	for _, step := range result.Steps {
		names = append(names, step.Name)
		for _, each := range step.Hosts {
			if each.Error != nil {
				t.Fatalf("%s: %v", step.Name, each.Error)
			}
			changed[step.Name] = append(changed[step.Name], each.Report.Plugins.Changed...)
		}
	}
	if want := []string{"server", "web", "tui"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("steps = %v, want %v", names, want)
	}
	if !reflect.DeepEqual(changed["web"], []string{"@test/notes"}) || !reflect.DeepEqual(changed["tui"], []string{"@test/lines"}) {
		t.Fatalf("changed plugins by step = %v: the web step rolls out the web plugins, the terminal step the terminal plugins", changed)
	}
	if dirty := s.app.Plugins.Marks().Dirty; len(dirty) != 0 {
		t.Fatalf("dirty after the rollout = %v", dirty)
	}
}

func TestAppMarksBaseForAChangeOfTheWebSurfacesBaseBundleInputs(t *testing.T) {
	for file, text := range map[string]string{"style.css": ":root { --brand: blue; }\n", "main.ts": "import \"./style.css\";\nglobalThis.started = 2;\n"} {
		t.Run(file, func(t *testing.T) {
			main := webEntry(t, webStarted)
			s := startTestApp(t, nil, withWeb(main, false))
			if s.app.Plugins.Marks().Base {
				t.Fatal("base is marked at start")
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(main), file), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			untilBase(t, s.app)
		})
	}
}

func TestAppFailsToStartWhenASurfacesBaseBundleDoesNotBuildWithEverythingClosed(t *testing.T) {
	broken := webEntry(t, "export default {")
	options, _ := testOptions(t, withWeb(broken, false))
	if _, err := StartServer(options, background); err == nil || !strings.Contains(err.Error(), "main.ts") {
		t.Fatalf("StartServer = %v, want esbuild's error naming main.ts", err)
	}
	noGoroutinesOf(t, "chord/nextnode/app.", "chord/nextnode/http.", "chord/nextnode/workers.", "chord/nextnode/plugins.")
}
