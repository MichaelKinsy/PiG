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

type findOutcome struct {
	Text               string   `json:"text,omitempty"`
	Thrown             string   `json:"thrown,omitempty"`
	ResultLimitReached *float64 `json:"resultLimitReached"`
	Truncated          *struct {
		By          *string `json:"by"`
		TotalLines  int     `json:"totalLines"`
		TotalBytes  int     `json:"totalBytes"`
		OutputLines int     `json:"outputLines"`
		OutputBytes int     `json:"outputBytes"`
		MaxLines    int     `json:"maxLines"`
		MaxBytes    int     `json:"maxBytes"`
	} `json:"truncated"`
}

func buildFindTree(t *testing.T) string {
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
	// A git repository (an empty .git directory is enough for the repository check), a plain project and nested repositories.
	write("repo/.git/HEAD", "ref: refs/heads/main\n")
	write("repo/.gitignore", "ignored/\n*.log\nbuild\n")
	for _, name := range []string{"src/main.ts", "src/util.ts", "src/deep/nested/thing.spec.ts", "src/deep/other.spec.ts", "src/a.test.js", "ignored/skip.ts", "debug.log", "build/out.js", "README.md", ".hidden/secret.ts", ".env", "docs/guide.md", "docs/日本語.md", "docs/Spaced Name.md", "node_modules/pkg/index.js", "package.json", "src/Component.TSX", "src/data.json", "nested/.git/HEAD", "nested/.gitignore", "nested/keep.ts", "nested/ignored-by-nested.ts"} {
		write(filepath.Join("repo", name), "x\n")
	}
	write("repo/nested/.gitignore", "ignored-by-nested.ts\n")
	write("plain/.gitignore", "*.tmp\n")
	for _, name := range []string{"a.ts", "b.tmp", "sub/c.ts", "sub/d.tmp", ".dot/e.ts"} {
		write(filepath.Join("plain", name), "x\n")
	}
	for i := range 1300 {
		write(fmt.Sprintf("many/dir%d/file-%04d.txt", i%13, i), "x\n")
	}
	for i := range 40 {
		write(fmt.Sprintf("long/%s%d.dat", strings.Repeat("segment-", 24), i), "x\n")
	}
	return root
}

func findScenarios() []map[string]any {
	rng := rand.New(rand.NewPCG(28, 5))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	patterns := []string{"*.ts", "**/*.ts", "*.json", "**/*.json", "src/**/*.spec.ts", "src/*.ts", "*.md", "docs/*.md", "**", "*", "?ain.ts", "main.*", "*.{ts,js}", "[ab].ts", "*.TSX", "*.tsx", "**/node_modules/**", "build/*", "*.log", "*.tmp", ".*", "**/.hidden/*", "/abs/nothing", "nothing-matches", "日本語.md", "*Spaced*", "keep.ts", "file-00*.txt", "*.txt", "**/*.dat", "dir3/*", "many/dir1/*.txt", "segment*", "*.ts ", "", "a/b/c/d/e/*.ts"}
	paths := []string{"repo", "repo/src", "repo/docs", "plain", "plain/sub", "many", "long", "repo/nested", ".", "missing", "repo/README.md", "repo/.hidden", "repo/../plain", "", "repo/src/"}
	var scenarios []map[string]any
	for range 700 {
		scenario := map[string]any{"pattern": pick(patterns)}
		if rng.IntN(5) > 0 {
			scenario["path"] = pick(paths)
		}
		if rng.IntN(5) == 0 {
			scenario["limit"] = []any{1, 2, 3, 10, 50, 100, 500, 1000, 5000, 2.5, 0}[rng.IntN(11)]
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// The find tool (core/tools/find.ts, over fd) against pinned Pi on a real tree of 1600 files with a git repository, nested repositories, .gitignore files, hidden entries, node_modules, unicode and spaced names: 700 seeded requests with basename and path globs, brace and character classes, an absolute or missing search directory, a file as the search directory and limits from zero to 5000 (some at fractional values); the result text, error, result-limit and truncation metadata agree.
func TestFindToolMatchesPi(t *testing.T) {
	requireFD(t)
	tree := buildFindTree(t)
	scenarios := findScenarios()
	input, err := json.Marshal(map[string]any{"tree": tree, "scenarios": scenarios})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/find_tool.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []findOutcome
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	tool := &FindTool{CWD: tree}
	failures := 0
	for i, scenario := range scenarios {
		raw, _ := json.Marshal(scenario)
		var got findOutcome
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
			if details, ok := result.Details.(*FindDetails); ok && details != nil {
				if details.ResultLimitReached != nil {
					limit := *details.ResultLimitReached
					got.ResultLimitReached = &limit
				}
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
		var normalized findOutcome
		_ = json.Unmarshal(gotRaw, &normalized)
		// fd walks in parallel, so the subset a result limit keeps is not specified (the same binary gives Pi and Pig different ones); a request that reaches its limit is compared by its notice, the number of results and the metadata, and every other request by its exact text.
		want := expected[i]
		if want.ResultLimitReached != nil && normalized.ResultLimitReached != nil {
			keep := func(outcome findOutcome) findOutcome {
				head, _, _ := strings.Cut(outcome.Text, "\n\n[")
				outcome.Text = fmt.Sprint(strings.Count(head, "\n") + 1)
				if outcome.Truncated != nil {
					truncated := *outcome.Truncated
					truncated.TotalBytes, truncated.OutputBytes = 0, 0
					outcome.Truncated = &truncated
				}
				return outcome
			}
			normalized, want = keep(normalized), keep(want)
		} else {
			// fd's parallel walk prints matches in no specified order and find.ts does not sort them, so two runs of Pi
			// can disagree on order under load; the set of lines and the notice must agree.
			sortLines := func(outcome findOutcome) findOutcome {
				head, notice, hasNotice := strings.Cut(outcome.Text, "\n\n[")
				lines := strings.Split(head, "\n")
				slices.Sort(lines)
				outcome.Text = strings.Join(lines, "\n")
				if hasNotice {
					outcome.Text += "\n\n[" + notice
				}
				return outcome
			}
			normalized, want = sortLines(normalized), sortLines(want)
		}
		if reflect.DeepEqual(normalized, want) {
			continue
		}
		if failures++; failures > 6 {
			continue
		}
		e, _ := json.Marshal(expected[i])
		t.Errorf("scenario %d %s\n  Pig %.700s\n  Pi  %.700s", i, raw, gotRaw, e)
	}
	if failures > 6 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
