package tui

import (
	"context"
	"testing"
	"testing/synctest"
)

// upstream: packages/tui/src/components/editor.ts:766-802,2235-2260,2389-2405
func TestCompletionUndoRestoresPreApplyTextAndCursor(t *testing.T) {
	for _, mode := range []string{"immediate single", "owned menu", "extension provider"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				provider := &completionUpstreamProvider{query: func(prefix string, _ bool) *AutocompleteSuggestions {
					if prefix != "文" {
						return nil
					}
					if mode == "owned menu" {
						return completionUpstreamItems(prefix, "文档", "文献")
					}
					return completionUpstreamItems(prefix, "文档")
				}}
				var e *Editor
				flush := func() {}
				switch mode {
				case "immediate single":
					e = NewEditor()
					e.SetAutocomplete(provider)
					t.Cleanup(e.AutocompleteCancel)
				case "owned menu":
					e, flush = completionUpstreamEditor(t)
					e.SetAutocomplete(provider)
				case "extension provider":
					p := newAsyncEditorProbe(t, &AsyncAutocompleteProvider{
						GetSuggestions: func(_ context.Context, lines []string, row, col int, force bool) (*AutocompleteSuggestions, error) {
							return provider.query(lines[row][:col], force), nil
						},
						ApplyCompletion: func(_ context.Context, lines []string, row, col int, item AutocompleteItem, prefix string) ([]string, int, int, error) {
							out, row, col := provider.ApplyCompletion(lines, row, col, item, prefix)
							return out, row, col, nil
						},
					})
					e, flush = p.editor, p.drain
				}
				e.SetText("文 tail")
				e.HandleInput("\x01")
				e.HandleInput("\x1b[C")
				before := e.GetCursor()
				e.HandleInput("\t")
				flush()
				if mode == "owned menu" {
					e.HandleInput("\t")
					flush()
				}
				completionUpstreamText(t, e, "文档 tail")
				if got := e.GetCursor(); got.Line != 0 || got.Col != 2 {
					t.Fatalf("applied cursor=%+v, want {0 2}", got)
				}
				e.HandleInput(kittyUndo)
				completionUpstreamText(t, e, "文 tail")
				if got := e.GetCursor(); got != before || got.Col != 1 {
					t.Fatalf("undo cursor=%+v, want pre-apply %+v", got, before)
				}
			})
		})
	}
}
