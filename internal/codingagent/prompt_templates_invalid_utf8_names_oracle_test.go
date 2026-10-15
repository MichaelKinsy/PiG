package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/prompt-templates.ts

// Pi's loadTemplatesFromDir joins each entry's name as readdirSync decoded it and takes the entry type from the directory entry, so a template file named with invalid UTF-8 is read at
// the decoded path and warns ENOENT; PiG must report the same templates and diagnostics.
func TestLoadPromptTemplatesFromDirWithNamesThatAreNotValidUTF8MatchesPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	// The default directory is under the agent directory, where Pi's source lookup needs no stat and each unreadable name warns; an explicit directory elsewhere makes the lookup stat the
	// decoded path, which throws and ends that directory's load.
	defaults, explicit := filepath.Join(agentDir, "prompts"), filepath.Join(base, "explicit")
	for _, root := range []string{defaults, explicit} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		writeInvalidNameTemplates(t, root)
	}
	type shaped struct {
		Templates   []string `json:"templates"`
		Diagnostics []string `json:"diagnostics"`
	}
	for _, c := range []struct {
		name     string
		defaults bool
		paths    []string
	}{{"default directory", true, nil}, {"explicit directory", false, []string{explicit}}, {"both", true, []string{explicit}}} {
		t.Run(c.name, func(t *testing.T) {
			var want struct{ Result shaped }
			pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/prompt-templates.js");
const r = mod.loadPromptTemplates({ cwd: input.base, agentDir: input.agentDir, promptPaths: input.paths, includeDefaults: input.defaults });
emit({ result: { templates: r.templates.map((x) => x.name + "|" + x.filePath), diagnostics: (r.diagnostics ?? []).map((d) => d.type + "|" + d.message + "|" + (d.path ?? "")) } });`, map[string]any{"base": base, "agentDir": agentDir, "paths": append([]string{}, c.paths...), "defaults": c.defaults}, &want)
			got := loadPromptTemplates(base, agentDir, c.defaults, false, c.paths)
			var shapedGot shaped
			for _, tpl := range got.Templates {
				shapedGot.Templates = append(shapedGot.Templates, tpl.Name+"|"+tpl.FilePath)
			}
			for _, d := range got.Diagnostics {
				shapedGot.Diagnostics = append(shapedGot.Diagnostics, d.Type+"|"+d.Message+"|"+d.Path)
			}
			if !slices.Equal(shapedGot.Templates, want.Result.Templates) || !slices.Equal(shapedGot.Diagnostics, want.Result.Diagnostics) {
				t.Errorf("PiG %q\nPi  %q", strings.Join(append(shapedGot.Templates, shapedGot.Diagnostics...), "\n    "), strings.Join(append(want.Result.Templates, want.Result.Diagnostics...), "\n    "))
			}
		})
	}
}

func writeInvalidNameTemplates(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"good.md", "b\xffd.md", "trunc\xe2\x82.md", "notes.txt", "z\xed\xa0\x80.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("---\ndescription: a template\n---\nBody $1\n"), 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "dir\xff.md"), 0o755); err != nil {
		t.Skipf("file system refuses the directory name: %v", err)
	}
}
