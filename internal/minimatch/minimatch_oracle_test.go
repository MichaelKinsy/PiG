package minimatch

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os/exec"
	"strings"
	"testing"
)

// minimatch(name, pattern, options) against the installed minimatch 10.2.6 over generated patterns and names: wildcards, globstars, character
// classes (including POSIX classes and negation), brace sets and sequences, extglobs, negation, escapes, dots and Unicode, with and without
// nocase and dot.
func TestMatchAgainstNodeMinimatch(t *testing.T) {
	r := rand.New(rand.NewPCG(13, 31))
	patternAtoms := []string{"a", "b", "A", "ab", "abc", ".", "..", "*", "**", "?", "[ab]", "[!a]", "[^a]", "[a-c]", "[[:alpha:]]", "[[:digit:]a]", "[]]", "[a", "{a,b}", "{a,b}c", "{1..3}", "{a..c}", "{,a}", "{a}", "{}", "!", "!!", "@(a|b)", "+(a|b)", "*(a)", "?(a|b)", "!(a)", "!(a|ab)", "+(a|*(b))", "@(a|@(b|c))", "?(+(a))", "**/", "/**/", "*/", "/**", "a/**/b", "{a,b/c}", "{**,a}/", "!(a)/b", "\\*", "\\?", "\\[a]", "\\\\", "/", "/", "//", "#", "-", "é", "É", "İ", "ß", "Σ", "ς", "x.y", ".a", "(", ")", "|", "a|b", "\u00a0", " "}
	nameAtoms := []string{"a/b", "a/b/c", "a/.b/c", "b/a", "a", "b", "A", "B", "ab", "abc", "abcb", ".", "..", ".a", "a.b", "c", "1", "2", "3", "-", "é", "É", "i̇", "İ", "i", "ß", "SS", "σ", "ς", "Σ", "x.y", "*", "?", "[a]", "a|b", "#", "!", "(", ")", "\u00a0", " "}
	build := func(atoms []string, maxN int, slashes bool) string {
		var b strings.Builder
		for range 1 + r.IntN(maxN) {
			b.WriteString(atoms[r.IntN(len(atoms))])
			if slashes && r.IntN(5) == 0 {
				b.WriteByte('/')
			}
		}
		return b.String()
	}
	type probe struct {
		Name    string `json:"name"`
		Pattern string `json:"pattern"`
		NoCase  bool   `json:"nocase"`
		Dot     bool   `json:"dot"`
	}
	var probes []probe
	for range 60000 {
		p := probe{Name: build(nameAtoms, 5, true), Pattern: build(patternAtoms, 6, false), NoCase: r.IntN(2) == 0, Dot: r.IntN(4) == 0}
		if r.IntN(3) == 0 {
			p.Name = strings.ToLower(p.Name)
		}
		probes = append(probes, p)
		// The pattern against names it is likely to match: itself with wildcards taken out.
		reduced := strings.NewReplacer("*", "a", "?", "b", "{", "", "}", "", "[", "", "]", "", "!", "", "@", "", "+", "").Replace(p.Pattern)
		probes = append(probes, probe{Name: reduced, Pattern: p.Pattern, NoCase: p.NoCase, Dot: p.Dot})
	}
	// Paths against patterns made only of path portions, which exercise the globstar sections.
	portions := func(atoms []string, maxN int) string {
		parts := make([]string, 1+r.IntN(maxN))
		for i := range parts {
			parts[i] = atoms[r.IntN(len(atoms))]
		}
		return strings.Join(parts, "/")
	}
	for range 40000 {
		probes = append(probes, probe{Name: portions([]string{"a", "b", "c", ".a", "", "a.b"}, 7), Pattern: portions([]string{"a", "b", "**", "**", "*", "?", "a*", ".*", ""}, 6), NoCase: r.IntN(2) == 0, Dot: r.IntN(3) == 0})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/minimatch.mjs", "10.2.6")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("minimatch oracle: %v\n%s", err, &stderr)
	}
	var want []any
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, matches := 0, 0
	for i, p := range probes {
		if want[i] == "threw" {
			continue
		}
		got := Match(p.Name, p.Pattern, Options{NoCase: p.NoCase, Dot: p.Dot})
		if want[i] == true {
			matches++
		}
		if got != want[i] {
			if failures++; failures <= 15 {
				t.Errorf("Match(%q, %q, nocase=%v dot=%v) = %v, minimatch %v", p.Name, p.Pattern, p.NoCase, p.Dot, got, want[i])
			}
		}
	}
	if failures > 15 {
		t.Errorf("%d of %d probes differ", failures, len(probes))
	}
	// The probes must include matches, or agreement on false everywhere would prove nothing.
	if matches < len(probes)/20 {
		t.Errorf("only %d of %d probes match", matches, len(probes))
	}
}
