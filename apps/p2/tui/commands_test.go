package tui_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const (
	ctrlV = "\x16"
	ctrlK = "\x0b"
	f1    = "\x1bOP"
)

func plain(lines []string) string {
	stripped := make([]string, len(lines))
	for i, line := range lines {
		stripped[i] = widthx.StripTerminalSequences(line)
	}
	return strings.Join(stripped, "\n")
}

func commandsFacet(commands ...registries.RecordEntry[runtime.TuiCommand]) chordhost.Facet {
	return chordhost.DefineFacet(func(_ chordhost.FacetHost, self chordhost.FacetSelf) error {
		return runtime.TuiCommands.Contribute(self, commands...)
	})
}

func entry(key string, command runtime.TuiCommand) registries.RecordEntry[runtime.TuiCommand] {
	return p2runtime.Entry(key, command)
}

// chatContexts are the context terms of the commands-only chat slot, which the test controls.
type contextSet struct {
	mu    sync.Mutex
	terms map[string]bool
}

func (c *contextSet) clear() { c.mu.Lock(); c.terms = map[string]bool{}; c.mu.Unlock() }
func (c *contextSet) add(term string) {
	c.mu.Lock()
	c.terms[term] = true
	c.mu.Unlock()
}
func (c *contextSet) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for term := range c.terms {
		out = append(out, term)
	}
	return out
}

var chatContexts = &contextSet{terms: map[string]bool{}}

type chatSurface struct{}

func (chatSurface) Render(int) []string { return []string{"chat"} }
func (chatSurface) Invalidate()         {}
func (chatSurface) Dispose()            {}
func (chatSurface) Contexts() []string  { return chatContexts.list() }

var chatSlot = runtime.TuiFacet("chat", func(*runtime.TuiRuntime) runtime.TuiSurface { return chatSurface{} }, nil, nil)

func runtimeWith(t *testing.T, facets ...chordhost.Facet) *tuitest.Fixture {
	t.Helper()
	chatContexts.clear()
	return tuitest.New(t, tuitest.Options{Facets: append([]chordhost.Facet{tui.CoreFacet, chatSlot}, facets...)})
}

func hasCommand(f *tuitest.Fixture, id string) func() bool {
	return func() bool {
		return slices.ContainsFunc(f.Runtime.Commands.List(nil), func(item runtime.TuiCommandListing) bool { return item.ID == id })
	}
}

type overlayComponent struct {
	render func() []string
	input  func(string)
}

func (c overlayComponent) Render(int) []string { return c.render() }
func (overlayComponent) Invalidate()           {}
func (c overlayComponent) HandleInput(data string) {
	if c.input != nil {
		c.input(data)
	}
}

// Pi: "privileged keys: same pi default collision kept (search next), other collisions lose and are reported"
func TestPrivilegedKeysSamePiDefaultCollisionKeptSearchNextOtherCollisionsLoseAndAreReported(t *testing.T) {
	definitions := prefs.BuiltinKeybindings(prefs.DefaultKeyPlatform)
	defaults := prefs.ResolvePrivilegedKeys(definitions, pitui.KeybindingsConfig{})
	// pi resolves Ctrl+G (external editor / search next) by mode; the worker suspends interception while searching.
	if !slices.Equal(defaults.Keys["app.editor.external"], []string{"ctrl+g"}) {
		t.Fatalf("external editor = %v", defaults.Keys["app.editor.external"])
	}
	if !slices.Equal(defaults.Keys["app.clipboard.pasteImage"], []string{"ctrl+v"}) {
		t.Fatalf("paste image = %v", defaults.Keys["app.clipboard.pasteImage"])
	}
	if len(defaults.Lost) != 0 {
		t.Fatalf("lost = %v", defaults.Lost)
	}
	user := prefs.ResolvePrivilegedKeys(definitions, pitui.KeybindingsConfig{"app.model.select": {"ctrl+v"}, "plugin.thing": {"ctrl+x"}})
	if len(user.Keys["app.clipboard.pasteImage"]) != 0 || len(user.Keys["app.message.copy"]) != 0 {
		t.Fatalf("keys = %v", user.Keys)
	}
	var lost [][2]string
	for _, each := range user.Lost {
		lost = append(lost, [2]string{each.ID, each.To})
	}
	want := [][2]string{{"app.clipboard.pasteImage", "app.model.select"}, {"app.message.copy", "plugin.thing"}}
	if !slices.Equal(lost, want) {
		t.Fatalf("lost = %v, want %v", lost, want)
	}
	// A user who rebinds search-next onto the editor key keeps it for search; the launcher action loses it.
	rebound := prefs.ResolvePrivilegedKeys(definitions, pitui.KeybindingsConfig{"app.editor.external": {"ctrl+g"}, "tui.altScreen.searchNext": {"ctrl+g"}})
	if len(rebound.Keys["app.editor.external"]) != 0 {
		t.Fatalf("rebound = %v", rebound.Keys["app.editor.external"])
	}
}

// Pi: "pi keybindings.json: legacy names migrate, invalid entries are reported, the user override drives dispatch"
func TestPiKeybindingsJSONLegacyNamesMigrateInvalidEntriesAreReportedTheUserOverrideDrivesDispatch(t *testing.T) {
	directory := t.TempDir()
	agentDir := filepath.Join(directory, "agent")
	if err := os.Mkdir(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "keybindings.json"), []byte(`{"selectModel":"ctrl+k","app.model.select":"ctrl+k","bogus":"hyper+q","tui.editor.undo":["ctrl+z"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	preferences := host.LoadLocalPreferences(host.LocalPreferenceSources{AgentDir: agentDir, ConfigDir: filepath.Join(directory, "p2")})
	wantBindings := pitui.KeybindingsConfig{"app.model.select": {"ctrl+k"}, "tui.editor.undo": {"ctrl+z"}}
	if len(preferences.Keybindings) != 2 || !slices.Equal(preferences.Keybindings["app.model.select"], []string{"ctrl+k"}) || !slices.Equal(preferences.Keybindings["tui.editor.undo"], wantBindings["tui.editor.undo"]) {
		t.Fatalf("keybindings = %v", preferences.Keybindings)
	}
	if !slices.Equal(preferences.Problems, []string{"keybindings.json: invalid binding bogus"}) {
		t.Fatalf("problems = %v", preferences.Problems)
	}
	migrated, _ := prefs.SanitizeKeybindings(map[string]any{"cycleModelForward": "ctrl+o"})
	if len(migrated) != 1 || !slices.Equal(migrated["app.model.cycleForward"], []string{"ctrl+o"}) {
		t.Fatalf("migrated = %v", migrated)
	}

	selected := 0
	f := runtimeWith(t, commandsFacet(entry("app.model.select", runtime.TuiCommand{Title: "Select model", Run: func(runtime.TuiContext, string) error { selected++; return nil }})))
	f.Poll(hasCommand(f, "app.model.select"))
	f.Do(func() { f.Runtime.Appearance.Update(runtime.AppearanceUpdate{Local: &preferences}) })
	f.Do(func() {
		if keys := f.Runtime.Keys().GetKeys("app.model.select"); !slices.Equal(keys, []string{"ctrl+k"}) {
			t.Errorf("keys = %v", keys)
		}
		if pitui.GetKeybindings() != f.Runtime.Keys() {
			t.Error("the rebuilt manager is not installed for pi-tui components")
		}
	})
	f.Send("\x0c")
	if selected != 0 {
		t.Fatal("Ctrl+L no longer selects")
	}
	f.Send(ctrlK)
	if selected != 1 {
		t.Fatalf("selected = %d", selected)
	}
}

// Pi: "plugin vs plugin: specificity, user bindings, registration order and conflict reports"
func TestPluginVsPluginSpecificityUserBindingsRegistrationOrderAndConflictReports(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	command := func(id, when string, keys ...string) runtime.TuiCommand {
		if keys == nil {
			keys = []string{"ctrl+y"}
		}
		return runtime.TuiCommand{Title: id, Keys: keys, When: when, Run: func(runtime.TuiContext, string) error {
			mu.Lock()
			ran = append(ran, id)
			mu.Unlock()
			return nil
		}}
	}
	got := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(ran) }
	f := runtimeWith(t,
		commandsFacet(entry("alpha.one", command("alpha.one", "sessions")), entry("alpha.two", command("alpha.two", "chat"))),
		commandsFacet(
			entry("beta.one", command("beta.one", "sessions")),
			entry("beta.chat", command("beta.chat", "chat && busy")),
			// Plugin defaults never claim launcher keys; transcript keys are reported (the alt screen consumes them).
			entry("beta.paste", command("beta.paste", "", "ctrl+v", "ctrl+u")),
			entry("beta.top", command("beta.top", "chat", "ctrl+home")),
		))
	f.Poll(hasCommand(f, "beta.top"))
	var conflicts []string
	for _, conflict := range f.Runtime.Commands.Conflicts() {
		conflicts = append(conflicts, conflict.Message)
	}
	has := func(message string) bool { return slices.Contains(conflicts, message) }
	if !has("ctrl+y: alpha.one and beta.one; alpha.one wins (registered first)") {
		t.Fatalf("conflicts: %s", strings.Join(conflicts, "\n"))
	}
	for _, message := range conflicts {
		if strings.Contains(message, "alpha.two and beta.chat") {
			t.Fatal("more specific: no conflict")
		}
		if strings.Contains(message, "alpha.one and alpha.two") {
			t.Fatal("screens are exclusive")
		}
	}
	if !has("ctrl+v: launcher-owned; dropped from beta.paste's defaults") {
		t.Fatalf("conflicts: %s", strings.Join(conflicts, "\n"))
	}
	if !has("ctrl+home: tui.altScreen.top (transcript) wins over beta.top") {
		t.Fatalf("conflicts: %s", strings.Join(conflicts, "\n"))
	}
	if keys := f.Runtime.Ctx.Keys().GetKeys("beta.paste"); !slices.Equal(keys, []string{"ctrl+u"}) {
		t.Fatalf("keys = %v", keys)
	}

	f.Do(func() { f.Runtime.NavigateScreen("sessions") })
	f.Send("\x19")
	if !slices.Equal(got(), []string{"alpha.one"}) {
		t.Fatalf("ran = %v", got())
	}
	// A user binding wins over a default at equal specificity.
	f.Do(func() {
		local := prefs.TuiLocalPreferences{Keybindings: pitui.KeybindingsConfig{"beta.one": {"ctrl+y"}}, Settings: prefs.TuiSettings{}, Problems: []string{}}
		f.Runtime.Appearance.Update(runtime.AppearanceUpdate{Local: &local})
	})
	f.Send("\x19")
	if !slices.Equal(got(), []string{"alpha.one", "beta.one"}) {
		t.Fatalf("ran = %v", got())
	}
	f.Do(func() { f.Runtime.NavigateScreen("chat") })
	f.Send("\x19")
	if last := got()[len(got())-1]; last != "alpha.two" {
		t.Fatalf("last = %s", last)
	}
	chatContexts.add("busy")
	f.Send("\x19")
	if last := got()[len(got())-1]; last != "beta.chat" {
		t.Fatalf("the more specific context wins: %s", last)
	}
	f.Send(ctrlV)
	f.Send("\x1b[1;5H")
	if last := got()[len(got())-1]; last != "beta.chat" {
		t.Fatalf("neither the launcher key nor the transcript key reached a plugin command: %s", last)
	}
}

// Pi: "duplicate slash names disable the later slash form only; /hotkeys reports it"
func TestDuplicateSlashNamesDisableTheLaterSlashFormOnlyHotkeysReportsIt(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	record := func(text string) { mu.Lock(); ran = append(ran, text); mu.Unlock() }
	f := runtimeWith(t,
		commandsFacet(entry("alpha.go", runtime.TuiCommand{Title: "Alpha", Slash: &runtime.TuiCommandSlash{Name: "go"}, Run: func(_ runtime.TuiContext, args string) error { record("alpha:" + args); return nil }})),
		commandsFacet(
			entry("beta.go", runtime.TuiCommand{Title: "Beta", Slash: &runtime.TuiCommandSlash{Name: "go"}, Run: func(_ runtime.TuiContext, args string) error { record("beta:" + args); return nil }}),
			entry("beta.bad", runtime.TuiCommand{Title: "Bad", Slash: &runtime.TuiCommandSlash{Name: "Not Valid"}, Run: func(runtime.TuiContext, string) error { return nil }}),
		))
	f.Poll(hasCommand(f, "beta.go"))
	f.Do(func() {
		if !f.Runtime.Ctx.Commands().RunSlash("/go now") {
			t.Error("/go now did not run")
		}
		if !f.Runtime.Ctx.Commands().Run("beta.go", "") {
			t.Error("the id stays usable")
		}
		if f.Runtime.Ctx.Commands().RunSlash("/unknown") {
			t.Error("/unknown ran")
		}
	})
	mu.Lock()
	if !slices.Equal(ran, []string{"alpha:now", "beta:"}) {
		t.Fatalf("ran = %v", ran)
	}
	mu.Unlock()
	var help string
	f.Do(func() { help = plain(tui.HotkeysText(f.Runtime.Ctx)) })
	for _, pattern := range []string{`/go: alpha\.go keeps it; disabled for beta\.go`, `/Not Valid of beta\.bad is not a valid name`, `Launcher-owned`, `ctrl\+g\s+Open external editor`} {
		if !regexp.MustCompile(pattern).MatchString(help) {
			t.Fatalf("help lacks %s:\n%s", pattern, help)
		}
	}
	suggestions := f.Runtime.Commands.Autocomplete().GetSuggestions(context.Background(), []string{"/g"}, 0, 2, pitui.AutocompleteSuggestionOptions{})
	// "go" once (the disabled duplicate has no slash form) and first (fuzzy ranking; other names may also match "g").
	var values []string
	if suggestions != nil {
		for _, item := range suggestions.Items {
			values = append(values, item.Value)
		}
	}
	if len(values) == 0 || values[0] != "go" {
		t.Fatalf("values = %v", values)
	}
	count := 0
	for _, value := range values {
		if value == "go" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("go appears %d times", count)
	}
}

// Pi: "registry reload replaces commands without stale handlers and closes their overlays"
func TestRegistryReloadReplacesCommandsWithoutStaleHandlersAndClosesTheirOverlays(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	facet := func(label string) chordhost.Facet {
		return commandsFacet(entry("sample.act", runtime.TuiCommand{Title: "Act", Keys: []string{"ctrl+y"}, Run: func(runtime.TuiContext, string) error {
			mu.Lock()
			ran = append(ran, label)
			mu.Unlock()
			return nil
		}}))
	}
	f := runtimeWith(t, facet("old"))
	f.Poll(hasCommand(f, "sample.act"))
	f.Send("\x19")
	var overlay runtime.TuiOverlayHandle
	f.Do(func() {
		overlay = f.Runtime.Ctx.Overlay("sample", overlayComponent{render: func() []string { return []string{"overlay"} }}, pitui.OverlaySpec{})
	})
	if err := f.Host.ReloadFacets([]chordhost.Facet{tui.CoreFacet, chatSlot, facet("new")}); err != nil {
		t.Fatal(err)
	}
	f.Poll(func() bool { return overlay.Closed() })
	f.Send("\x19")
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(ran, []string{"old", "new"}) {
		t.Fatalf("ran = %v", ran)
	}
}

// Pi: "overlay precedence, empty-editor exit, palette and /hotkeys through the dispatcher"
func TestOverlayPrecedenceEmptyEditorExitPaletteAndHotkeysThroughTheDispatcher(t *testing.T) {
	var mu sync.Mutex
	var ran []string
	record := func(text string) func(runtime.TuiContext, string) error {
		return func(runtime.TuiContext, string) error { mu.Lock(); ran = append(ran, text); mu.Unlock(); return nil }
	}
	got := func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(ran) }
	exited := 0
	f := runtimeWith(t, commandsFacet(
		entry("x.close", runtime.TuiCommand{Title: "Close", Keys: []string{"ctrl+y"}, When: "overlay:x", Run: record("close")}),
		entry("x.global", runtime.TuiCommand{Title: "Global", Keys: []string{"ctrl+y"}, Run: record("global")}),
		entry("x.target", runtime.TuiCommand{Title: "Palette target", Run: record("palette")}),
	))
	f.Do(func() { f.Runtime.OnExit = func() { exited++ } })
	f.Poll(hasCommand(f, "x.close"))
	f.Do(func() { f.Runtime.NavigateScreen("chat") })
	chatContexts.add("editorFocused")
	f.Send("\x04")
	if exited != 0 {
		t.Fatal("Ctrl+D with text deletes forward instead")
	}
	chatContexts.add("editorEmpty")
	f.Send("\x04")
	if exited != 1 {
		t.Fatalf("exited = %d", exited)
	}

	// Stage 3: overlay-scoped commands, then the overlay; global commands do not run under an overlay.
	var input []string
	var overlay runtime.TuiOverlayHandle
	f.Do(func() {
		overlay = f.Runtime.Ctx.Overlay("x", overlayComponent{render: func() []string { return nil }, input: func(data string) { input = append(input, data) }}, pitui.OverlaySpec{})
		if !f.Runtime.Contexts()["overlay:x"] {
			t.Error("overlay:x is not an active context")
		}
	})
	f.Send("\x19")
	f.Send("a")
	f.Send("\x04")
	if !slices.Equal(got(), []string{"close"}) || !slices.Equal(input, []string{"a", "\x04"}) || exited != 1 {
		t.Fatalf("ran = %v, input = %q, exited = %d", got(), input, exited)
	}
	f.Do(func() { overlay.Close() })
	f.Send("\x19")
	if !slices.Equal(got(), []string{"close", "global"}) {
		t.Fatalf("ran = %v", got())
	}

	f.Send(f1)
	f.Do(func() {
		if !f.Runtime.Contexts()["overlay:palette"] {
			t.Error("the palette is not open")
		}
	})
	for _, char := range "palette target" {
		f.Send(string(char))
	}
	f.Send("\r")
	if !slices.Equal(got(), []string{"close", "global", "palette"}) {
		t.Fatalf("ran = %v", got())
	}
	f.Do(func() {
		if f.Runtime.Contexts()["overlay"] {
			t.Error("an overlay is still open")
		}
		if !f.Runtime.Ctx.Commands().RunSlash("/hotkeys") {
			t.Error("/hotkeys did not run")
		}
		if !f.Runtime.Contexts()["overlay:hotkeys"] {
			t.Error("the hotkeys overlay is not open")
		}
	})
	f.Send("\x1b")
	f.Do(func() {
		if f.Runtime.Contexts()["overlay"] {
			t.Error("an overlay is still open")
		}
	})
}
