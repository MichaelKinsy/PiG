package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type lsNode struct {
	Dir        bool     `json:"dir"`
	Entries    []string `json:"entries"`
	Unreadable bool     `json:"unreadable,omitempty"`
}

type lsScenario struct {
	Nodes map[string]lsNode `json:"nodes"`
	Args  map[string]any    `json:"args"`
}

type lsOutcome struct {
	Text              string   `json:"text,omitempty"`
	Thrown            string   `json:"thrown,omitempty"`
	EntryLimitReached *float64 `json:"entryLimitReached"`
	Truncation        *struct {
		Truncated             bool    `json:"truncated"`
		TruncatedBy           *string `json:"truncatedBy"`
		TotalLines            int     `json:"totalLines"`
		TotalBytes            int     `json:"totalBytes"`
		OutputLines           int     `json:"outputLines"`
		OutputBytes           int     `json:"outputBytes"`
		LastLinePartial       bool    `json:"lastLinePartial"`
		FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
		MaxLines              int     `json:"maxLines"`
		MaxBytes              int     `json:"maxBytes"`
	} `json:"truncation"`
}

func lsScenarios() []lsScenario {
	rng := rand.New(rand.NewPCG(27, 13))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	stems := []string{"alpha", "Beta", "gamma", "Delta", ".hidden", ".Config", "README", "readme", "src", "Src", "a b", "a_b", "a-b", "a.b", "10", "2", "1", "zeta", "Zeta", "été", "ete", "Étoile", "日本語", "ファイル", "ß", "ss", "ǆ", "x", "X", "_under", "~tilde", "é", "e\u0301", "İstanbul", "istanbul", "Ωmega", "omega", "file.txt", "file.TXT", "node_modules"}
	var scenarios []lsScenario
	for range 1200 {
		scenario := lsScenario{Nodes: map[string]lsNode{"/vfs": {Dir: true}}, Args: map[string]any{}}
		count := []int{0, 1, 3, 8, 20, 60, 520, 650}[rng.IntN(8)]
		heavy := count > 500 && rng.IntN(2) == 0 // names long enough for the 50KB limit to hit before the entry limit
		entries := make([]string, 0, count)
		seen := map[string]bool{}
		for len(entries) < count {
			name := pick(stems)
			switch rng.IntN(4) {
			case 0:
				name += fmt.Sprint(rng.IntN(300))
			case 1:
				name += "-" + strings.Repeat("long", rng.IntN(25))
			}
			if heavy {
				name += strings.Repeat("-padding", 8+rng.IntN(8))
			}
			if seen[name] {
				name += fmt.Sprint(len(entries))
			}
			seen[name] = true
			entries = append(entries, name)
		}
		rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
		scenario.Nodes["/vfs"] = lsNode{Dir: true, Entries: entries, Unreadable: rng.IntN(25) == 0}
		for _, entry := range entries {
			switch rng.IntN(7) {
			case 0, 1:
				scenario.Nodes["/vfs/"+entry] = lsNode{Dir: true, Entries: []string{"inner"}}
			case 2:
				// An entry that cannot be statted is skipped.
			default:
				scenario.Nodes["/vfs/"+entry] = lsNode{}
			}
		}
		scenario.Nodes["/vfs/sub"] = lsNode{Dir: true, Entries: []string{"b", "A", "c"}}
		scenario.Nodes["/vfs/sub/b"] = lsNode{}
		scenario.Nodes["/vfs/sub/A"] = lsNode{Dir: true, Entries: []string{}}
		scenario.Nodes["/vfs/sub/c"] = lsNode{}
		scenario.Nodes["/vfs/locked"] = lsNode{Dir: true, Unreadable: true}
		scenario.Nodes["/vfs/file.txt"] = lsNode{}
		scenario.Nodes["/vfs/empty"] = lsNode{Dir: true, Entries: []string{}}
		if rng.IntN(8) == 0 {
			scenario.Args["path"] = pick([]string{"sub", "/vfs/sub", "./sub", "locked", "file.txt", "missing", "empty", ".", "", "sub/", "sub/../sub"})
		}
		if rng.IntN(5) == 0 {
			scenario.Args["limit"] = []any{1, 2, 5, 10, 100, 500, 800, 0, 2.5}[rng.IntN(9)]
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runLsWithPig(t *testing.T, scenario lsScenario) lsOutcome {
	tool := &LsTool{CWD: "/vfs", Operations: &LsOperations{
		Exists: func(path string) (bool, error) { _, ok := scenario.Nodes[path]; return ok, nil },
		Stat: func(path string) (LsStat, error) {
			node, ok := scenario.Nodes[path]
			if !ok {
				return nil, errors.New("ENOENT")
			}
			return lsStatOf(node.Dir), nil
		},
		Readdir: func(path string) ([]string, error) {
			node := scenario.Nodes[path]
			if node.Unreadable {
				return nil, errors.New("boom")
			}
			return append([]string{}, node.Entries...), nil
		},
	}}
	raw, err := json.Marshal(scenario.Args)
	if err != nil {
		t.Fatal(err)
	}
	var outcome lsOutcome
	result, err := tool.Execute(context.Background(), "call", raw, nil)
	switch {
	case err != nil:
		outcome.Thrown = err.Error()
	case result.IsError:
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				outcome.Thrown += text.Text
			}
		}
	default:
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				outcome.Text += text.Text
			}
		}
		if details, ok := result.Details.(*LsDetails); ok && details != nil {
			if details.EntryLimitReached != 0 {
				limit := details.EntryLimitReached
				outcome.EntryLimitReached = &limit
			}
			if truncation := details.Truncation; truncation != nil {
				outcome.Truncation = &struct {
					Truncated             bool    `json:"truncated"`
					TruncatedBy           *string `json:"truncatedBy"`
					TotalLines            int     `json:"totalLines"`
					TotalBytes            int     `json:"totalBytes"`
					OutputLines           int     `json:"outputLines"`
					OutputBytes           int     `json:"outputBytes"`
					LastLinePartial       bool    `json:"lastLinePartial"`
					FirstLineExceedsLimit bool    `json:"firstLineExceedsLimit"`
					MaxLines              int     `json:"maxLines"`
					MaxBytes              int     `json:"maxBytes"`
				}{Truncated: truncation.Truncated, TotalLines: truncation.TotalLines, TotalBytes: truncation.TotalBytes, OutputLines: truncation.OutputLines, OutputBytes: truncation.OutputBytes, LastLinePartial: truncation.LastLinePartial, FirstLineExceedsLimit: truncation.FirstLineExceedsLimit, MaxLines: truncation.MaxLines, MaxBytes: truncation.MaxBytes}
				if truncation.TruncatedBy != "" {
					by := truncation.TruncatedBy
					outcome.Truncation.TruncatedBy = &by
				}
			}
		}
	}
	return outcome
}

func metaOf(outcome lsOutcome) string {
	outcome.Text = ""
	raw, _ := json.Marshal(outcome)
	return string(raw)
}

type lsStatOf bool

func (s lsStatOf) IsDirectory() bool { return bool(s) }

// The ls tool (core/tools/ls.ts) against pinned Pi over 1200 seeded requests on a virtual file system with injected operations: ICU case-insensitive ordering of mixed-case, accented, wide and combining names, the directory suffix, entries that cannot be statted, unreadable directories, missing paths, a file path, the entry limit (including fractional and zero limits) and the 50KB limit; text, error, entry-limit and truncation metadata agree.
func TestLsToolMatchesPi(t *testing.T) {
	scenarios := lsScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/ls_tool.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []lsOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runLsWithPig(t, scenario)
		raw, _ := json.Marshal(got)
		var normalized lsOutcome
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 {
			continue
		}
		args, _ := json.Marshal(scenario.Args)
		e, _ := json.Marshal(expected[i])
		at := 0
		for at < len(normalized.Text) && at < len(expected[i].Text) && normalized.Text[at] == expected[i].Text[at] {
			at++
		}
		lo := max(0, at-120)
		t.Errorf("scenario %d args %s (%d root entries)\n  first text difference at byte %d\n  Pig ...%q\n  Pi  ...%q\n  Pig meta %.300s\n  Pi  meta %.300s", i, args, len(scenario.Nodes["/vfs"].Entries), at, normalized.Text[lo:min(len(normalized.Text), at+120)], expected[i].Text[lo:min(len(expected[i].Text), at+120)], metaOf(normalized), e[max(0, len(e)-300):])
	}
	if failures > 5 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
