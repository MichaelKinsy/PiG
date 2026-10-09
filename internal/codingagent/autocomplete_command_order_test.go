package codingagent

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's createBaseAutocompleteProvider puts extension commands before skills (interactive-mode.ts:747-776). Fuzzy filtering strips skill: and preserves insertion order for equal scores (autocomplete.ts:335-341).
func TestExtensionCommandPrecedesSameNameSkillAutocomplete(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.opts.AgentDir = t.TempDir()
	mode.opts.Skills = []*SkillDef{{Name: "btw", Description: "Side-question skill"}}
	mode.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "side-question",
		CommandOrder: []string{"btw"},
		Commands: map[string]extension.RegisteredCommand{
			"btw": {Name: "btw", Description: "Side-question command"},
		},
	}}, mode.opts.AgentDir)
	provider := mode.buildAutocompleteProvider()
	suggestions := provider.GetSuggestions(context.Background(), []string{"/btw"}, 0, len("/btw"), tui.AutocompleteSuggestionOptions{})
	if suggestions == nil || len(suggestions.Items) != 2 {
		t.Fatalf("suggestions = %#v, want the explicitly registered command and skill", suggestions)
	}
	for i, want := range []string{"btw", "skill:btw"} {
		if got := suggestions.Items[i].Value; got != want {
			t.Errorf("suggestion %d = %q, want %q", i, got, want)
		}
	}
	mode.editor.SetAutocomplete(provider)
	for _, key := range []string{"/", "b", "t", "w", "\t"} {
		if err := mode.dispatchKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if got := mode.editor.Text(); got != "/btw " {
		t.Fatalf("Tab accepted %q, want the extension command /btw", got)
	}
}

func BenchmarkAutocompleteCommandOrder(b *testing.B) {
	for _, count := range []int{0, 8, 512} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			dir := b.TempDir()
			mode := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: dir}}
			ext := extension.Extension{Commands: make(map[string]extension.RegisteredCommand)}
			for i := range count {
				name := fmt.Sprintf("side-%d", i)
				ext.CommandOrder = append(ext.CommandOrder, name)
				ext.Commands[name] = extension.RegisteredCommand{Name: name, Description: "Side-question command"}
				mode.opts.Skills = append(mode.opts.Skills, &SkillDef{Name: name, Description: "Side-question skill"})
			}
			mode.newRunner = inproc.NewRunner([]extension.Extension{ext}, dir)
			b.ReportAllocs()
			for b.Loop() {
				mode.buildAutocompleteProvider().GetSuggestions(context.Background(), []string{"/side-0"}, 0, len("/side-0"), tui.AutocompleteSuggestionOptions{})
			}
		})
	}
}
