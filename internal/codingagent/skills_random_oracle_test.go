package codingagent

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/skills.ts loadSkillsFromDir against pinned Pi over a directory of random skills: names and descriptions at the length limits (counted in UTF-16 code units), with hyphen, case, digit and non-ASCII variations, non-string and absent values, the model-invocation flag in several types, and directory names that match the declared name or not. A YAML syntax error is compared by its path only (its message is the YAML library's).
func TestLoadSkillsRandomDirectoriesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261010))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	quote := func(s string) string {
		return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(s) + "\""
	}
	name := func() string {
		switch rng.Intn(6) {
		case 0:
			return strings.Repeat("a", 62+rng.Intn(5))
		case 1:
			return pick("alpha", "a-b", "a--b", "-a", "a-", "A", "aB", "a_b", "a b", "é", "a1", "1", "日本", "a😀", "")
		default:
			return pick("alpha", "beta", "gamma", "my-skill", "skill-1")
		}
	}
	description := func() string {
		switch rng.Intn(6) {
		case 0:
			return quote(strings.Repeat("d", 1022+rng.Intn(5)))
		case 1:
			return quote(strings.Repeat("😀", 510+rng.Intn(5)))
		case 2:
			return quote(strings.Repeat("é", 1023+rng.Intn(3)) + strings.Repeat("😀", rng.Intn(2)))
		default:
			return pick("short", "\"  \"", "''", "5", "true", "null", "[a]", "{a: 1}", "\"tab\\there\"", "|\n  block text", "text: colon")
		}
	}
	root := t.TempDir()
	for i := range 300 {
		var b strings.Builder
		if rng.Intn(8) == 0 {
			b.WriteString("\ufeff")
		}
		b.WriteString("---\n")
		for _, key := range []string{"name", "description", "disable-model-invocation"} {
			if rng.Intn(8) == 0 {
				continue
			}
			switch key {
			case "name":
				value := name()
				if rng.Intn(5) == 0 {
					value = pick("5", "true", "null", "[a]")
				}
				b.WriteString("name: " + quote(value) + "\n")
			case "description":
				b.WriteString("description: " + description() + "\n")
			default:
				b.WriteString(key + ": " + pick("true", "false", "\"true\"", "yes", "1", "null", "\"false\"", "True") + "\n")
			}
		}
		b.WriteString("---\n" + pick("Body\n", "", "\r\n"))
		dir := filepath.Join(root, fmt.Sprintf("d%03d-%s", i, pick("", "alpha", "my-skill")))
		if rng.Intn(3) == 0 {
			dir = filepath.Join(root, pick("alpha", "beta", "gamma", "my-skill", "skill-1")+fmt.Sprint(i%2))
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want struct {
		Dirs []skillOracleResult `json:"dirs"`
	}
	pioracle.Run(t, skillsOracleBody, map[string]any{"loads": []any{}, "dirs": []map[string]string{{"dir": root, "source": "path"}}}, &want)
	got := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: root, Source: "path"})
	short := func(path string) string { return strings.TrimPrefix(path, root) }
	describe := func(skills []skillOracleSkill, diagnostics []skillOracleDiagnostic, yamlOnlyPath bool) (s, d []string) {
		for _, skill := range skills {
			s = append(s, fmt.Sprintf("%s|%q|%s|%v", skill.Name, skill.Description, short(skill.FilePath), skill.DisableModelInvocation))
		}
		for _, diagnostic := range diagnostics {
			message := diagnostic.Message
			if yamlOnlyPath && strings.Contains(message, "at line") {
				message = ""
			}
			d = append(d, fmt.Sprintf("%s|%q|%s", diagnostic.Type, strings.ReplaceAll(message, root, ""), short(diagnostic.Path)))
		}
		return
	}
	var gotSkills []skillOracleSkill
	for _, s := range got.Skills {
		gotSkills = append(gotSkills, skillOracleSkill{Name: s.Name, Description: asPiJSON(s.Description), FilePath: s.FilePath, DisableModelInvocation: s.DisableModelInvocation})
	}
	var gotDiagnostics []skillOracleDiagnostic
	for _, d := range got.Diagnostics {
		gotDiagnostics = append(gotDiagnostics, skillOracleDiagnostic{Type: d.Type, Message: d.Message, Path: d.Path})
	}
	gs, gd := describe(gotSkills, gotDiagnostics, true)
	ws, wd := describe(want.Dirs[0].Skills, want.Dirs[0].Diagnostics, true)
	if len(ws) < 100 || len(wd) < 100 {
		t.Fatalf("Pi loaded %d skills and reported %d diagnostics: too few to prove anything", len(ws), len(wd))
	}
	failures := 0
	compare := func(kind string, g, w []string) {
		for i := range max(len(g), len(w)) {
			var a, b string
			if i < len(g) {
				a = g[i]
			}
			if i < len(w) {
				b = w[i]
			}
			if a != b {
				if failures++; failures <= 8 {
					t.Errorf("%s %d:\n  Pig %s\n  Pi  %s", kind, i, a, b)
				}
			}
		}
	}
	compare("skill", gs, ws)
	compare("diagnostic", gd, wd)
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}
