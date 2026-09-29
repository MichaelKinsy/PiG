package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

func main() {
	type cursor struct {
		Line int `json:"line"`
		Col  int `json:"col"`
	}
	type state struct {
		Text     string   `json:"text"`
		Expanded string   `json:"expanded"`
		Cursor   cursor   `json:"cursor"`
		Rows     []string `json:"rows"`
	}
	type result struct {
		Name   string  `json:"name"`
		States []state `json:"states"`
	}
	paste := "\x1b[200~" + strings.TrimSuffix(strings.Repeat("line\n", 20), "\n") + "\x1b[201~"
	results := []result{}
	for _, tc := range []struct {
		name, text string
		keys       []string
	}{
		{"mixed CJK", "hello你好，world世界", []string{"\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;5C", "\x1b[1;5C", "\x1b[1;5C", "\x1b[1;5C"}},
		{"SEA Thai", "ภาษาไทยภาษาไทย", []string{"\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19", "\x01", "\x1bd"}},
		{"SEA Lao", "ສະບາຍດີ ພາສາລາວ!", []string{"\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19", "\x01", "\x1bd"}},
		{"SEA Khmer", "សួស្តីពិភពលោក ភាសាខ្មែរ", []string{"\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19", "\x01", "\x1bd"}},
		{"SEA Burmese", "မြန်မာဘာသာစကား,မင်္ဂလာပါ", []string{"\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19", "\x01", "\x1bd"}},
		{"SEA mixed marker", "ภาษาไทย", []string{paste, "ພາສາລາວ မြန်မာဘာသာစကား", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19"}},
		{"word deletion", "学生です，hello.foo 😀😀", []string{"\x17", "\x17", "\x17", "\x17", "\x17", "\x19"}},
		{"marker arrows and backspace", "A", []string{paste, "B", "\x1b[D", "\x1b[D", "\x1b[C", "\x7f"}},
		{"marker forward delete", "A", []string{paste, "B", "\x01", "\x1b[C", "\x1b[3~"}},
		{"marker word movement", "学生 ", []string{paste, " 世界", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19"}},
		{"multiple markers", "", []string{paste, " ", paste, "\x01", "\x1b[C", "\x1b[C", "\x1b[C"}},
		{"unregistered marker", "[paste #99 +5 lines]", []string{"\x01", "\x1b[C", "\x1b[1;5C", "\x1b[3~"}},
		{"marker mouse", "A", []string{paste, "B", "\x01", "click inside marker"}},
		{"marker beside unregistered text", "", []string{paste, " [paste #99 +5 lines]", "\x01", "\x1b[C", "\x1b[C", "\x1b[C"}},
		{"history backward delete", "", []string{"\x1b[A", "\x17", "\x1b[B"}},
		{"history forward delete", "", []string{"\x1b[A", "\x1bd", "\x1b[A"}},
		{"kill chain", "first second", []string{"\x17", "\x1b[1;5D", "\x1b[1;5C", "\x17", "\x19"}},
		{"sticky reset", "abcdefghij\nword\nabcdefghij", []string{"\x01", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[C", "\x1b[A", "\x1b[1;5D", "\x1b[1;5C", "\x1b[B"}},
		{"line boundaries", "one\ntwo", []string{"\x1b[1;5C", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5D", "\x1b[1;5C", "\x1b[1;5C", "\x1bd"}},
	} {
		e := tui.NewEditor()
		e.SetMaxVisibleLines(7)
		e.SetText(tc.text)
		if strings.HasPrefix(tc.name, "history ") {
			e.AddToHistory("older")
			e.AddToHistory("newer")
		}
		out := result{Name: tc.name, States: []state{}}
		capture := func() {
			lines := e.Render(80)
			rows := make([]string, 0, len(lines)-2)
			for _, line := range lines[1 : len(lines)-1] {
				rows = append(rows, hex.EncodeToString([]byte(line)))
			}
			c := e.GetCursor()
			out.States = append(out.States, state{hex.EncodeToString([]byte(e.Text())), hex.EncodeToString([]byte(e.GetExpandedText())), cursor{c.Line, c.Col}, rows})
		}
		capture()
		for _, key := range tc.keys {
			if key == "click inside marker" {
				e.HandleMouse(tui.TuiMouseEvent{Type: tui.MouseClick, Button: tui.MouseButtonLeft, X: 10, Y: 1, Width: 80})
			} else {
				e.HandleInput(key)
			}
			capture()
		}
		results = append(results, out)
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
