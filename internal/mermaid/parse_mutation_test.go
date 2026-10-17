package mermaid

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// mutatedMermaidSources makes n sources by mutating the parser corpus: characters and mermaid markers inserted, deleted, replaced and swapped, lines cut, duplicated and indented, and spans of other sources spliced in.
func mutatedMermaidSources(t *testing.T, corpusFile string, n int, seed int64) []string {
	t.Helper()
	data, err := os.ReadFile(corpusFile)
	if err != nil {
		t.Fatal(err)
	}
	var corpus []string
	for raw := range strings.SplitSeq(string(data), "\n") {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var src string
		if err := json.Unmarshal([]byte(raw), &src); err != nil {
			t.Fatalf("corpus line %q: %v", raw, err)
		}
		corpus = append(corpus, src)
	}
	rng := rand.New(rand.NewSource(seed))
	markers := []string{"-->", "---", "==>", "-.->", "--", "->>", "-->>", "->", "[", "]", "(", ")", "{", "}", "((", "))", "[(", ")]", "|", "\"", "'", ":", ";", "&", "#", "%%", "\n", "\n  ", "  ", "end", "subgraph ", "participant ", "class ", "state ", "[*]", "<<", ">>", "A", "B", "é", "日", "😀", "\t", "\r", "\r\n", "²", "Ⅻ", "½", "style ", "classDef ", "direction TD", "note ", "alt ", "loop ", "{{", "}}", "o--o", "||--o{", "|o--|{", "..>", "<|--"}
	mutate := func(src string) string {
		runes := []rune(src)
		for range 1 + rng.Intn(3) {
			at := 0
			if len(runes) > 0 {
				at = rng.Intn(len(runes) + 1)
			}
			switch rng.Intn(6) {
			case 0, 1:
				runes = append(runes[:at], append([]rune(markers[rng.Intn(len(markers))]), runes[at:]...)...)
			case 2:
				if at < len(runes) {
					runes = append(runes[:at], runes[at+1:]...)
				}
			case 3:
				if at < len(runes) {
					runes[at], _ = utf8.DecodeRuneInString(markers[rng.Intn(len(markers))])
				}
			case 4:
				lines := strings.Split(string(runes), "\n")
				i := rng.Intn(len(lines))
				switch rng.Intn(3) {
				case 0:
					lines[i] = "  " + lines[i]
				case 1:
					lines = append(lines[:i+1], lines[i:]...)
				default:
					lines = append(lines[:i], lines[i+1:]...)
				}
				runes = []rune(strings.Join(lines, "\n"))
			default:
				other := []rune(corpus[rng.Intn(len(corpus))])
				if len(other) > 0 {
					from := rng.Intn(len(other))
					to := from + rng.Intn(len(other)-from+1)
					runes = append(runes[:at], append(append([]rune{}, other[from:to]...), runes[at:]...)...)
				}
			}
		}
		return string(runes)
	}
	out := make([]string, 0, n)
	for len(out) < n {
		out = append(out, mutate(corpus[rng.Intn(len(corpus))]))
	}
	return out
}

// runMermaidOracle runs a script of test/parity/testdata over the sources through the Pi package's grok-mermaid and returns one JSON line per source.
func runMermaidOracle(t *testing.T, script string, sources []string) []string {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	piRoot := filepath.Join(root, "extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if _, err := os.Stat(filepath.Join(piRoot, "node_modules/grok-mermaid/dist/parse.js")); err != nil {
		t.Fatalf("the Pi package with grok-mermaid is not installed (run make parity-deps): %v", err)
	}
	var corpus bytes.Buffer
	for _, src := range sources {
		line, _ := json.Marshal(src)
		corpus.Write(line)
		corpus.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "corpus.txt")
	if err := os.WriteFile(path, corpus.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", filepath.Join(root, "test/parity/testdata", script), path)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "PI_PACKAGE_ROOT="+piRoot)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("mermaid oracle: %v\n%s", err, &stderr)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != len(sources) {
		t.Fatalf("oracle printed %d results for %d sources", len(lines), len(sources))
	}
	return lines
}

// TestParseMatchesUpstreamOnMutatedSources compares grok-mermaid's own parse functions (run live from the pinned Pi package) with the Go parser over mutated corpus sources: the diagram kind and the parsed model, or the null model of a fallback.
func TestParseMatchesUpstreamOnMutatedSources(t *testing.T) {
	sources := mutatedMermaidSources(t, "testdata/parse-corpus.txt", 3000, 20261010)
	lines := runMermaidOracle(t, "mermaid-parse-pi.mjs", sources)
	failures, parsed := 0, 0
	for i, line := range lines {
		var want struct {
			Input string `json:"input"`
			Kind  string `json:"kind"`
			Model any    `json:"model"`
		}
		if err := json.Unmarshal([]byte(line), &want); err != nil {
			t.Fatalf("bad oracle line %q: %v", line, err)
		}
		src := sources[i]
		if want.Model != nil {
			parsed++
		}
		kind := diagramKind(src)
		got := canonical(t, buildParseModel(src))
		if kind != want.Kind || !reflect.DeepEqual(got, want.Model) {
			if failures++; failures <= 8 {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(want.Model)
				t.Errorf("parse(%q): kind %q want %q\n  go: %.400s\n  pi: %.400s", src, kind, want.Kind, gj, wj)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d sources differ", failures, len(sources))
	}
	// Agreement on fallbacks alone would prove nothing.
	if parsed < len(sources)/4 {
		t.Errorf("only %d of %d mutated sources still parse", parsed, len(sources))
	}
}

// TestRenderMatchesUpstreamOnMutatedSources compares grok-mermaid's render() with the Go renderer over mutated render-corpus sources: the art (plain rows, styled spans, width, warnings) or a null fallback.
func TestRenderMatchesUpstreamOnMutatedSources(t *testing.T) {
	sources := mutatedMermaidSources(t, "testdata/corpus.txt", 2000, 20261010)
	lines := runMermaidOracle(t, "mermaid-pi.mjs", sources)
	failures, rendered := 0, 0
	for i, line := range lines {
		var want struct {
			Art any `json:"art"`
		}
		if err := json.Unmarshal([]byte(line), &want); err != nil {
			t.Fatalf("bad oracle line %q: %v", line, err)
		}
		art, ok := Render(sources[i])
		var got any
		if ok {
			got = canonical(t, artJSON(art))
			rendered++
		}
		if !reflect.DeepEqual(got, want.Art) {
			if failures++; failures <= 6 {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(want.Art)
				t.Errorf("render(%q)\n  go: %.500s\n  pi: %.500s", sources[i], gj, wj)
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d sources differ", failures, len(sources))
	}
	if rendered < len(sources)/4 {
		t.Errorf("only %d of %d mutated sources still render", rendered, len(sources))
	}
}
