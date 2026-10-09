package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// diff.ts:renderDiff against pinned Pi: line classification, tab replacement, and the intra-line word diff (the npm diff package's
// diffWords) for one removed and one added line, which Pi highlights and which is the part a hand-written word splitter gets wrong.
func TestRenderDiffMatchesPi(t *testing.T) {
	pairs := [][2]string{
		{"hello world", "hello universe"},
		{"  indented line here", "  indented line there"},
		{"foo(bar, baz)", "foo(bar, qux)"},
		{"const a = 1;", "const a = 2;"},
		{"same", "same"},
		{"", "added only text"},
		{"removed only text", ""},
		{"a b c d", "a x c d"},
		{"one  two   three", "one two three"},
		{"trailing space ", "trailing space"},
		{"tab\tseparated\tvalues", "tab\tseparated\tother"},
		{"snake_case_name = 1", "snake_case_other = 1"},
		{"x.y.z()", "x.y.w()"},
		{"héllo wörld", "héllo wörld!"},
		{"日本語 text", "日本語 テキスト"},
		{"foo,bar;baz", "foo,qux;baz"},
		{"The quick brown fox", "The slow brown dog"},
		{"    \treturn value;", "    \treturn other;"},
		{"a-b-c", "a-b-d"},
		{"1234 5678", "1234 5679"},
		{"don't stop", "don't start"},
		{"path/to/file.go", "path/to/other.go"},
		{"emoji 🙂 here", "emoji 🙃 here"},
		{"  ", "   x"},
		{"word", "word word"},
		{"a  b", "a b"},
	}
	// Seeded random lines over words, punctuation, runs of spaces, tabs, NBSP and non-Latin letters.
	alphabet := []string{"foo", "bar", "baz", "x", "_", "(", ")", ",", ".", ";", " ", "  ", "\t", "\u00a0", "é", "\u00ad", "×", "ǅ", "ˇ", "ḁ", "\u1eff", "日本", "1", "42", "-", "=>", "\u2003"}
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	randomLine := func() string {
		var b strings.Builder
		for range next(9) {
			b.WriteString(alphabet[next(len(alphabet))])
		}
		return b.String()
	}
	for range 600 {
		pairs = append(pairs, [2]string{randomLine(), randomLine()})
	}
	var diffs []string
	for _, p := range pairs {
		diffs = append(diffs, "-  5 "+p[0]+"\n+  5 "+p[1])
	}
	diffs = append(diffs,
		" 1 context\n-  2 old one\n-  3 old two\n+  2 new one\n+  3 new two\n  4 tail",
		"-  1 only removed\n-  2 second removed\n   3 context",
		"+  1 only added\n+  2 second added",
		"+  1 added first\n-  2 removed after",
		"     ...\n-  7 x\n+  7 y\n     ...",
		"no prefix line\n-  1 a\n+  1 b",
		"-1 a\n+1 b",
		" 10\tcontext\twith tabs\n-11\told\ttab\n+11\tnew\ttab",
		"",
		"\n-  1 a\n\n+  1 b\n",
	)
	input, err := json.Marshal(diffs)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/render_diff.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(previousCaps) })
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark")
	failures := 0
	for i, diff := range diffs {
		if got := RenderDiff(diff); got != expected[i] {
			if failures++; failures > 5 {
				continue
			}
			t.Errorf("diff %q:\n  Pig %q\n  Pi  %q", diff, got, expected[i])
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d diffs differ from Pi", failures, len(diffs))
	}
}
