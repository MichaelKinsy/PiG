package tui

import (
	"testing"
	"testing/synctest"
	"time"
)

type editorRequestCounter struct {
	requests int
	result   *AutocompleteSuggestions
}

func (p *editorRequestCounter) GetSuggestions([]string, int, int) *AutocompleteSuggestions {
	p.requests++
	return p.result
}
func (*editorRequestCounter) ApplyCompletion(lines []string, row, col int, _ AutocompleteItem, _ string) ([]string, int, int) {
	return lines, row, col
}

type editorTriggerProvider struct {
	*editorRequestCounter
	triggers []string
}

func (p *editorTriggerProvider) TriggerCharacters() []string { return p.triggers }

// Pi editor.ts:1232-1237 admits provider-advertised triggers only at autocomplete boundaries.
func TestEditorAdvertisedTriggerCharactersRequestAfterCJKBoundary(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		editor := NewEditor()
		defer editor.AutocompleteCancel()
		provider := &editorTriggerProvider{editorRequestCounter: &editorRequestCounter{}, triggers: []string{"$"}}
		editor.SetAutocomplete(provider)
		editor.SetText("查看，")
		editor.HandleInput("$")
		time.Sleep(20 * time.Millisecond)
		synctest.Wait()
		if provider.requests != 1 {
			t.Fatalf("requests=%d, want the advertised trigger to reach the provider", provider.requests)
		}
	})
}

// .upstream/v0.87.1/packages/tui/test/editor.test.ts:2626
func TestEditorResetsCustomTriggerCharactersWhenProviderChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		editor := NewEditor()
		defer editor.AutocompleteCancel()
		result := &AutocompleteSuggestions{Items: []AutocompleteItem{{Value: "$skill-name", Label: "skill-name"}}, Prefix: "$"}
		editor.SetAutocomplete(&editorTriggerProvider{editorRequestCounter: &editorRequestCounter{result: result}, triggers: []string{"$"}})
		provider := &editorRequestCounter{result: result}
		editor.SetAutocomplete(provider)
		editor.HandleInput("$")
		editor.HandleInput("s")
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		if provider.requests != 0 || editor.AutocompleteOpen() {
			t.Fatalf("requests=%d popup=%v", provider.requests, editor.AutocompleteOpen())
		}
	})
}

// .upstream/v0.99.2/packages/tui/test/editor.test.ts:2178
func TestEditorDoesNotAutoTriggerAfterCJKLettersOrForUnprefixedPaths(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		editor := NewEditor()
		defer editor.AutocompleteCancel()
		provider := &editorRequestCounter{}
		editor.SetAutocomplete(provider)
		for _, text := range []string{"user@example.com", "张三@example.com", "查看@src", "あ@src", "カ@src", "한@src", "ㄅ@src", "𠮷@src", "か\u3099@src", "禰\U000e0100@src", "々@src", "Ａ@src", "文档@备份", "prefix#123", "foo(@src", "问题#123", "查看，/path/", "查看，./文档/", "src/index.ts", "./文档/说明.md", "文档/说明.md", "查看src/index.ts"} {
			editor.SetText("")
			for _, char := range text {
				editor.HandleInput(string(char))
			}
			time.Sleep(20 * time.Millisecond)
			synctest.Wait()
			if provider.requests != 0 {
				t.Fatalf("%q caused %d unsolicited autocomplete requests", text, provider.requests)
			}
		}
	})
}
