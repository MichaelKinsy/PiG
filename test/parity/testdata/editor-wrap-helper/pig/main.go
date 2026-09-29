package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

type wrapCase struct {
	Name    string   `json:"name"`
	Width   int      `json:"width"`
	Padding int      `json:"padding"`
	Text    string   `json:"text"`
	Keys    []string `json:"keys"`
}

type cursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

type state struct {
	Text   string   `json:"text"`
	Cursor cursor   `json:"cursor"`
	Rows   []string `json:"rows"`
}

type result struct {
	Name   string  `json:"name"`
	States []state `json:"states"`
}

func main() {
	data, err := os.ReadFile("test/parity/testdata/editor-wrap-helper/cases.json")
	if err != nil {
		panic(err)
	}
	var cases []wrapCase
	if err := json.Unmarshal(data, &cases); err != nil {
		panic(err)
	}
	cases = append(cases,
		wrapCase{Name: "wide overflow after backtrack", Width: 188, Text: " " + strings.Repeat("a", 186) + "你"},
		wrapCase{Name: "non-CJK backtrack overflow", Width: 188, Text: " " + strings.Repeat("a", 186) + "✅"},
		wrapCase{Name: "UTF16 wrap offsets", Width: 4, Text: "A😀B😀C"},
	)
	for width := 2; width <= 42; width++ {
		tc := cases[1]
		tc.Name = fmt.Sprintf("scroll width %d", width)
		tc.Width = width
		cases = append(cases, tc)
	}
	results := make([]result, 0, len(cases))
	for _, tc := range cases {
		e := tui.NewEditor()
		e.SetMaxVisibleLines(7)
		e.SetPaddingX(tc.Padding)
		e.BorderColor = func(s string) string { return "\x1b[35m" + s + "\x1b[39m" }
		e.SetText(tc.Text)
		out := result{Name: tc.Name, States: []state{}}
		capture := func() {
			lines := e.Render(tc.Width)
			rows := make([]string, len(lines))
			for i, line := range lines {
				rows[i] = hex.EncodeToString([]byte(line))
			}
			c := e.GetCursor()
			out.States = append(out.States, state{hex.EncodeToString([]byte(e.Text())), cursor{c.Line, c.Col}, rows})
		}
		capture()
		for _, key := range tc.Keys {
			switch key {
			case "PASTE20":
				key = "\x1b[200~" + strings.TrimSuffix(strings.Repeat("line\n", 20), "\n") + "\x1b[201~"
			case "PASTE30":
				key = "\x1b[200~" + strings.TrimSuffix(strings.Repeat("line\n", 30), "\n") + "\x1b[201~"
			}
			e.HandleInput(key)
			capture()
		}
		results = append(results, out)
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
