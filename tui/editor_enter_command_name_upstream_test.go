package tui

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// Ports packages/tui/test/editor.test.ts "Enter on a Tab path menu with a / prefix completes without submitting",
// "Enter on an argument menu with a / prefix completes without submitting" and "Enter on a Tab slash command menu submits
// the command" (Pi 9a7cfba47, #12312).
func TestUpstreamEditorEnterSubmitsOnlyFromTheSlashCommandNameMenu(t *testing.T) {
	t.Run("Tab path menu with a / prefix completes without submitting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e, flush := completionUpstreamEditor(t)
			var submitted []string
			e.OnSubmit = func(text string) { submitted = append(submitted, text) }
			e.SetAutocomplete(&completionUpstreamProvider{query: func(string, bool) *AutocompleteSuggestions {
				return completionUpstreamItems("/tmp/x/fi", "/tmp/x/file-one.md", "/tmp/x/file-two.md")
			}})
			e.SetText("look at /tmp/x/fi")
			e.HandleInput("\t")
			flush()
			completionUpstreamOpen(t, e, true)
			e.HandleInput("\r")
			if len(submitted) != 0 {
				t.Fatalf("submitted=%q, want none", submitted)
			}
			completionUpstreamText(t, e, "look at /tmp/x/file-one.md")
		})
	})
	t.Run("argument menu with a / prefix completes without submitting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e, flush := completionUpstreamEditor(t)
			var submitted []string
			e.OnSubmit = func(text string) { submitted = append(submitted, text) }
			e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "open", GetArgumentCompletions: func(prefix string) []AutocompleteItem {
				if strings.HasPrefix(prefix, "/") {
					return []AutocompleteItem{{Value: "/tmp/alpha", Label: "alpha"}}
				}
				return nil
			}}}, t.TempDir(), ""))
			completionUpstreamType(e, "/open /tm")
			flush()
			completionUpstreamOpen(t, e, true)
			e.HandleInput("\r")
			if len(submitted) != 0 {
				t.Fatalf("submitted=%q, want none", submitted)
			}
			completionUpstreamText(t, e, "/open /tmp/alpha")
		})
	})
	t.Run("Tab slash command menu submits the command", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e, flush := completionUpstreamEditor(t)
			var submitted []string
			e.OnSubmit = func(text string) { submitted = append(submitted, text) }
			e.SetAutocomplete(NewCombinedProvider([]SlashCommand{{Name: "model"}, {Name: "session"}}, t.TempDir(), ""))
			e.SetText("/mo")
			e.HandleInput("\t")
			flush()
			completionUpstreamOpen(t, e, true)
			e.HandleInput("\r")
			if want := []string{"/model"}; !slices.Equal(submitted, want) {
				t.Fatalf("submitted=%q, want %q", submitted, want)
			}
		})
	})
}
