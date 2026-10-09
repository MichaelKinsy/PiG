package codingagent

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/pigstrip/leakgate"
	"github.com/MichaelKinsy/PiG/tui"
)

// The strip leak gate of the interactive surfaces. It derives its cases from the generated strip ID table
// (pigstrip.Lists, pigstrip.Known), so a new ID is covered without an edit here. For every ID it records a runtime strip
// of that ID in pigstrip, the one strip state a Piglet Binary's OFF shims also record, rebuilds each surface from its
// registry and fails when the surface still names the ID (the names are leakgate's tables). cmd/pig's TestStripLeakGate
// covers the CLI, extension API, RPC and system prompt surfaces with the same tables.
//
// pig additive (D92): Stock PiG strips nothing, so every surface here is Pi's.

// leakSurface renders one user-visible surface as plain text. lists names the strip lists whose IDs the surface can show.
type leakSurface struct {
	name   string
	lists  []string
	render func(t *testing.T) string
}

// leakIncidental are lines of a surface that name an ID in another sense: "edit" in a startup hint is the verb, not the
// edit tool. /reload's description is Pi's list of what a reload rereads, which Pi keeps under --no-skills and
// --no-themes too.
var leakIncidental = map[string][]string{
	"startup hints":      {"to edit all queued messages"},
	"slash autocomplete": {"/reload Reload keybindings, extensions, skills, prompts, themes, and context files"},
}

// commandKeys are the keys that run the key actions a command owns. A stripped command's keys do nothing.
var commandKeys = map[string][]string{
	"/model":    {"\x0c", "\x10"},
	"/thinking": {"\x1b[Z"},
	"/copy":     {"\x18"},
}

func leakSurfaces() []leakSurface {
	all := pigstrip.Lists()
	return []leakSurface{
		{name: "/hotkeys", lists: []string{pigstrip.ListTools, pigstrip.ListCommands}, render: func(*testing.T) string {
			NewKeybindingsManager("")
			return hotkeysMarkdown(nil)
		}},
		{name: "startup hints", lists: all, render: func(t *testing.T) string {
			var out []string
			for _, expanded := range []bool{false, true} {
				m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
				m.keybindings = NewKeybindingsManager("")
				m.builtInHeaderExpanded = expanded
				out = append(out, m.renderBuiltInHeader(200)...)
			}
			return strings.Join(out, "\n")
		}},
		{name: "slash autocomplete", lists: all, render: func(t *testing.T) string {
			m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir()})
			suggestions := m.buildAutocompleteProvider().GetSuggestions(context.Background(), []string{"/"}, 0, 1, tui.AutocompleteSuggestionOptions{})
			var out []string
			if suggestions != nil {
				for _, item := range suggestions.Items {
					out = append(out, "/"+item.Value+" "+item.Description)
				}
			}
			return strings.Join(out, "\n")
		}},
		{name: "argument completion", lists: all, render: func(t *testing.T) string {
			m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: NewModelRegistry(t.TempDir())})
			var out []string
			for _, items := range [][]tui.AutocompleteItem{m.loginArgCompletions(""), m.modelArgCompletions(""), m.thinkingArgCompletions("")} {
				for _, item := range items {
					out = append(out, item.Value+" "+item.Label+" "+item.Description)
				}
			}
			return strings.Join(out, "\n")
		}},
		{name: "/settings", lists: all, render: func(*testing.T) string {
			var out []string
			for _, item := range NewSettingsSelectorComponent(SettingsConfig{}, SettingsCallbacks{}).GetSettingsList().Items() {
				out = append(out, item.Label+" | "+item.Description+" | "+strings.Join(item.Values, ","))
			}
			return strings.Join(out, "\n")
		}},
		{name: "/login providers", lists: all, render: func(t *testing.T) string {
			m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: t.TempDir(), ModelRegistry: NewModelRegistry(t.TempDir())})
			var out []string
			for _, provider := range m.getLoginProviderOptions(false) {
				methodName := ""
				if provider.Method != nil {
					methodName = provider.Method.AuthMethodName()
				}
				out = append(out, provider.ID+" "+provider.Name+" "+methodName)
			}
			return strings.Join(out, "\n")
		}},
		{name: "first-run setup", lists: all, render: func(*testing.T) string {
			setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
			var out []string
			for range 3 {
				out = append(out, setup.Render(120)...)
				setup.HandleInput("\r")
			}
			return strings.Join(out, "\n")
		}},
		{name: "system prompt docs section", lists: []string{pigstrip.ListExtensions, pigstrip.ListFeatures}, render: func(*testing.T) string {
			return prompts.BuildDefaultPrompt(prompts.Options{Cwd: "/work", Tools: []string{}, PigDocsPath: "/pig/docs"})
		}},
		{name: "provider login help", lists: []string{pigstrip.ListCommands, pigstrip.ListFeatures}, render: func(*testing.T) string {
			return FormatNoModelsAvailableMessage() + "\n" + FormatNoModelSelectedMessage() + "\n" + FormatNoAPIKeyFoundMessage("openai")
		}},
	}
}

// TestStripLeakGate strips every generated strip ID in turn and fails when an interactive surface still shows it, or
// when a key of a stripped command still runs its action. It fails too when a strip list maps to no surface here.
func TestStripLeakGate(t *testing.T) {
	surfaces := leakSurfaces()
	for _, list := range pigstrip.Lists() {
		if !slices.ContainsFunc(surfaces, func(s leakSurface) bool { return slices.Contains(s.lists, list) }) {
			t.Fatalf("strip list %q maps to no interactive surface", list)
		}
	}
	for _, list := range pigstrip.Lists() {
		for _, id := range pigstrip.Known(list) {
			t.Run(list+"/"+id, func(t *testing.T) {
				if pigstrip.Has(list, id) {
					t.Skipf("this build compiles %s out", id)
				}
				patterns, err := leakgate.Names(list, id)
				if err != nil {
					t.Fatal(err)
				}
				stockKeys := map[string]keyAction{}
				for _, key := range commandKeys[id] {
					stockKeys[key] = classifyKeyWithBindings(key, NewKeybindingsManager(""))
					if stockKeys[key] == actionInsert {
						t.Fatalf("stock key %q runs no action of %s", key, id)
					}
				}
				t.Cleanup(pigstrip.Strip(list, id))
				for _, surface := range surfaces {
					if !slices.Contains(surface.lists, list) {
						continue
					}
					if found := leakgate.Find(surface.render(t), leakIncidental[surface.name], patterns); len(found) > 0 {
						t.Errorf("%s still shows stripped %s %s:\n  %s", surface.name, list, id, strings.Join(found, "\n  "))
					}
				}
				for key := range stockKeys {
					if action := classifyKeyWithBindings(key, NewKeybindingsManager("")); action != actionInsert {
						t.Errorf("key %q still runs action %d of stripped %s", key, action, id)
					}
				}
			})
		}
	}
	for _, surface := range surfaces {
		if strings.TrimSpace(surface.render(t)) == "" {
			t.Fatalf("stock %s renders nothing; the gate would pass vacuously", surface.name)
		}
	}
	NewKeybindingsManager("")
}
