package packagecontent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/core/package-manager.ts

// Pi's directory collectors (collectFiles, collectAutoExtensionEntries, collectSkillEntries) list each entry under the name readdirSync decoded (U+FFFD per ill-formed subpart) and classify it
// from the directory entry, stat'ing only a symlink: a file named with invalid UTF-8 is listed under its decoded path, a directory with such a name is entered and found missing.
func TestConfiguredDirectoriesWithNamesThatAreNotValidUTF8MatchPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	base := t.TempDir()
	write := func(name string) {
		path := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: x\ndescription: x\n---\nbody\n"), 0o644); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	for _, name := range []string{
		"ext/ok.ts", "ext/b\xffd.ts", "ext/t\xe2\x82.js", "ext/sub/index.ts", "ext/sub\xff/index.ts", "ext/note.txt",
		"prompts/ok.md", "prompts/b\xffd.md", "prompts/sub/deep.md", "prompts/dir\xff/deep.md",
		"skills/ok/SKILL.md", "skills/b\xffd/SKILL.md", "skills/nest/inner/SKILL.md", "skills/rootdoc.md", "skills/r\xffdoc.md",
		"themes/ok.json", "themes/b\xffd.json",
	} {
		write(name)
	}
	cwd, agentDir := filepath.Join(base, "cwd"), filepath.Join(base, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var want map[string][]string
	pioracle.Run(t, `
const pm = await load("pi-coding-agent/core/package-manager.js");
const sm = await load("pi-coding-agent/core/settings-manager.js");
const settings = sm.SettingsManager.inMemory({ extensions: [input.base + "/ext"], prompts: [input.base + "/prompts"], skills: [input.base + "/skills"], themes: [input.base + "/themes"] });
const manager = new pm.DefaultPackageManager({ cwd: input.cwd, agentDir: input.agentDir, settingsManager: settings });
const r = await manager.resolve();
emit({ extensions: r.extensions.map((e) => e.path), prompts: r.prompts.map((e) => e.path), skills: r.skills.map((e) => e.path), themes: r.themes.map((e) => e.path) });`,
		map[string]any{"base": base, "cwd": cwd, "agentDir": agentDir}, &want)
	for kind, subdir := range map[Kind]string{Extensions: "ext", Prompts: "prompts", Skills: "skills", Themes: "themes"} {
		got, err := ResolveConfiguredWithError([]string{filepath.Join(base, subdir)}, cwd, kind)
		if err != nil {
			t.Fatal(err)
		}
		expected := slices.Clone(want[string(kind)])
		if kind == Skills {
			// Pi lists a skill by its SKILL.md; PiG lists the skill directory.
			for i, path := range expected {
				expected[i] = strings.TrimSuffix(path, string(filepath.Separator)+"SKILL.md")
			}
		}
		slices.Sort(got)
		slices.Sort(expected)
		if !slices.Equal(got, expected) {
			t.Errorf("%s:\n  PiG %q\n  Pi  %q", kind, got, expected)
		}
	}
}
