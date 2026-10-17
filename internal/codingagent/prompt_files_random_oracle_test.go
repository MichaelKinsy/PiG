package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/prompt-templates.ts loadPromptTemplates against pinned Pi over a directory of random prompt files: headers with string and non-string description and argument-hint values, first lines at and around the 60-code-unit truncation point with astral characters, whitespace-only lines, every newline form and a BOM. Files whose YAML Pi rejects are compared by name and warning path only (the message is the YAML library's).
func TestLoadPromptTemplatesRandomFilesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	line := func() string {
		var b strings.Builder
		n := rng.Intn(5)
		if rng.Intn(3) == 0 {
			n = 55 + rng.Intn(10)
		}
		for range n {
			b.WriteString(pick("a", "é", "😀", "日", " ", "\t", "\u00a0", "\u2003", "$1", "x"))
		}
		return b.String()
	}
	scalar := func() string {
		return pick("plain", "\"quoted\"", "'single'", "5", "true", "null", "~", "[a, b]", "{a: 1}", "", "|\n  block", ">\n  folded\n  text", "- x", "\"multi\\nline\"")
	}
	root := t.TempDir()
	for i := range 400 {
		var b strings.Builder
		if rng.Intn(6) == 0 {
			b.WriteString("\ufeff")
		}
		if rng.Intn(2) == 0 {
			b.WriteString("---\n")
			for range rng.Intn(4) {
				b.WriteString(pick("description", "argument-hint", "other") + ": " + scalar() + "\n")
			}
			b.WriteString("---\n")
		}
		for range rng.Intn(5) {
			b.WriteString(line() + pick("\n", "\n", "\r\n", "\r"))
		}
		name := fmt.Sprintf("p%03d.md", i)
		if err := os.WriteFile(filepath.Join(root, name), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	load := map[string]any{"cwd": root, "agentDir": filepath.Join(root, "agent"), "promptPaths": []string{root}, "includeDefaults": false}
	var want []promptOracleLoad
	pioracle.Run(t, promptLoadOracleBody, map[string]any{"loads": []any{load}}, &want)
	got := LoadPromptTemplatesFromOptions(LoadPromptTemplatesOptions{Cwd: root, AgentDir: filepath.Join(root, "agent"), PromptPaths: []string{root}})
	w := want[0]
	failures := 0
	report := func(format string, args ...any) {
		if failures++; failures <= 8 {
			t.Errorf(format, args...)
		}
	}
	if len(got.Templates) != len(w.Templates) {
		report("%d templates, Pi %d", len(got.Templates), len(w.Templates))
	}
	for i := range min(len(got.Templates), len(w.Templates)) {
		tmpl := got.Templates[i]
		if g := (promptOracleTemplate{tmpl.Name, asPiJSON(tmpl.Description), asPiJSON(tmpl.ArgumentHint), asPiJSON(tmpl.Content), tmpl.FilePath}); g != w.Templates[i] {
			report("template %d:\n got %+v\nwant %+v", i, g, w.Templates[i])
		}
	}
	if len(got.Diagnostics) != len(w.Diagnostics) {
		report("%d diagnostics, Pi %d", len(got.Diagnostics), len(w.Diagnostics))
	}
	for i := range min(len(got.Diagnostics), len(w.Diagnostics)) {
		if got.Diagnostics[i].Type != w.Diagnostics[i].Type || got.Diagnostics[i].Path != w.Diagnostics[i].Path {
			report("diagnostic %d: got %+v, Pi %+v", i, got.Diagnostics[i], w.Diagnostics[i])
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}

// asPiJSON is the text as the oracle's JSON delivers Pi's strings: a lone surrogate, which Pig keeps as WTF-8, reads back as U+FFFD.
func asPiJSON(text string) string {
	encoded, err := jsstring.MarshalJSON(text)
	if err != nil {
		return text
	}
	var decoded string
	if json.Unmarshal(encoded, &decoded) != nil {
		return text
	}
	return decoded
}
