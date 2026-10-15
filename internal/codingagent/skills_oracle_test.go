package codingagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type skillOracleSkill struct {
	Name                   string       `json:"name"`
	Description            string       `json:"description"`
	FilePath               string       `json:"filePath"`
	BaseDir                string       `json:"baseDir"`
	SourceInfo             PiSourceInfo `json:"sourceInfo"`
	DisableModelInvocation bool         `json:"disableModelInvocation"`
}

type skillOracleDiagnostic struct {
	Type      string `json:"type"`
	Message   string `json:"message"`
	Path      string `json:"path"`
	Collision *struct {
		ResourceType string `json:"resourceType"`
		Name         string `json:"name"`
		WinnerPath   string `json:"winnerPath"`
		LoserPath    string `json:"loserPath"`
	} `json:"collision"`
}

type skillOracleResult struct {
	Skills      []skillOracleSkill      `json:"skills"`
	Diagnostics []skillOracleDiagnostic `json:"diagnostics"`
}

func skillFixture(t *testing.T, root string) {
	t.Helper()
	write := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skill := func(name, description string, extra string) string {
		return "---\nname: " + name + "\ndescription: " + description + "\n" + extra + "---\nBody\n"
	}
	write("alpha/SKILL.md", skill("alpha", "Alpha skill", ""))
	write("beta/SKILL.md", "---\ndescription: No name uses the directory\n---\nBody")
	write("gamma/SKILL.md", skill("Gamma_Bad", "Bad name", ""))
	write("delta/SKILL.md", skill("-edge-", "Hyphen edges", ""))
	write("epsilon/SKILL.md", skill("a--b", "Double hyphen", ""))
	write("zeta/SKILL.md", skill(strings.Repeat("n", 65), "Long name", ""))
	write("eta/SKILL.md", "---\nname: eta\n---\nMissing description")
	write("theta/SKILL.md", "---\nname: theta\ndescription: \"   \"\n---\nBlank description")
	write("iota/SKILL.md", skill("iota", strings.Repeat("d", 1025), ""))
	write("kappa/SKILL.md", skill("kappa", "\""+strings.Repeat("😀", 513)+"\"", ""))
	write("lambda/SKILL.md", skill("lambda", "Hidden from the model", "disable-model-invocation: true\n"))
	write("mu/SKILL.md", skill("mu", "Truthy string flag", "disable-model-invocation: \"true\"\n"))
	write("nu/SKILL.md", "---\nname: nu\ndescription: [unclosed\n---\nbad yaml in a declared skill")
	write("xi/SKILL.md", "---\nname: 12\ndescription: numeric name\n---\n")
	write("omicron/SKILL.md", "---\nname: \"\"\ndescription: empty name uses the directory\n---\n")
	write("pi/SKILL.md", "no frontmatter at all")
	write("rho/SKILL.md", "---\nname: alpha\ndescription: collides with alpha\n---\n")
	write("nested/deep/SKILL.md", skill("deep", "Nested skill", ""))
	write("nested/other/notes.md", "---\ndescription: ignored below the root\n---\n")
	write("nested/SKILL.md.bak", "ignored")
	write("rootdoc.md", "---\ndescription: Root document skill\n---\nBody")
	write("rootbad.md", "---\ndescription: [unclosed\n---\n")
	write("rootplain.md", "no description")
	write("rootname.md", "---\nname: rn\ndescription: Root with name\n---\n")
	write("node_modules/pkg/SKILL.md", skill("skipped", "node_modules is skipped", ""))
	write(".hidden/SKILL.md", skill("hidden", "dot directories are skipped", ""))
	write("ignored-dir/SKILL.md", skill("ignored-dir", "ignored by .gitignore", ""))
	write("negated/SKILL.md", skill("negated", "re-included", ""))
	write(".gitignore", "ignored-dir/\n*.bak\n# comment\n\n!negated/\nnegated/\n!negated/\n/rootplain.md\n")
	write("sub/.ignore", "inner/\n")
	write("sub/inner/SKILL.md", skill("inner", "ignored by a nested .ignore", ""))
	write("sub/kept/SKILL.md", skill("kept", "kept", ""))
	write("crlf/SKILL.md", "---\r\nname: crlf\r\ndescription: Windows line endings\r\n---\r\nBody")
	write("bom/SKILL.md", "\ufeff---\nname: bom\ndescription: byte order mark\n---\n")
	write("quoted/SKILL.md", "---\nname: quoted\ndescription: 'It''s <tag> & \"quoted\"'\n---\n")
	write("multi/SKILL.md", "---\nname: multi\ndescription: >\n  folded text\n  continues\n---\n")
	testenv.RequireDirectoryLink(t, filepath.Join(root, "alpha"), filepath.Join(root, "linkdir"))
	testenv.Symlink(t, filepath.Join(root, "rootdoc.md"), filepath.Join(root, "linkfile.md"))
	testenv.Symlink(t, filepath.Join(root, "missing"), filepath.Join(root, "broken"))
}

const skillsOracleBody = `
const mod = await load("pi-coding-agent/core/skills.js");
const shape = (r) => ({
	skills: r.skills.map((s) => ({ name: s.name, description: s.description, filePath: s.filePath, baseDir: s.baseDir, sourceInfo: { ...s.sourceInfo, baseDir: s.sourceInfo.baseDir ?? "" }, disableModelInvocation: s.disableModelInvocation })),
	diagnostics: r.diagnostics.map((d) => ({ type: d.type, message: d.message, path: d.path ?? "", collision: d.collision ?? null })),
});
emit({
	loads: input.loads.map((o) => shape(mod.loadSkills(o))),
	dirs: input.dirs.map((o) => shape(mod.loadSkillsFromDir(o))),
});`

// TestLoadSkillsMatchesPi runs loadSkills and loadSkillsFromDir of Pi 1.0.4 and of Pig over one tree of skills: SKILL.md and root documents, frontmatter and name/description validation, ignore files, symlinks, collisions, dot and node_modules directories, defaults and explicit paths.
func TestLoadSkillsMatchesPi(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "cwd")
	agentDir := filepath.Join(base, "agent")
	tree := filepath.Join(base, "tree")
	skillFixture(t, tree)
	userSkills := filepath.Join(agentDir, "skills")
	projectSkills := filepath.Join(cwd, ".pi", "skills")
	skillFixture(t, userSkills)
	if err := os.MkdirAll(filepath.Join(projectSkills, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectSkills, "alpha", "SKILL.md"), []byte("---\nname: alpha\ndescription: project alpha\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lone := filepath.Join(base, "lone.md")
	if err := os.WriteFile(lone, []byte("---\nname: lone\ndescription: A lone file\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(base, "x.txt")
	if err := os.WriteFile(text, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	type loadCase struct {
		Cwd             string   `json:"cwd"`
		AgentDir        string   `json:"agentDir"`
		SkillPaths      []string `json:"skillPaths"`
		IncludeDefaults bool     `json:"includeDefaults"`
	}
	loads := []loadCase{
		{cwd, agentDir, []string{tree}, false},
		{cwd, agentDir, []string{tree, tree, lone, text, filepath.Join(base, "none")}, true},
		{cwd, agentDir, []string{}, true},
		{cwd, agentDir, []string{userSkills, projectSkills, filepath.Join(userSkills, "alpha", "SKILL.md")}, false},
		{cwd, agentDir, []string{" " + lone + " ", filepath.Join(tree, "alpha")}, false},
		{cwd, agentDir, []string{filepath.Join(tree, "nested"), filepath.Join(tree, "sub"), filepath.Join(tree, "rootdoc.md"), filepath.Join(tree, "linkfile.md")}, true},
	}
	type dirCase struct {
		Dir    string `json:"dir"`
		Source string `json:"source"`
	}
	dirs := []dirCase{{tree, "path"}, {filepath.Join(tree, "sub"), "user"}, {filepath.Join(tree, "nested"), "project"}, {filepath.Join(tree, "alpha"), "custom-source"}, {filepath.Join(base, "none"), "path"}}
	var want struct {
		Loads []skillOracleResult `json:"loads"`
		Dirs  []skillOracleResult `json:"dirs"`
	}
	pioracle.Run(t, skillsOracleBody, map[string]any{"loads": loads, "dirs": dirs}, &want)

	convert := func(result LoadSkillsResult) skillOracleResult {
		var out skillOracleResult
		for _, s := range result.Skills {
			out.Skills = append(out.Skills, skillOracleSkill{s.Name, s.Description, s.FilePath, s.BaseDir, s.SourceInfo, s.DisableModelInvocation})
		}
		for _, d := range result.Diagnostics {
			entry := skillOracleDiagnostic{Type: d.Type, Message: d.Message, Path: d.Path}
			if d.Collision != nil {
				entry.Collision = &struct {
					ResourceType string `json:"resourceType"`
					Name         string `json:"name"`
					WinnerPath   string `json:"winnerPath"`
					LoserPath    string `json:"loserPath"`
				}{d.Collision.ResourceType, d.Collision.Name, d.Collision.WinnerPath, d.Collision.LoserPath}
			}
			out.Diagnostics = append(out.Diagnostics, entry)
		}
		return out
	}
	// The text of a YAML syntax error is the YAML library's (see the frontmatter survey test).
	normalizeYAML := func(got, want *skillOracleResult) {
		for i := range want.Diagnostics {
			if i < len(got.Diagnostics) && got.Diagnostics[i].Path == want.Diagnostics[i].Path && strings.HasSuffix(want.Diagnostics[i].Path, "nu/SKILL.md") {
				got.Diagnostics[i].Message = want.Diagnostics[i].Message
			}
		}
	}
	short := func(path string) string { return strings.TrimPrefix(path, base) }
	same := func(t *testing.T, label string, got, want skillOracleResult) {
		t.Helper()
		normalizeYAML(&got, &want)
		describeSkill := func(s skillOracleSkill) string {
			return fmt.Sprintf("%s|%q|%s|%s|%+v|%v", s.Name, s.Description, short(s.FilePath), short(s.BaseDir), PiSourceInfo{Path: short(s.SourceInfo.Path), Source: s.SourceInfo.Source, Scope: s.SourceInfo.Scope, Origin: s.SourceInfo.Origin, BaseDir: short(s.SourceInfo.BaseDir)}, s.DisableModelInvocation)
		}
		describeDiagnostic := func(d skillOracleDiagnostic) string {
			collision := ""
			if d.Collision != nil {
				collision = fmt.Sprintf("%s %s %s %s", d.Collision.ResourceType, d.Collision.Name, short(d.Collision.WinnerPath), short(d.Collision.LoserPath))
			}
			return fmt.Sprintf("%s|%q|%s|%s", d.Type, d.Message, short(d.Path), collision)
		}
		compare := func(kind string, gotLines, wantLines []string) {
			for i := range max(len(gotLines), len(wantLines)) {
				var g, w string
				if i < len(gotLines) {
					g = gotLines[i]
				}
				if i < len(wantLines) {
					w = wantLines[i]
				}
				if g != w {
					t.Errorf("%s %s %d:\n  Pig %s\n  Pi  %s", label, kind, i, g, w)
				}
			}
		}
		var gs, ws, gd, wd []string
		for _, s := range got.Skills {
			gs = append(gs, describeSkill(s))
		}
		for _, s := range want.Skills {
			ws = append(ws, describeSkill(s))
		}
		for _, d := range got.Diagnostics {
			gd = append(gd, describeDiagnostic(d))
		}
		for _, d := range want.Diagnostics {
			wd = append(wd, describeDiagnostic(d))
		}
		compare("skill", gs, ws)
		compare("diagnostic", gd, wd)
	}
	for i, l := range loads {
		got, err := LoadSkills(LoadSkillsOptions{CWD: l.Cwd, AgentDir: l.AgentDir, SkillPaths: l.SkillPaths, IncludeDefaults: l.IncludeDefaults})
		if err != nil {
			t.Fatal(err)
		}
		same(t, "load "+string(rune('0'+i)), convert(got), want.Loads[i])
	}
	for i, d := range dirs {
		same(t, "dir "+string(rune('0'+i)), convert(LoadSkillsFromDir(LoadSkillsFromDirOptions(d))), want.Dirs[i])
	}
	if len(want.Loads[0].Skills) < 15 {
		t.Fatalf("Pi loaded only %d skills from the tree; the fixture no longer reaches the loader", len(want.Loads[0].Skills))
	}
}
