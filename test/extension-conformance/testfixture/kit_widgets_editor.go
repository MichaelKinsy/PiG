package testfixture

import (
	// The Go SDK: the ledger counts a Go fixture that imports it.
	_ "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/kit"
)

func init() {
	commands := []kit.SlashCommand{
		{Name: "help", Description: "Show help"},
		{Name: "model", Description: "Switch the model", ArgumentHint: "<name>"},
		{Name: "quit", Description: "Exit"},
		{Name: "session", Description: "Session info"},
		{Name: "settings", Description: "Open settings"},
		{Name: "share"},
		{Name: "skills", Description: "List skills"},
		{Name: "compact", Description: "Compact the context"},
	}
	editor := func(set func(*kit.Editor), focus bool) func() kit.View {
		return func() kit.View {
			node := kit.NewEditor("e")
			if set != nil {
				set(node)
			}
			view := kit.View{Root: kit.NewContainer(node)}
			if focus {
				view.Focus = "e"
			}
			return view
		}
	}
	const c = "pi-tui/Editor/"
	registerKitWidgetCase(c+"empty", editor(nil, false))
	registerKitWidgetCase(c+"empty focused", editor(nil, true))
	registerKitWidgetCase(c+"value", editor(func(n *kit.Editor) { n.Value = "hello world" }, false))
	registerKitWidgetCase(c+"multiline value focused", editor(func(n *kit.Editor) { n.Value = "one\ntwo\nthree" }, true))
	registerKitWidgetCase(c+"wrapped line", editor(func(n *kit.Editor) {
		n.Value = "The quick brown fox jumps over the lazy dog and keeps running through the field"
	}, true))
	registerKitWidgetCase(c+"wide characters", editor(func(n *kit.Editor) { n.Value = "漢字 and 😀 text" }, true))
	registerKitWidgetCase(c+"typing", editor(nil, true))
	registerKitWidgetCase(c+"newline", editor(nil, true))
	registerKitWidgetCase(c+"backspace and delete", editor(nil, true))
	registerKitWidgetCase(c+"kill word and yank", editor(nil, true))
	registerKitWidgetCase(c+"undo", editor(nil, true))
	registerKitWidgetCase(c+"submit", editor(nil, true))
	registerKitWidgetCase(c+"paste", editor(nil, true))
	registerKitWidgetCase(c+"paste large", editor(nil, true))
	registerKitWidgetCase(c+"cursor movement", editor(func(n *kit.Editor) { n.Value = "one\ntwo\nthree" }, true))
	registerKitWidgetCase(c+"submit multiline", editor(func(n *kit.Editor) { n.Value = "one\ntwo" }, true))
	registerKitWidgetCase(c+"submit disabled", editor(func(n *kit.Editor) { n.Value = "keep"; n.DisableSubmit = true }, true))
	registerKitWidgetCase(c+"padding", editor(func(n *kit.Editor) { n.Value = "padded text"; n.PaddingX = 2 }, true))
	registerKitWidgetCase(c+"padding wrapped", editor(func(n *kit.Editor) {
		n.Value = "The quick brown fox jumps over the lazy dog and keeps running"
		n.PaddingX = 3
	}, true))
	registerKitWidgetCase(c+"border color", editor(func(n *kit.Editor) { n.Value = "colored"; n.BorderColor = "accent" }, true))
	registerKitWidgetCase(c+"history", editor(func(n *kit.Editor) { n.History = []string{"first", "second", "third"} }, true))
	registerKitWidgetCase(c+"history then edit", editor(func(n *kit.Editor) { n.History = []string{"first", "second"} }, true))
	registerKitWidgetCase(c+"scrolls past the visible lines", editor(func(n *kit.Editor) {
		n.Value = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12"
	}, true))
	registerKitWidgetCase(c+"scroll indicator below", editor(func(n *kit.Editor) {
		n.Value = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12"
	}, true))
	registerKitWidgetCase(c+"more rows show more lines", editor(func(n *kit.Editor) {
		n.Value = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12"
	}, true))
	registerKitWidgetCase(c+"few rows show five lines", editor(func(n *kit.Editor) {
		n.Value = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12"
	}, true))
	registerKitWidgetCase(c+"page up and down", editor(func(n *kit.Editor) {
		n.Value = "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\nline 11\nline 12\nline 13\nline 14\nline 15\nline 16\nline 17\nline 18\nline 19\nline 20\nline 21\nline 22\nline 23\nline 24\nline 25\nline 26\nline 27\nline 28\nline 29\nline 30"
	}, true))
	registerKitWidgetCase(c+"click moves the cursor", editor(func(n *kit.Editor) { n.Value = "hello\nworld" }, true))
	registerKitWidgetCase(c+"click on the border", editor(func(n *kit.Editor) { n.Value = "hello\nworld" }, true))
	registerKitWidgetCase(c+"right click ignored", editor(func(n *kit.Editor) { n.Value = "hello" }, true))
	registerKitWidgetCase(c+"slash opens the menu", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash narrows", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash no match", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash tab accepts", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash arrow then tab", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash enter accepts", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash escape closes", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash click selects", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"text after the slash opens nothing", editor(func(n *kit.Editor) { n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"slash max visible 3", editor(func(n *kit.Editor) {
		n.AutocompleteMaxVisible = 3
		n.Autocomplete = &kit.Autocomplete{Commands: commands}
	}, true))
	registerKitWidgetCase(c+"slash with padding", editor(func(n *kit.Editor) { n.PaddingX = 2; n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
	registerKitWidgetCase(c+"autocomplete menu does not follow disabled submit", editor(func(n *kit.Editor) { n.DisableSubmit = true; n.Autocomplete = &kit.Autocomplete{Commands: commands} }, true))
}
