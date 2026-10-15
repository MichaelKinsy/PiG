package jsdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
)

// pi: the `diff` package (jsdiff 8.0.4) behind Pi's renderDiff (diffWords) and the edit tool's unified patch (diffLines).
func TestDiffMatchesJsdiff(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	words := []string{"the", "quick", "brown", "fox", "jumps", "over", "a", "lazy", "dog", "foo_bar", "x1", "é", "日本語", "😀", "-", "--", ".", ",", "(", ")", "\t", "  ", "\n", "\r\n", " ", "\u00a0", "a-b", "don't"}
	text := func() string {
		var b strings.Builder
		for range rng.Intn(14) {
			b.WriteString(words[rng.Intn(len(words))])
			if rng.Intn(3) != 0 {
				b.WriteString(" ")
			}
			if rng.Intn(6) == 0 {
				b.WriteString("\n")
			}
		}
		return b.String()
	}
	mutate := func(s string) string {
		parts := strings.Fields(s)
		if len(parts) == 0 {
			return text()
		}
		for range 1 + rng.Intn(3) {
			i := rng.Intn(len(parts))
			switch rng.Intn(3) {
			case 0:
				parts = append(parts[:i], parts[i+1:]...)
			case 1:
				parts = append(parts[:i], append([]string{words[rng.Intn(len(words))]}, parts[i:]...)...)
			default:
				parts[i] = words[rng.Intn(len(words))]
			}
			if len(parts) == 0 {
				break
			}
		}
		sep := " "
		if rng.Intn(4) == 0 {
			sep = "\n"
		}
		return strings.Join(parts, sep)
	}
	var pairs [][2]string
	for range 2500 {
		a := text()
		if rng.Intn(3) == 0 {
			pairs = append(pairs, [2]string{a, text()})
		} else {
			pairs = append(pairs, [2]string{a, mutate(a)})
		}
	}
	pairs = append(pairs, [2]string{"", ""}, [2]string{"", "a"}, [2]string{"a", ""}, [2]string{"a\n", "a"}, [2]string{"a", "a\n"}, [2]string{"a\nb\nc\n", "a\nc\n"}, [2]string{"x\r\ny\r\n", "x\ny\n"})
	in, _ := json.Marshal(pairs)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/diff.mjs")
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	type change struct {
		Value          string
		Count          int
		Added, Removed bool
	}
	var want []struct{ Words, Lines []change }
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	convert := func(changes []Change) []change {
		out := make([]change, len(changes))
		for i, c := range changes {
			out[i] = change(c)
		}
		return out
	}
	var differences []string
	changed := 0
	for i, pair := range pairs {
		for kind, got := range map[string][]change{"words": convert(DiffWords(pair[0], pair[1])), "lines": convert(DiffLines(pair[0], pair[1]))} {
			w := want[i].Words
			if kind == "lines" {
				w = want[i].Lines
			}
			if len(w) > 1 {
				changed++
			}
			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", w) {
				differences = append(differences, fmt.Sprintf("%s(%q, %q):\n  go: %+v\n  js: %+v", kind, pair[0], pair[1], got, w))
			}
		}
	}
	if changed < 1500 {
		t.Fatalf("only %d of %d diffs have changes", changed, 2*len(pairs))
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 6 {
			shown = shown[:6]
		}
		t.Errorf("%d differences from jsdiff:\n%s", len(differences), strings.Join(shown, "\n"))
	}
}
