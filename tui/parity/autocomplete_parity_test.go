package parity

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func autocompleteSampleCommands() []tui.SlashCommand {
	return []tui.SlashCommand{
		{Name: "help", Description: "Show available slash commands"},
		{Name: "clear", Description: "Clear the chat transcript"},
		{Name: "quit", Description: "Quit pig"},
		{Name: "model", Description: "Show or switch the active model"},
		{Name: "models", Description: "List available models matching a pattern"},
		{Name: "agent", Description: "Show or switch the active agent persona"},
		{Name: "agents", Description: "List available agents"},
		{Name: "tools", Description: "List registered tools"},
		{Name: "skill", Description: "List loaded skills"},
		{Name: "cost", Description: "Show session token + cost summary"},
		{Name: "save", Description: "Save the transcript to a file"},
		{Name: "copy", Description: "Copy last assistant message to clipboard"},
		{Name: "session", Description: "Show session info and stats"},
		{Name: "hotkeys", Description: "Show keyboard shortcuts"},
	}
}

func renderEditorToTUI(t *testing.T, rows, cols int, ed *tui.Editor) func(*bytes.Buffer) {
	t.Helper()
	return func(w *bytes.Buffer) {
		ti := tui.NewWithOutput(w, cols, rows)
		ti.Add(ed)
		ti.Render()
	}
}

func TestParityEditorAutocompletePopup_GoldenVisibleState(t *testing.T) {
	ed := tui.NewEditor()
	ed.SetAutocomplete(tui.NewSlashOnlyProvider(autocompleteSampleCommands()))
	ed.HandleInput("/")
	ed.HandleInput("\x1b[B")
	AssertGolden(t, "editor-autocomplete-visible", 14, 60, renderEditorToTUI(t, 14, 60, ed))
}
