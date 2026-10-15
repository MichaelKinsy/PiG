package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/skills.ts

// Pi's skill loader (loadSkillsFromDirInternal) joins each directory entry's name as readdirSync decoded it (U+FFFD per ill-formed subpart) and takes the entry type from the
// directory entry: a file named with invalid UTF-8 is read at the decoded path and warns ENOENT, a directory is entered and found missing, a SKILL.md symlink is stat'ed.
func TestLoadSkillsFromDirWithNamesThatAreNotValidUTF8MatchesPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	root := t.TempDir()
	skill := func(name string) string {
		return "---\nname: " + name + "\ndescription: " + name + " skill\n---\nBody\n"
	}
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	write("good.md", skill("good"))
	write("b\xffd.md", skill("bad"))
	write("trunc\xe2\x82.md", skill("trunc"))
	write("dir\xff/SKILL.md", skill("nested-bad"))
	write("ok/SKILL.md", skill("ok"))
	write("ok2\xff/inner/SKILL.md", skill("deep"))
	type shaped struct {
		Skills      []string `json:"skills"`
		Diagnostics []string `json:"diagnostics"`
	}
	var want struct{ Result shaped }
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/skills.js");
const r = mod.loadSkillsFromDir({ dir: input.dir, source: "path" });
emit({ result: { skills: r.skills.map((s) => s.name + "|" + s.filePath), diagnostics: r.diagnostics.map((d) => d.type + "|" + d.message + "|" + (d.path ?? "")) } });`, map[string]any{"dir": root}, &want)
	var got shaped
	result := LoadSkillsFromDir(LoadSkillsFromDirOptions{Dir: root, Source: "path"})
	for _, s := range result.Skills {
		got.Skills = append(got.Skills, s.Name+"|"+s.FilePath)
	}
	for _, d := range result.Diagnostics {
		got.Diagnostics = append(got.Diagnostics, d.Type+"|"+d.Message+"|"+d.Path)
	}
	if !reflect.DeepEqual(got, want.Result) {
		t.Errorf("PiG %q\nPi  %q", strings.Join(append(got.Skills, got.Diagnostics...), "\n    "), strings.Join(append(want.Result.Skills, want.Result.Diagnostics...), "\n    "))
	}
}
