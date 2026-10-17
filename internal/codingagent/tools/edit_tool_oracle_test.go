package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type editToolScenario struct {
	Files map[string]string `json:"files"`
	Args  map[string]any    `json:"args"`
}

type editToolOutcome struct {
	Out struct {
		Text    string `json:"text,omitempty"`
		Thrown  string `json:"thrown,omitempty"`
		Details *struct {
			Diff             string `json:"diff"`
			Patch            string `json:"patch"`
			FirstChangedLine *int   `json:"firstChangedLine"`
		} `json:"details,omitempty"`
	} `json:"out"`
	Files map[string]string `json:"files"`
}

func editToolScenarios() []editToolScenario {
	rng := rand.New(rand.NewPCG(26, 11))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	lines := []string{"alpha", "beta", "gamma", "  indented", "\tTabbed line", "trailing space  ", "", "const a = 1;", "// “smart” quotes — and dashes", "日本語の行", "😀 emoji line", "x", "same", "same", "func main() {", "}", "a\u00a0b nbsp", "long line " + strings.Repeat("word ", 30)}
	var scenarios []editToolScenario
	for range 1500 {
		var body []string
		for range rng.IntN(14) {
			body = append(body, pick(lines))
		}
		ending := pick([]string{"\n", "\n", "\r\n", "\r\n", "\n"})
		content := strings.Join(body, ending)
		if rng.IntN(3) > 0 && len(body) > 0 {
			content += ending
		}
		if rng.IntN(8) == 0 {
			content = "\ufeff" + content
		}
		if rng.IntN(10) == 0 && strings.Contains(content, ending) {
			content = strings.Replace(content, ending, pick([]string{"\r\n", "\n", "\r"}), 1)
		}
		pathFor := pick([]string{"a.txt", "dir/a.txt", "/vfs/a.txt", "./a.txt", "dir/../a.txt"})
		files := map[string]string{"/vfs/a.txt": content, "/vfs/dir/a.txt": "other\n"}
		if rng.IntN(12) == 0 {
			delete(files, "/vfs/a.txt")
		}
		// The model never sends the invisible BOM in oldText (the tool strips it before matching), so the targets are cut from the text without it.
		normalized := strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(content, "\ufeff"), "\r\n", "\n"), "\r", "\n")
		target := func() string {
			if len(normalized) == 0 || rng.IntN(6) == 0 {
				return pick([]string{"", "missing text", "alpha\nzzz", "same"})
			}
			a := rng.IntN(len(normalized))
			b := min(len(normalized), a+1+rng.IntN(60))
			for a > 0 && normalized[a]&0xC0 == 0x80 {
				a--
			}
			for b < len(normalized) && normalized[b]&0xC0 == 0x80 {
				b++
			}
			text := normalized[a:b]
			switch rng.IntN(7) {
			case 0:
				text = strings.NewReplacer("“", "\"", "”", "\"", "—", "-", "\u00a0", " ").Replace(text)
			case 1:
				text = strings.TrimSpace(text)
			case 2:
				text = strings.ReplaceAll(text, "\n", "\r\n")
			}
			return text
		}
		replacement := func() string {
			return pick([]string{"", "REPLACED", "two\nlines", "  indented\n", "日本語", "alpha", "“quoted”", "x"})
		}
		args := map[string]any{"path": pathFor}
		switch rng.IntN(10) {
		case 0:
			args["oldText"], args["newText"] = target(), replacement()
		case 1:
			args["edits"] = []any{}
		// A request without a path never reaches execute in the agent loop (its arguments are checked against the schema first), so none is generated.
		default:
			var edits []any
			for range 1 + rng.IntN(3) {
				edits = append(edits, map[string]any{"oldText": target(), "newText": replacement()})
			}
			args["edits"] = edits
		}
		scenarios = append(scenarios, editToolScenario{Files: files, Args: args})
	}
	return scenarios
}

func runEditToolWithPig(t *testing.T, scenario editToolScenario) editToolOutcome {
	files := map[string]string{}
	maps.Copy(files, scenario.Files)
	tool := &EditTool{CWD: "/vfs", Operations: &EditOperations{
		Access: func(path string) error {
			if _, ok := files[path]; !ok {
				return &fs.PathError{Op: "access", Path: path, Err: syscall.ENOENT}
			}
			return nil
		},
		ReadFile: func(path string) ([]byte, error) { return []byte(files[path]), nil },
		WriteFile: func(path, content string) error {
			files[path] = content
			return nil
		},
	}}
	raw, err := json.Marshal(scenario.Args)
	if err != nil {
		t.Fatal(err)
	}
	var outcome editToolOutcome
	result, err := tool.Execute(context.Background(), "call", raw, nil)
	switch {
	case err != nil:
		outcome.Out.Thrown = err.Error()
	case result.IsError:
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				outcome.Out.Thrown += text.Text
			}
		}
	default:
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				outcome.Out.Text += text.Text
			}
		}
		if details, ok := result.Details.(*EditToolDetails); ok && details != nil {
			outcome.Out.Details = &struct {
				Diff             string `json:"diff"`
				Patch            string `json:"patch"`
				FirstChangedLine *int   `json:"firstChangedLine"`
			}{Diff: details.Diff, Patch: details.Patch}
			if details.FirstChangedLine != 0 {
				line := details.FirstChangedLine
				outcome.Out.Details.FirstChangedLine = &line
			}
		}
	}
	outcome.Files = files
	return outcome
}

// The edit tool (core/tools/edit.ts) against pinned Pi over 1500 seeded edit requests on a virtual file system with injected operations: unique, ambiguous, missing, empty and overlapping targets, one to three edits, fuzzy matches (smart quotes, dashes, no-break spaces, trailing whitespace), legacy top-level oldText/newText, a missing path or file, LF, CRLF and mixed line endings, a BOM and a missing final newline; the result text or error, the diff, the unified patch, the first changed line and the file left behind must agree.
func TestEditToolMatchesPi(t *testing.T) {
	scenarios := editToolScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/edit_tool.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []editToolOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, scenario := range scenarios {
		got := runEditToolWithPig(t, scenario)
		raw, _ := json.Marshal(got)
		var normalized editToolOutcome
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 6 {
			continue
		}
		args, _ := json.Marshal(scenario.Args)
		files, _ := json.Marshal(scenario.Files)
		e, _ := json.Marshal(expected[i])
		t.Errorf("scenario %d\n  args %.600s\n  files %.400s\n  Pig %.900s\n  Pi  %.900s", i, args, files, raw, e)
	}
	if failures > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
