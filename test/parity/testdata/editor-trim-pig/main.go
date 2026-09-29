package main

import (
	"encoding/hex"
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/tui"
)

func textHex(text string) string { return hex.EncodeToString([]byte(text)) }

func main() {
	type row struct {
		Name      string   `json:"name"`
		Input     string   `json:"input"`
		History   []string `json:"history"`
		Submitted []string `json:"submitted"`
		Remaining string   `json:"remaining"`
	}
	rows := []row{}
	for _, tc := range []struct{ name, input string }{
		{"empty", ""},
		{"ordinary", " \t ordinary prompt \n"},
		{"BOM edges", "\ufeffprompt\ufeff"},
		{"BOM only", "\ufeff"},
		{"NEL edges", "\u0085prompt\u0085"},
		{"NEL only", "\u0085"},
		{"mixed edges", "\ufeff \u0085prompt\u0085 \ufeff"},
	} {
		historyEditor := tui.NewEditor()
		historyEditor.AddToHistory("older")
		historyEditor.AddToHistory(tc.input)
		historyEditor.HandleInput("\x1b[A")
		history := []string{textHex(historyEditor.Text())}
		historyEditor.HandleInput("\x1b[A")
		history = append(history, textHex(historyEditor.Text()))
		editor := tui.NewEditor()
		submitted := []string{}
		editor.OnSubmit = func(text string) { submitted = append(submitted, textHex(text)) }
		editor.SetText(tc.input)
		editor.HandleInput("\r")
		rows = append(rows, row{tc.name, textHex(tc.input), history, submitted, textHex(editor.Text())})
	}
	editor := tui.NewEditor()
	editor.AddToHistory("older")
	editor.AddToHistory("prompt")
	editor.AddToHistory("\ufeffprompt\ufeff")
	deduplicated := []string{}
	for range 3 {
		editor.HandleInput("\x1b[A")
		deduplicated = append(deduplicated, textHex(editor.Text()))
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Rows         []row    `json:"rows"`
		Deduplicated []string `json:"deduplicated"`
	}{rows, deduplicated}); err != nil {
		panic(err)
	}
}
