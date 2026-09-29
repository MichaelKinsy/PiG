package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

type editCase struct {
	Name  string      `json:"name"`
	Width int         `json:"width"`
	Steps [][2]string `json:"steps"`
}
type cursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}
type state struct {
	Text      string   `json:"text"`
	Expanded  string   `json:"expanded"`
	Cursor    cursor   `json:"cursor"`
	Rows      []string `json:"rows"`
	Submitted []string `json:"submitted"`
}
type result struct {
	Name   string  `json:"name"`
	States []state `json:"states"`
}

func hexString(s string) string { return hex.EncodeToString([]byte(s)) }
func main() {
	data, err := os.ReadFile("test/parity/testdata/editor-edit-state/cases.json")
	if err != nil {
		panic(err)
	}
	var cases []editCase
	if err := json.Unmarshal(data, &cases); err != nil {
		panic(err)
	}
	results := make([]result, 0, len(cases))
	for _, tc := range cases {
		e := tui.NewEditor()
		e.SetMaxVisibleLines(7)
		e.BorderColor = func(s string) string { return s }
		submitted := []string{}
		e.OnSubmit = func(text string) { submitted = append(submitted, hexString(text)) }
		out := result{Name: tc.Name, States: []state{}}
		width := tc.Width
		if width == 0 {
			width = 30
		}
		capture := func() {
			c := e.GetCursor()
			rows := e.Render(width)
			for i := range rows {
				rows[i] = hexString(rows[i])
			}
			out.States = append(out.States, state{hexString(e.Text()), hexString(e.GetExpandedText()), cursor{c.Line, c.Col}, rows, slices.Clone(submitted)})
		}
		capture()
		for _, step := range tc.Steps {
			op, value := step[0], step[1]
			switch op {
			case "set":
				e.SetText(value)
			case "key":
				e.HandleInput(value)
			case "insert":
				e.InsertTextAtCursor(value)
			case "history":
				e.AddToHistory(value)
			case "paste":
				e.HandleInput("\x1b[200~" + value + "\x1b[201~")
			case "bigPaste":
				lines := make([]string, 12)
				for i := range lines {
					lines[i] = fmt.Sprintf("%s%d", value, i)
				}
				e.HandleInput("\x1b[200~" + strings.Join(lines, "\n") + "\x1b[201~")
			case "type":
				for _, ch := range value {
					e.HandleInput(string(ch))
					capture()
				}
			case "right":
				count, err := strconv.Atoi(value)
				if err != nil {
					panic(err)
				}
				for range count {
					e.HandleInput("\x1b[C")
					capture()
				}
			default:
				panic(op)
			}
			capture()
		}
		results = append(results, out)
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
