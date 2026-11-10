package surfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Tests of packages/chord/src/node/surfaces/published.ts (PublishedScripts). Pi has no test file of its own for it: its
// cases run through surface-build.test.ts, web.test.ts and tui.test.ts, which read only the set it publishes after a
// build. These cases drive the class itself: the files of a set (a script, its stylesheet and its assets by content, the
// combined file without source maps), the last four sets staying served, a plugin whose next build fails keeping its
// script, stylesheet and assets, and the rollout step's report (added, changed and removed in Pi's order).

const publishedBase = "0123456789abcdef"

// publishedFixture is a published-scripts object of the web kind over a loaded server (the step publishes to it).
type publishedFixture struct {
	t         *testing.T
	published *PublishedScripts
	server    host.Host
}

func newPublishedFixture(t *testing.T) *publishedFixture {
	t.Helper()
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		t.Fatal(err)
	}
	address := kind.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	created := plugins.CreatePlugins(plugins.PluginsOptions{
		Build: build.BuildFacet, Kinds: []plugins.KindEntry{{Name: "web", Definition: plugins.KindDefinition{Platform: model.PlatformBrowser}}},
	}, chordctx.Background())
	published := NewPublishedScripts(Web, "web", WebPrefix, publishedBase, created, server)
	report, err := server.Load([]host.HostFacet{{Name: "chord.web", Facet: ScriptsFacet(Web, published.Set)}}, chordctx.Background())
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("load = %+v, %v", report.Failed, err)
	}
	t.Cleanup(func() {
		_ = server.Close(chordctx.Background())
		created.Close()
	})
	return &publishedFixture{t: t, published: published, server: server}
}

// shown is the set the server's scripts object shows.
func (f *publishedFixture) shown() next.ScriptSet {
	f.t.Helper()
	scripts, err := f.server.Get(chordctx.Background(), next.WebScripts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	defer scripts.Release()
	value, err := scripts.Member("set").Value()
	if err != nil {
		f.t.Fatal(err)
	}
	return next.ScriptSetOf(value)
}

// built is a plugin whose build made a script.
func built(name, digest, source string) plugins.PluginScript {
	return plugins.PluginScript{
		Plugin: model.Plugin{Name: name},
		Built:  model.BuiltFacet{Kind: model.BuiltFacetScript, Plugin: name, Facet: "web", Source: source, Digest: digest},
	}
}

func withCSS(script plugins.PluginScript, text string) plugins.PluginScript {
	script.Built.CSS = &model.CSS{Name: "style.css", Text: text}
	return script
}

func withAssets(script plugins.PluginScript, assets ...model.Asset) plugins.PluginScript {
	script.Built.Assets = assets
	return script
}

// failedBuild is a plugin whose build failed.
func failedBuild(name string, errs ...string) plugins.PluginScript {
	return plugins.PluginScript{
		Plugin: model.Plugin{Name: name},
		Built:  model.BuiltFacet{Kind: model.BuiltFacetFailed, Plugin: name, Facet: "web", Errors: errs},
	}
}

// sixteen is the first 16 hex digits of the SHA-256 of text, written out here rather than taken from hashOf.
func sixteen(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}

func mustFile(t *testing.T, published *PublishedScripts, path string) ServedFile {
	t.Helper()
	file := published.File(path)
	if file == nil {
		t.Fatalf("file %q is not served", path)
	}
	return *file
}

const (
	mapComment = "\n//# sourceMappingURL=data:application/json;base64,eyJ2ZXJzaW9uIjozfQ=="
)

func TestPublishedScriptsFilesNameEachScriptStylesheetAndAssetByContentAndCombineWithoutSourceMaps(t *testing.T) {
	f := newPublishedFixture(t)
	scoped := withAssets(withCSS(built("@scope/name", "d-scoped", "function(){return 1}"+mapComment), ".a{color:red}"),
		model.Asset{Name: "logo.abc123.svg", Bytes: []byte("<svg/>")}, model.Asset{Name: "font.def456.woff2", Bytes: []byte{0, 1, 2}})
	plain := built("alpha", "d-alpha", "function(){return 2}")
	// Pi sorts the plugins by name (`<` over UTF-16): `@` (0x40) before `a` (0x61), whatever order the build lists them in.
	failed, err := f.published.publish([]plugins.PluginScript{plain, scoped})
	if err != nil || len(failed) != 0 {
		t.Fatalf("publish = %v, %v", failed, err)
	}
	set := f.published.Set()
	scopedText := next.RegisteredSource("@scope/name", "d-scoped", "function(){return 1}"+mapComment)
	plainText := next.RegisteredSource("alpha", "d-alpha", "function(){return 2}")
	scopedStem := WebPrefix + "plugin/@scope~name.d-scoped"
	style := scopedStem + "." + sixteen(".a{color:red}") + ".css"
	body := strings.Replace(scopedText, mapComment, "", 1) + plainText
	combined := WebPrefix + "plugins." + sixteen(body) + ".js"
	if set.Base != publishedBase || set.Combined != combined {
		t.Fatalf("set base %q combined %q, want %q and %q", set.Base, set.Combined, publishedBase, combined)
	}
	want := []next.ScriptEntry{
		{Name: "@scope/name", Digest: "d-scoped", URL: scopedStem + ".js", CSS: &style},
		{Name: "alpha", Digest: "d-alpha", URL: WebPrefix + "plugin/alpha.d-alpha.js"},
	}
	if len(set.Plugins) != 2 {
		t.Fatalf("plugins = %+v", set.Plugins)
	}
	for i, entry := range want {
		got := set.Plugins[i]
		if got.Name != entry.Name || got.Digest != entry.Digest || got.URL != entry.URL || (got.CSS == nil) != (entry.CSS == nil) || got.CSS != nil && *got.CSS != *entry.CSS {
			t.Errorf("entry %d = %+v, want %+v", i, got, entry)
		}
	}
	if !strings.Contains(scopedText, mapComment) {
		t.Fatal("fixture: the registered text lost its source map comment")
	}
	// Each script keeps its source map (its own file is read by one browser tool at a time); the combined file has none.
	if file := mustFile(t, f.published, scopedStem+".js"); file.Mime != JavaScript || string(file.Bytes) != scopedText {
		t.Errorf("script file = %q %q, want %q", file.Mime, file.Bytes, scopedText)
	}
	if file := mustFile(t, f.published, combined); file.Mime != JavaScript || string(file.Bytes) != body || strings.Contains(string(file.Bytes), "sourceMappingURL") {
		t.Errorf("combined file = %q %q, want %q", file.Mime, file.Bytes, body)
	}
	if file := mustFile(t, f.published, style); file.Mime != CSS || string(file.Bytes) != ".a{color:red}" {
		t.Errorf("stylesheet = %q %q", file.Mime, file.Bytes)
	}
	if file := mustFile(t, f.published, WebPrefix+"assets/logo.abc123.svg"); file.Mime != "image/svg+xml" || string(file.Bytes) != "<svg/>" {
		t.Errorf("svg asset = %q %q", file.Mime, file.Bytes)
	}
	if file := mustFile(t, f.published, WebPrefix+"assets/font.def456.woff2"); file.Mime != "font/woff2" || !slices.Equal(file.Bytes, []byte{0, 1, 2}) {
		t.Errorf("font asset = %q %v", file.Mime, file.Bytes)
	}
	if file := f.published.File(WebPrefix + "plugin/alpha.d-alpha.css"); file != nil {
		t.Errorf("a plugin without a stylesheet serves one: %+v", file)
	}
	if file := f.published.File(WebPrefix + "plugin/nothing.js"); file != nil {
		t.Errorf("an unknown path is served: %+v", file)
	}
}

func TestPublishedScriptsAStylesheetChangeAloneKeepsTheScriptsDigestAndChangesItsName(t *testing.T) {
	f := newPublishedFixture(t)
	if _, err := f.published.publish([]plugins.PluginScript{withCSS(built("a", "d1", "function(){}"), ".one{}")}); err != nil {
		t.Fatal(err)
	}
	first := f.published.Set()
	if _, err := f.published.publish([]plugins.PluginScript{withCSS(built("a", "d1", "function(){}"), ".two{}")}); err != nil {
		t.Fatal(err)
	}
	second := f.published.Set()
	if first.Plugins[0].URL != second.Plugins[0].URL || *first.Plugins[0].CSS == *second.Plugins[0].CSS {
		t.Fatalf("css-only change: script %q then %q, style %q then %q", first.Plugins[0].URL, second.Plugins[0].URL, *first.Plugins[0].CSS, *second.Plugins[0].CSS)
	}
	// both stylesheets are served: a client still on the first set loads its own
	if string(mustFile(t, f.published, *first.Plugins[0].CSS).Bytes) != ".one{}" || string(mustFile(t, f.published, *second.Plugins[0].CSS).Bytes) != ".two{}" {
		t.Fatal("a stylesheet of the last sets is not served")
	}
}

func TestPublishedScriptsTheFilesOfTheLastFourSetsStayServed(t *testing.T) {
	f := newPublishedFixture(t)
	var urls, combined []string
	for _, digest := range []string{"d1", "d2", "d3", "d4", "d5"} {
		if _, err := f.published.publish([]plugins.PluginScript{built("a", digest, "function(){return '"+digest+"'}")}); err != nil {
			t.Fatal(err)
		}
		set := f.published.Set()
		urls = append(urls, set.Plugins[0].URL)
		combined = append(combined, set.Combined)
	}
	if keptSets != 4 {
		t.Fatalf("keptSets = %d, Pi's KEPT_SETS is 4", keptSets)
	}
	for i := range urls {
		served := f.published.File(urls[i]) != nil
		served2 := f.published.File(combined[i]) != nil
		if want := i >= 1; served != want || served2 != want {
			t.Errorf("set %d: script served %v, combined served %v, want both %v (the current set and the three before it)", i+1, served, served2, want)
		}
	}
	// the current set is the last published
	if got := f.published.Set(); got.Plugins[0].Digest != "d5" || got.Combined != combined[4] {
		t.Fatalf("set = %+v", got)
	}
}

func TestPublishedScriptsAFilePublishedInAnEarlierSetStaysServedWhileALaterSetStillHasIt(t *testing.T) {
	f := newPublishedFixture(t)
	steady := built("steady", "s1", "function(){}")
	var steadyURL string
	for i, digest := range []string{"d1", "d2", "d3", "d4", "d5", "d6"} {
		if _, err := f.published.publish([]plugins.PluginScript{steady, built("moving", digest, "function(){}")}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			steadyURL = f.published.Set().Plugins[1].URL
		}
	}
	if f.published.File(steadyURL) == nil {
		t.Fatal("a plugin whose script every set has lost its file")
	}
	if f.published.File(WebPrefix+"plugin/moving.d1.js") != nil || f.published.File(WebPrefix+"plugin/moving.d2.js") != nil {
		t.Fatal("a script only the sets before the last four had is still served")
	}
	if f.published.File(WebPrefix+"plugin/moving.d3.js") == nil {
		t.Fatal("a script of the fourth set from the current one is not served")
	}
}

func TestPublishedScriptsAPluginWhoseNextBuildFailsKeepsItsDigestTextStylesheetAndAssets(t *testing.T) {
	f := newPublishedFixture(t)
	ok := withAssets(withCSS(built("a", "d1", "function(){return 'a'}"+mapComment), ".a{}"), model.Asset{Name: "pic.1.png", Bytes: []byte("png")})
	if _, err := f.published.publish([]plugins.PluginScript{ok}); err != nil {
		t.Fatal(err)
	}
	before := f.published.Set()
	failed, err := f.published.publish([]plugins.PluginScript{failedBuild("a", "a.ts:1: bad", "a.ts:2: worse"), failedBuild("never", "never.ts: bad"), built("b", "e1", "function(){}")})
	if err != nil {
		t.Fatal(err)
	}
	// the failures in the order of the scripts, each with its errors one per line; "never" had no script to keep
	if len(failed) != 2 || failed[0].Plugin != "a" || failed[0].Error.Error() != "a.ts:1: bad\na.ts:2: worse" || failed[1].Plugin != "never" || failed[1].Error.Error() != "never.ts: bad" {
		t.Fatalf("failed = %+v", failed)
	}
	var failure *buildFailure
	if !errors.As(failed[0].Error, &failure) {
		t.Fatalf("failure is %T, want the build's failure", failed[0].Error)
	}
	set := f.published.Set()
	if len(set.Plugins) != 2 || set.Plugins[0].Name != "a" || set.Plugins[1].Name != "b" {
		t.Fatalf("plugins = %+v, want a (kept) and b (new); never has none", set.Plugins)
	}
	kept := set.Plugins[0]
	if kept.Digest != "d1" || kept.URL != before.Plugins[0].URL || kept.CSS == nil || *kept.CSS != *before.Plugins[0].CSS {
		t.Fatalf("kept entry = %+v, before = %+v", kept, before.Plugins[0])
	}
	if got := string(mustFile(t, f.published, kept.URL).Bytes); got != next.RegisteredSource("a", "d1", "function(){return 'a'}"+mapComment) {
		t.Fatalf("kept text = %q", got)
	}
	if string(mustFile(t, f.published, *kept.CSS).Bytes) != ".a{}" || string(mustFile(t, f.published, WebPrefix+"assets/pic.1.png").Bytes) != "png" {
		t.Fatal("a plugin whose build failed lost its stylesheet or assets")
	}
	// a plugin the next build no longer lists (not failed: gone) is dropped, with its files from the current set
	if _, err := f.published.publish([]plugins.PluginScript{built("b", "e1", "function(){}")}); err != nil {
		t.Fatal(err)
	}
	if got := f.published.Set(); len(got.Plugins) != 1 || got.Plugins[0].Name != "b" {
		t.Fatalf("plugins after a removal = %+v", got.Plugins)
	}
}

func TestPublishedScriptsTheRolloutStepReportsAddedChangedAndRemovedInPisOrderAndPublishesTheSet(t *testing.T) {
	f := newPublishedFixture(t)
	// U+1F600 is the surrogate pair D83D DE00 and U+FF5E one unit FF5E: by UTF-16 code unit the emoji sorts first, by code point
	// (UTF-8 bytes) after. Pi's `<` compares code units.
	const emoji, tilde = "\U0001F600", "\uFF5E"
	if _, err := f.published.publish([]plugins.PluginScript{
		built("keep", "k1", "function(){}"), built("change", "c1", "function(){}"), built("gone-b", "g1", "function(){}"),
		built(emoji, "e1", "function(){}"), built(tilde, "t1", "function(){}"),
	}); err != nil {
		t.Fatal(err)
	}
	step := f.published.Step("web")
	if step.Name != "web" || !slices.Equal(step.Kinds, []string{"web"}) {
		t.Fatalf("step = %q %v, want one \"web\" host of the web kind", step.Name, step.Kinds)
	}
	applied := map[string][]plugins.PluginScript{"web": {
		built("keep", "k1", "function(){}"), built("change", "c2", "function(){}"), failedBuild("broken", "broken.ts: bad"),
		built("added-b", "a1", "function(){}"), built("added-a", "a2", "function(){}"),
	}}
	result, err := step.Reload(nil, applied, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hosts) != 1 {
		t.Fatalf("hosts = %+v, want one", result.Hosts)
	}
	got := result.Hosts[0]
	if got.Kind != plugins.RolloutReported || got.Host != "web" || got.Error != nil {
		t.Fatalf("host = %+v", got)
	}
	// added in the set's name order, changed those whose digest moved, removed in the order of the set they leave
	if !slices.Equal(got.Report.Plugins.Added, []string{"added-a", "added-b"}) || !slices.Equal(got.Report.Plugins.Changed, []string{"change"}) {
		t.Errorf("added %v changed %v", got.Report.Plugins.Added, got.Report.Plugins.Changed)
	}
	if want := []string{"gone-b", emoji, tilde}; !slices.Equal(got.Report.Plugins.Removed, want) {
		t.Errorf("removed %q, want %q (UTF-16 order, as the set Pi leaves)", got.Report.Plugins.Removed, want)
	}
	if len(got.Report.Failed) != 1 || got.Report.Failed[0].Plugin != "broken" || got.Report.Failed[0].Error.Error() != "broken.ts: bad" {
		t.Errorf("failed = %+v", got.Report.Failed)
	}
	if len(got.Report.Objects) != 0 {
		t.Errorf("objects = %+v", got.Report.Objects)
	}
	// the step published the set to the server's scripts object, and its files are served
	shown, current := f.shown(), f.published.Set()
	if shown.Base != publishedBase || shown.Combined != current.Combined || len(shown.Plugins) != len(current.Plugins) || len(current.Plugins) != 4 {
		t.Fatalf("shown = %+v, set = %+v", shown, current)
	}
	for _, entry := range current.Plugins {
		if f.published.File(entry.URL) == nil {
			t.Errorf("%s: no file for %q", entry.Name, entry.URL)
		}
	}
	// a second step with the same scripts reports nothing
	again, err := step.Reload(nil, applied, chordctx.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report := again.Hosts[0].Report; len(report.Plugins.Added)+len(report.Plugins.Changed)+len(report.Plugins.Removed) != 0 {
		t.Errorf("a repeated step reports %+v", report.Plugins)
	}
}

func TestPublishedScriptsStartPublishesTheKindsFirstSet(t *testing.T) {
	f := newPublishedFixture(t)
	failed, err := f.published.Start(chordctx.Background())
	if err != nil || len(failed) != 0 {
		t.Fatalf("start = %v, %v", failed, err)
	}
	set := f.published.Set()
	body := ""
	if set.Base != publishedBase || len(set.Plugins) != 0 || set.Combined != WebPrefix+"plugins."+sixteen(body)+".js" {
		t.Fatalf("first set = %+v, want the empty combined file", set)
	}
	if file := mustFile(t, f.published, set.Combined); len(file.Bytes) != 0 || file.Mime != JavaScript {
		t.Fatalf("combined file = %+v", file)
	}
}
