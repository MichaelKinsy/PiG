package packagemanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pi: packages/coding-agent/src/core/package-manager.ts DefaultPackageManager.resolve. Random fixture trees (local packages with manifests, conventional directories and ignore files, top-level settings entries with globs and +/-/! overrides in both scopes, auto-discovered
// resource directories, ancestor .agents/skills) resolve to the same resource lists in the same precedence order with the same enabled flags and metadata.
func TestResolveMatchesPiOnRandomFixtures(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs ...string) string { return xs[rng.Intn(len(xs))] }
	chance := func(n int) bool { return rng.Intn(n) == 0 }
	jsonOf := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	type fixture struct {
		Files   map[string]string `json:"files"`
		Builtin []string          `json:"builtin"`
	}
	// resourceFiles lays out one resource root: the files below it are the same for each scope.
	resourceFiles := func(files map[string]string, root string) {
		for _, f := range []string{"extensions/a.ts", "extensions/b.ts", "extensions/c/index.ts", "extensions/d.js", "extensions/e.md", "extensions/sub/deep.ts", "skills/s1/SKILL.md", "skills/s2/SKILL.md", "skills/loose.md", "skills/nested/n1/SKILL.md", "prompts/p1.md", "prompts/p2.md", "prompts/sub/p3.md", "themes/t1.json", "themes/t2.json", "themes/notes.txt"} {
			if chance(2) {
				files[root+"/"+f] = "x"
			}
		}
	}
	patterns := func(kind string) []string {
		exts := map[string][]string{"extensions": {"a.ts", "b.ts", "c", "c/index.ts", "d.js", "sub/deep.ts"}, "skills": {"s1", "s2", "loose.md", "nested", "s1/SKILL.md"}, "prompts": {"p1.md", "p2.md", "sub/p3.md"}, "themes": {"t1.json", "t2.json"}}[kind]
		var out []string
		for range rng.Intn(4) {
			name := exts[rng.Intn(len(exts))]
			switch rng.Intn(9) {
			case 0:
				out = append(out, "!"+kind+"/"+name)
			case 1:
				out = append(out, "+"+kind+"/"+name)
			case 2:
				out = append(out, "-"+kind+"/"+name)
			case 3:
				out = append(out, kind+"/*")
			case 4:
				out = append(out, kind)
			case 5:
				out = append(out, "!"+kind+"/*")
			case 6:
				out = append(out, kind+"/missing-"+name)
			case 7:
				out = append(out, "./"+kind+"/"+name)
			default:
				out = append(out, kind+"/"+name)
			}
		}
		return out
	}
	settingsFor := func(scopeBase string) string {
		s := map[string]any{}
		for _, kind := range []string{"extensions", "skills", "prompts", "themes"} {
			if chance(2) {
				s[kind] = patterns(kind)
			}
		}
		var packages []any
		for range rng.Intn(3) {
			source := pick("../work/pkg1", "../work/pkg2", "../work/pkg3", "../../pkg1", "../../pkg2", "$D/work/pkg1", "$D/work/pkg2", "./nonexistent", "../work/solo.ts", "$D/work/pkg2/")
			if scopeBase == "project" {
				source = pick("../../pkg1", "../../pkg2", "../../pkg3", "$D/work/pkg1", "$D/work/pkg2", "../../missing", "../proj2", "$D/work/solo.ts")
			}
			if chance(2) {
				filters := map[string]any{"source": source}
				for _, kind := range []string{"extensions", "skills", "prompts", "themes"} {
					switch {
					case chance(3):
						filters[kind] = []string{"!" + pick("ext/x.ts", "ext/y.ts", "extensions/e1.ts", "extensions/*", "sk", "skills/k1", "prompts/q.md", "themes/th.json", "**/*.ts"), pick("+ext/y.ts", "ext/*.ts", "-skills/k1", "extensions/e1.ts")}[:1+rng.Intn(2)]
					case chance(6):
						// An explicit empty list disables the kind (applyPackageFilter) yet still lists its files, from the manifest or the conventional directory.
						filters[kind] = []string{}
					}
				}
				if chance(4) {
					// autoload: false lists only the files a pattern touches (applyPackageDeltaFilter).
					filters["autoload"] = false
				}
				packages = append(packages, filters)
			} else {
				packages = append(packages, source)
			}
		}
		if len(packages) > 0 {
			s["packages"] = packages
		}
		return jsonOf(s)
	}
	var fixtures []fixture
	for range 400 {
		files := map[string]string{}
		resourceFiles(files, "agent")
		resourceFiles(files, "work/proj/.pi")
		if chance(2) {
			files["home/.agents/skills/h1/SKILL.md"] = "x"
		}
		if chance(2) {
			files["work/proj/.agents/skills/a1/SKILL.md"] = "x"
		}
		if chance(3) {
			files["work/.agents/skills/up1/SKILL.md"] = "x"
		}
		if chance(2) {
			files["work/pkg1/package.json"] = jsonOf(map[string]any{"name": "pkg1", "pi": map[string]any{"extensions": []string{"ext/x.ts", "ext/y.ts"}, "skills": []string{"sk"}, "prompts": []string{"pr"}, "themes": []string{"th"}}})
			files["work/pkg1/ext/x.ts"], files["work/pkg1/ext/y.ts"], files["work/pkg1/sk/k/SKILL.md"], files["work/pkg1/pr/q.md"], files["work/pkg1/th/z.json"] = "x", "x", "x", "x", "x"
		}
		if chance(2) {
			files["work/pkg2/extensions/e1.ts"], files["work/pkg2/extensions/e2.ts"], files["work/pkg2/skills/k1/SKILL.md"], files["work/pkg2/prompts/q.md"], files["work/pkg2/themes/th.json"] = "x", "x", "x", "x", "x"
			if chance(2) {
				files["work/pkg2/.gitignore"] = pick("e2.ts\n", "extensions/e2.ts\n", "skills/\n", "!extensions/e2.ts\n*.ts\n")
			}
		}
		if chance(3) {
			files["work/pkg3/package.json"] = pick(`{"pi":{"extensions":["a.ts","missing.ts"]}}`, `{"pi":{"extensions":"a.ts"}}`, `{"pi":null}`, `{`, `{"pi":{"skills":["s"],"extensions":[]}}`)
			files["work/pkg3/a.ts"], files["work/pkg3/s/SKILL.md"], files["work/pkg3/extensions/conv.ts"] = "x", "x", "x"
		}
		if chance(3) {
			files["work/solo.ts"] = "x"
		}
		if chance(2) {
			files["agent/settings.json"] = settingsFor("user")
		}
		if chance(2) {
			files["work/proj/.pi/settings.json"] = settingsFor("project")
		}
		var builtin []string
		if chance(3) {
			builtin = []string{"mcp"}
		}
		fixtures = append(fixtures, fixture{files, builtin})
	}
	in, _ := json.Marshal(fixtures)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/resolve.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	type entry struct {
		Path        string `json:"path"`
		Enabled     bool   `json:"enabled"`
		Source      string `json:"source"`
		Scope       string `json:"scope"`
		Origin      string `json:"origin"`
		BaseDir     string `json:"baseDir"`
		PackageRoot string `json:"packageRoot"`
	}
	var want []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_OFFLINE", "1")
	base, _ := filepath.EvalSymlinks(t.TempDir())
	var differences []string
	nonEmpty := 0
	for i, f := range fixtures {
		dir := filepath.Join(base, fmt.Sprint(i))
		for file, content := range f.Files {
			target := filepath.Join(dir, strings.Replace(file, "/.pi/", "/"+codingagent.ConfigDirName()+"/", 1))
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.WriteFile(target, []byte(strings.ReplaceAll(content, "$D", dir)), 0o644)
		}
		t.Setenv("HOME", filepath.Join(dir, "home"))
		cwd, agentDir := filepath.Join(dir, "work", "proj"), filepath.Join(dir, "agent")
		_ = os.MkdirAll(cwd, 0o755)
		_ = os.MkdirAll(agentDir, 0o755)
		settings := codingagent.NewSettingsManager(cwd, agentDir)
		manager := NewPackageManager(PackageManagerOptions{CWD: cwd, AgentDir: agentDir, SettingsManager: settings, BuiltinExtensions: f.Builtin})
		got, gerr := manager.Resolve(nil)
		rel := func(p string) string {
			return configDirSuffix.ReplaceAllString(strings.ReplaceAll(p, dir, "$D"), "/.pi$1")
		}
		if errText, ok := want[i]["error"]; ok {
			if gerr == nil {
				differences = append(differences, fmt.Sprintf("fixture %d: Pi fails %s, Go resolves", i, errText))
			}
			continue
		}
		if gerr != nil {
			differences = append(differences, fmt.Sprintf("fixture %d: Go fails %v", i, gerr))
			continue
		}
		for kind, resources := range map[string][]codingagent.ResolvedResource{"extensions": got.Extensions, "skills": got.Skills, "prompts": got.Prompts, "themes": got.Themes} {
			var goList, piList []entry
			for _, r := range resources {
				goList = append(goList, entry{rel(r.Path), r.Enabled, rel(r.Metadata.Source), r.Metadata.Scope, r.Metadata.Origin, rel(r.Metadata.BaseDir), rel(r.Metadata.PackageRoot)})
			}
			_ = json.Unmarshal(want[i][kind], &piList)
			if len(piList) > 0 {
				nonEmpty++
			}
			if !reflect.DeepEqual(goList, piList) && (len(goList) > 0 || len(piList) > 0) {
				g, _ := json.Marshal(goList)
				w, _ := json.Marshal(piList)
				differences = append(differences, fmt.Sprintf("fixture %d %s: settings %q %q pkg3 %q\n  go: %s\n  pi: %s", i, kind, f.Files["agent/settings.json"], f.Files["work/proj/.pi/settings.json"], f.Files["work/pkg3/package.json"], g, w))
			}
		}
	}
	if nonEmpty < 400 {
		t.Fatalf("Pi resolves resources in only %d lists", nonEmpty)
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 6 {
			shown = shown[:6]
		}
		t.Errorf("%d differences from Pi:\n%s", len(differences), strings.Join(shown, "\n"))
	}
}

// configDirSuffix matches PiG's project configuration directory name as a path component.
var configDirSuffix = regexp.MustCompile(`/` + regexp.QuoteMeta(codingagent.ConfigDirName()) + `(/|$)`)
