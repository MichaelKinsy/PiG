package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type grepOutcome struct {
	Text              string   `json:"text,omitempty"`
	Thrown            string   `json:"thrown,omitempty"`
	MatchLimitReached *float64 `json:"matchLimitReached"`
	LinesTruncated    bool     `json:"linesTruncated"`
	Truncated         *struct {
		By          *string `json:"by"`
		TotalLines  int     `json:"totalLines"`
		TotalBytes  int     `json:"totalBytes"`
		OutputLines int     `json:"outputLines"`
		OutputBytes int     `json:"outputBytes"`
		MaxLines    int     `json:"maxLines"`
		MaxBytes    int     `json:"maxBytes"`
	} `json:"truncated"`
}

func buildGrepTree(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("repo/.git/HEAD", "ref: refs/heads/main\n")
	write("repo/.gitignore", "ignored/\n*.log\n")
	write("repo/src/main.ts", "import { helper } from './util';\n\nexport function main() {\n  const value = helper(42);\n  console.log('Hello, World', value);\n  // TODO: handle errors\n  return value;\n}\n")
	write("repo/src/util.ts", "export function helper(n: number) {\n  return n * 2;\n}\n// TODO: document helper\nexport const HELPER_NAME = 'helper';\n")
	write("repo/src/deep/thing.spec.ts", "describe('thing', () => {\n  it('works', () => {\n    expect(helper(1)).toBe(2);\n  });\n});\n")
	write("repo/README.md", "# Project\n\nHello world.\nThis project says hello to the World.\nTODO list below:\n- one\n- two\n")
	write("repo/notes.txt", "alpha\r\nbeta\r\nhello again\r\ngamma\r\n")
	write("repo/ignored/skip.ts", "TODO ignored file with hello\n")
	write("repo/debug.log", "hello from the log\n")
	write("repo/.hidden/secret.ts", "const hello = 'hidden';\n")
	write("repo/日本語.md", "こんにちは hello 世界\n日本語の行\n")
	write("repo/long.txt", "short hello line\n"+strings.Repeat("x", 700)+" hello "+strings.Repeat("y", 700)+"\nanother hello\n")
	write("repo/binary.bin", "hello\x00binary\x00data\n")
	write("repo/special.txt", "a.b a*b a+b (paren) [bracket] {brace} ^caret$ back\\slash tab\there\n")
	write("repo/empty.txt", "")
	write("repo/no-newline.txt", "last line hello")
	write("repo/Mixed.TXT", "Hello HELLO hello hElLo\n")
	for i := range 150 {
		write(fmt.Sprintf("many/file%03d.txt", i), fmt.Sprintf("line one\nmatch number %d here\nline three hello\n", i))
	}
	write("many/big.txt", strings.Repeat("hello world this is a repeated line of moderately long text for truncation\n", 1500))
	return root
}

func grepScenarios() []map[string]any {
	rng := rand.New(rand.NewPCG(29, 17))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	patterns := []string{"hello", "Hello", "TODO", "helper", "HELPER", "function", "export function", "^export", "world$", "h.llo", "hel+o", "[Hh]ello", "a\\.b", "a.b", "a*b", "(paren)", "[bracket]", "{brace}", "^caret$", "\\\\", "\\t", "x{100}", "こんにちは", "世界", "match number \\d+", "\\bhello\\b", "(?i)hello", "(", "[", "*", "", " ", "nothing-matches-this", "line (one|three)", "$", "^$", "\\d{3}"}
	globs := []string{"", "", "", "*.ts", "**/*.ts", "*.md", "*.txt", "src/**", "**/*.spec.ts", "*.{ts,md}", "!*.ts", "*.log", "*.bin", "nothing"}
	paths := []string{"repo", "repo/src", "repo/src/main.ts", "repo/README.md", "many", "repo/ignored", "missing", "repo/binary.bin", "repo/long.txt", ".", "", "repo/日本語.md", "repo/.hidden"}
	var scenarios []map[string]any
	for range 800 {
		scenario := map[string]any{"pattern": pick(patterns)}
		if rng.IntN(4) > 0 {
			scenario["path"] = pick(paths)
		}
		if g := pick(globs); g != "" {
			scenario["glob"] = g
		}
		if rng.IntN(4) == 0 {
			scenario["ignoreCase"] = rng.IntN(2) == 0
		}
		if rng.IntN(4) == 0 {
			scenario["literal"] = rng.IntN(2) == 0
		}
		if rng.IntN(3) == 0 {
			scenario["context"] = []any{0, 1, 2, 3, 10, 2.5, -1}[rng.IntN(7)]
		}
		if rng.IntN(3) == 0 {
			scenario["limit"] = []any{1, 2, 3, 5, 10, 100, 1000, 2.5, 0}[rng.IntN(9)]
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// The grep tool (core/tools/grep.ts, over ripgrep) against pinned Pi on a real tree (a git repository with .gitignore, hidden and binary files, CRLF, wide text, a file with 700-character lines, a file of 1500 long lines, 150 small files): 800 seeded requests with literal and regular-expression patterns (valid and invalid), glob filters (including negations), case and literal flags, context lines (fractional and negative too), a file, a directory or a missing path, and limits from zero to 1000. Output ordering across files is not specified (ripgrep searches in parallel), so the result lines are compared as a sorted set, and a request that reaches its match limit by its notice; error text, notices, truncation metadata and the truncated-line flag agree.
func TestGrepToolMatchesPi(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Fatalf("rg is required: %v", err)
	}
	tree := buildGrepTree(t)
	scenarios := grepScenarios()
	input, err := json.Marshal(map[string]any{"tree": tree, "scenarios": scenarios})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/grep_tool.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []grepOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	tool := &GrepTool{CWD: tree}
	failures := 0
	for i, scenario := range scenarios {
		raw, _ := json.Marshal(scenario)
		var got grepOutcome
		result, err := tool.Execute(context.Background(), "call", raw, nil)
		switch {
		case err != nil:
			got.Thrown = err.Error()
		case result.IsError:
			for _, block := range result.Content {
				if text, ok := block.(ai.TextContent); ok {
					got.Thrown += text.Text
				}
			}
		default:
			for _, block := range result.Content {
				if text, ok := block.(ai.TextContent); ok {
					got.Text += text.Text
				}
			}
			if details, ok := result.Details.(*GrepDetails); ok && details != nil {
				if details.MatchLimitReached != 0 {
					limit := details.MatchLimitReached
					got.MatchLimitReached = &limit
				}
				got.LinesTruncated = details.LinesTruncated
				if tr := details.Truncation; tr != nil {
					got.Truncated = &struct {
						By          *string `json:"by"`
						TotalLines  int     `json:"totalLines"`
						TotalBytes  int     `json:"totalBytes"`
						OutputLines int     `json:"outputLines"`
						OutputBytes int     `json:"outputBytes"`
						MaxLines    int     `json:"maxLines"`
						MaxBytes    int     `json:"maxBytes"`
					}{TotalLines: tr.TotalLines, TotalBytes: tr.TotalBytes, OutputLines: tr.OutputLines, OutputBytes: tr.OutputBytes, MaxLines: tr.MaxLines, MaxBytes: tr.MaxBytes}
					if tr.TruncatedBy != "" {
						by := tr.TruncatedBy
						got.Truncated.By = &by
					}
				}
			}
		}
		gotRaw, _ := json.Marshal(got)
		var normalized grepOutcome
		_ = json.Unmarshal(gotRaw, &normalized)
		normalized0 := normalized
		want := expected[i]
		keep := func(outcome grepOutcome, byCount bool) grepOutcome {
			body, notice, hasNotice := strings.Cut(outcome.Text, "\n\n[")
			lines := strings.Split(body, "\n")
			if byCount {
				// The subset ripgrep keeps at a match limit differs from run to run, and with it the context lines, whether a long line is among them, and the byte counts.
				outcome.Text = ""
				outcome.LinesTruncated = false
				notice, _, _ = strings.Cut(notice, ". Some lines truncated")
				outcome.Text = "[" + strings.TrimSuffix(notice, "]") + "]"
				outcome.Truncated = nil
				return outcome
			}
			slices.Sort(lines)
			outcome.Text = strings.Join(lines, "\n")
			if hasNotice {
				outcome.Text += "\n\n[" + notice
			}
			return outcome
		}
		limited := want.MatchLimitReached != nil && normalized.MatchLimitReached != nil
		if want.Thrown == "" && normalized.Thrown == "" {
			normalized, want = keep(normalized, limited), keep(want, limited)
		}
		if reflect.DeepEqual(normalized, want) {
			continue
		}
		if failures++; failures > 6 {
			continue
		}
		e, _ := json.Marshal(expected[i])
		describe := func(outcome grepOutcome) string {
			body, notice, _ := strings.Cut(outcome.Text, "\n\n[")
			meta := outcome
			meta.Text = ""
			metaJSON, _ := json.Marshal(meta)
			return fmt.Sprintf("%d lines, notice [%s, meta %s, first %.80q", strings.Count(body, "\n")+1, notice, metaJSON, body)
		}
		t.Errorf("scenario %d %s\n  Pig %s\n  Pi  %s\n  (raw Pi %.200s)", i, raw, describe(normalized0), describe(expected[i]), e)
	}
	if failures > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
