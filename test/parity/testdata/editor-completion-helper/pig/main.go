package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

type testCase struct {
	Name   string     `json:"name"`
	Mode   string     `json:"mode"`
	Text   string     `json:"text"`
	Values []string   `json:"values"`
	Filter bool       `json:"filter"`
	Steps  [][]string `json:"steps"`
}

type provider struct{ testCase }

func (p provider) GetSuggestions(lines []string, row, col int) *tui.AutocompleteSuggestions {
	return p.query(lines[row][:col], false)
}
func (p provider) GetSuggestionsForce(lines []string, row, col int) *tui.AutocompleteSuggestions {
	return p.query(lines[row][:col], true)
}
func (p provider) query(before string, force bool) *tui.AutocompleteSuggestions {
	prefix, values, filter := before, p.Values, p.Filter
	switch p.Mode {
	case "force":
		if !force && !strings.Contains(prefix, "/") && !strings.HasPrefix(prefix, ".") {
			return nil
		}
		filter = true
	case "cursor":
		if !strings.HasPrefix(before, "/") {
			return nil
		}
		if i := strings.IndexByte(before, ' '); i >= 0 {
			prefix = before[i+1:]
			values = []string{"repo", "message", "help"}
		} else {
			values = []string{"cmd"}
		}
	case "slash":
		if !strings.HasPrefix(before, "/") {
			return nil
		}
		values = []string{"/model", "/help"}
	case "argument":
		_, arg, ok := strings.Cut(before, " ")
		if !ok || arg == "" {
			return nil
		}
		prefix = arg
	}
	items := []tui.AutocompleteItem{}
	for _, value := range values {
		if !filter || strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)) {
			items = append(items, tui.AutocompleteItem{Value: value, Label: value})
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &tui.AutocompleteSuggestions{Prefix: prefix, Items: items}
}
func (provider) ApplyCompletion(lines []string, row, col int, item tui.AutocompleteItem, prefix string) ([]string, int, int) {
	out := slices.Clone(lines)
	out[row] = lines[row][:col-len(prefix)] + item.Value + lines[row][col:]
	return out, row, col - len(prefix) + len(item.Value)
}

type cursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}
type state struct {
	Text   string   `json:"text"`
	Cursor cursor   `json:"cursor"`
	Open   bool     `json:"open"`
	Menu   []string `json:"menu"`
}
type result struct {
	Name       string  `json:"name"`
	States     []state `json:"states"`
	Invocation string  `json:"invocation,omitempty"`
}

func runCase(tc testCase) result {
	e := tui.NewEditor()
	e.SetMaxVisibleLines(7)
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	queue := make(chan func(), 32)
	finished := make(chan struct{}, 32)
	started, consumed := 0, 0
	e.SetAsyncApply(func(f func()) { queue <- f })
	e.SetAutocompleteTaskOwner(ctx, func(task func()) {
		started++
		workers.Go(func() { task(); finished <- struct{}{} })
	}, func(err error) { panic(err) })
	flush := func() {
		for consumed < started || len(queue) > 0 {
			select {
			case f := <-queue:
				f()
			case <-finished:
				consumed++
			}
		}
	}
	var host *subprocess.Host
	var tempDir string
	defer func() {
		e.AutocompleteCancel()
		cancel()
		if host != nil {
			host.Shutdown("fixture done")
		}
		flush()
		workers.Wait()
		if tempDir != "" {
			if err := os.RemoveAll(tempDir); err != nil {
				panic(err)
			}
		}
	}()
	invocation := ""
	switch tc.Mode {
	case "combined":
		e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "help", Description: "Show help"}, {Name: "model", Description: "Switch model", GetArgumentCompletions: func(string) []tui.AutocompleteItem {
			return []tui.AutocompleteItem{{Value: "claude-opus", Label: "claude-opus"}}
		}}}, ".", ""))
	case "awaited":
		e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "load-skills", Description: "Load skills", AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			invocation = prefix
			if strings.HasPrefix(prefix, "s") {
				return []tui.AutocompleteItem{{Value: "skill-a", Label: "skill-a"}}, nil
			}
			return nil, nil
		}}}, ".", ""))
	case "invalid":
		var err error
		tempDir, err = os.MkdirTemp("", "editor-invalid-")
		if err != nil {
			panic(err)
		}
		fixture, err := os.ReadFile("test/parity/testdata/editor-completion-helper/invalid-argument.mjs")
		if err != nil {
			panic(err)
		}
		entry := filepath.Join(tempDir, "commands.mjs")
		if err := os.WriteFile(entry, fixture, 0o600); err != nil {
			panic(err)
		}
		agentDir := filepath.Join(tempDir, "agent")
		if err := os.Mkdir(agentDir, 0o700); err != nil {
			panic(err)
		}
		host = subprocess.NewHost(agentDir)
		loaded, errs := host.LoadAll(ctx, []subprocess.ExtConfig{{Name: "commands", Source: entry, Enabled: true}})
		if len(errs) != 0 || len(loaded) != 1 {
			panic(fmt.Sprintf("load Node command: %v", errs))
		}
		complete := loaded[0].Commands["load-skills"].GetArgumentCompletions
		if complete == nil {
			panic("Node command did not register argument completions")
		}
		e.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "load-skills", Description: "Load skills", AwaitArgumentCompletions: func(prefix string) ([]tui.AutocompleteItem, error) {
			items, err := complete(prefix)
			if err != nil {
				return nil, err
			}
			var out []tui.AutocompleteItem
			for _, item := range items {
				out = append(out, tui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description})
			}
			return out, nil
		}}}, tempDir, ""))
	default:
		e.SetAutocomplete(provider{tc})
	}
	if tc.Text != "" {
		e.SetText(tc.Text)
	}
	out := result{Name: tc.Name, States: []state{}}
	for _, keys := range tc.Steps {
		for _, key := range keys {
			e.HandleInput(key)
		}
		flush()
		rows := e.Render(80)
		menu := []string{}
		for _, row := range rows[3:] {
			menu = append(menu, hex.EncodeToString([]byte(row)))
		}
		c := e.GetCursor()
		out.States = append(out.States, state{hex.EncodeToString([]byte(e.Text())), cursor{c.Line, c.Col}, e.AutocompleteOpen(), menu})
	}
	if tc.Mode == "invalid" {
		prefix, err := os.ReadFile(filepath.Join(tempDir, "received-prefix.txt"))
		if err != nil {
			panic(err)
		}
		invocation = string(prefix)
	}
	out.Invocation = invocation
	return out
}

func main() {
	data, err := os.ReadFile("test/parity/testdata/editor-completion-helper/cases.json")
	if err != nil {
		panic(err)
	}
	var cases []testCase
	if err = json.Unmarshal(data, &cases); err != nil {
		panic(err)
	}
	results := []result{}
	for _, tc := range cases {
		results = append(results, runCase(tc))
	}
	if err = json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
