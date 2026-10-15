package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// find.ts trims each fd line with JavaScript's trim(): a name ending in U+FEFF loses it and a name ending in U+0085 (NEL, which Go's
// strings.TrimSpace removes) keeps it. U+00A0, U+2028 and U+3000 are JavaScript white space as well; a space-only name is dropped.
func TestFindToolTrimsFdLinesLikePi(t *testing.T) {
	requireFD(t)
	tree, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a\ufeff", "b\u0085", "c\u00a0", "d\u2028", "e\u3000", "f ", "g\ufeff\u0085", "h\u0085\ufeff", "\ufeffi", "\u0085j", "k", "\u0085", "\ufeff", "\u00a0"} {
		if err := os.WriteFile(filepath.Join(tree, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scenarios := []map[string]any{{"pattern": "*"}, {"pattern": "?"}, {"pattern": "*", "path": "."}, {"pattern": "**"}}
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
	sorted := func(text string) []string {
		lines := strings.Split(text, "\n")
		slices.Sort(lines)
		return lines
	}
	tool := &FindTool{CWD: tree}
	for i, scenario := range scenarios {
		raw, _ := json.Marshal(scenario)
		result, err := tool.Execute(context.Background(), "call", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, block := range result.Content {
			if text, ok := block.(ai.TextContent); ok {
				got += text.Text
			}
		}
		want := expected[i].Text + expected[i].Thrown
		if !slices.Equal(sorted(got), sorted(want)) {
			t.Errorf("find %s:\n  Pig %q\n  Pi  %q", raw, sorted(got), sorted(want))
		}
	}
}
