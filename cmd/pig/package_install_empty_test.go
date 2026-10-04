package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi package-manager.ts:installAndPersist awaits npm installation and records the source without requiring Pi resources or importing the package's JavaScript.
func TestPackageInstallPlainNpmPersistsWithoutLoadingCode(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "global", true: "project"}[local], func(t *testing.T) {
			f := newPackageProcessFixture(t, `
if(command !== 'npm') throw new Error('unexpected command');
const root = args[args.indexOf('--prefix') + 1];
const pkg = path.join(root, 'node_modules', 'plain-library');
if(args[0] === 'install') {
  if(args[1] !== 'plain-library@7.0.0') throw new Error('wrong pinned source');
  fs.mkdirSync(pkg, {recursive:true});
  fs.writeFileSync(path.join(pkg, 'package.json'), JSON.stringify({name:'plain-library', version:'7.0.0', main:'index.js'}));
  fs.writeFileSync(path.join(pkg, 'index.js'), "require('node:fs').writeFileSync(__filename + '.imported', 'yes'); throw new Error('install must not import code'); module.exports = function(value) { return typeof value === 'number'; };\n");
} else if(args[0] === 'uninstall') {
  if(args[1] !== 'plain-library') throw new Error('wrong uninstall source');
  fs.rmSync(pkg, {recursive:true});
} else throw new Error('unexpected operation');
`)
			t.Chdir(f.cwd)
			const source = "npm:plain-library@7.0.0"
			args := []string{"install", source}
			root := filepath.Join(f.agent, "npm")
			settingsPath := filepath.Join(f.agent, "settings.json")
			if local {
				args = append(args, "--local", "--approve")
				root = filepath.Join(codingagent.ProjectConfigDir(f.cwd), "npm")
				settingsPath = filepath.Join(codingagent.ProjectConfigDir(f.cwd), "settings.json")
			}
			stdout, stderr, code := captureStdoutStderr(t, func() int { return runPackageCommand(args) })
			if code != 0 || stderr != "" || stdout != "Installing "+source+"...\nInstalled "+source+"\n" {
				t.Fatalf("install: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			requirePackageProcessCall(t, f.calls(t), "npm", []string{"install", "plain-library@7.0.0", "--prefix", root, "--legacy-peer-deps"}, "")
			if _, err := os.Stat(filepath.Join(root, "node_modules", "plain-library", "index.js.imported")); !os.IsNotExist(err) {
				t.Fatalf("package code ran during installation: marker stat = %v", err)
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Packages []string `json:"packages"`
			}
			if err := json.Unmarshal(data, &settings); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(settings.Packages, []string{source}) {
				t.Fatalf("persisted packages = %q", settings.Packages)
			}
			stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"list", "--approve"}) })
			title := "User packages:"
			if local {
				title = "Project packages:"
			}
			if want := title + "\n  " + source + "\n    " + filepath.Join(root, "node_modules", "plain-library") + "\n"; code != 0 || stderr != "" || stdout != want {
				t.Fatalf("list: exit=%d stdout=%q stderr=%q; want %q", code, stdout, stderr, want)
			}
			resources, err := packagecontent.Discover(filepath.Join(root, "node_modules", "plain-library"))
			if err != nil || packageResourceCount(resources) != 0 {
				t.Fatalf("ordinary library contributes resources: %+v, %v", resources, err)
			}
			args[0] = "remove"
			_, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand(args) })
			if code != 0 || stderr != "" {
				t.Fatalf("remove: exit=%d stderr=%q", code, stderr)
			}
			stdout, stderr, code = captureStdoutStderr(t, func() int { return runPackageCommand([]string{"list", "--approve"}) })
			if code != 0 || stderr != "" || stdout != "No packages installed.\n" {
				t.Fatalf("list after remove: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func BenchmarkVerifyEmptyNodePackage(b *testing.B) {
	root, agent := b.TempDir(), b.TempDir()
	for name, body := range map[string]string{
		"package.json": `{"name":"plain-library","version":"7.0.0"}`,
		"index.js":     "module.exports = function(value) { return typeof value === 'number'; };\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(filepath.Dir(root), agent)
	b.ReportAllocs()
	for b.Loop() {
		if err := verifyPackageContributesResources(filepath.Dir(root), sm, root, false); err != nil {
			b.Fatal(err)
		}
	}
}

func writeBareGoExtension(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demoext\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package demoext\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\n" +
		"func Extension() *sdk.Extension { return sdk.New(\"demoext\") }\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInstallRejectsAnExtensionDirectoryAsAPackage(t *testing.T) {
	dir := writeBareGoExtension(t)

	err := verifyPackageContributesResources(filepath.Dir(dir), codingagent.NewSettingsManager(filepath.Dir(dir), t.TempDir()), dir, false)
	if err == nil {
		t.Fatal("installing a bare extension directory as a package reported success; " +
			"it contributes no resources and would never load")
	}
	// The message has to name the working path, or the user is left knowing only
	// that it failed.
	for _, want := range []string{"is an extension, not a package", "pig -e "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// The source arrives as typed, relative to the working directory, and only the settings record rebases it onto the settings directory. Resolved against the agent directory instead, `pig install ./ext` found nothing there, skipped the guard, and recorded an extension that never loads.
func TestInstallRejectsAnExtensionDirectoryGivenRelativeToTheWorkingDirectory(t *testing.T) {
	dir := writeBareGoExtension(t)
	sm := codingagent.NewSettingsManager(filepath.Dir(dir), t.TempDir())
	source := "./" + filepath.Base(dir)

	err := verifyPackageContributesResources(filepath.Dir(dir), sm, source, false)
	if err == nil || !strings.Contains(err.Error(), "is an extension, not a package") {
		t.Fatalf("verify %s from its parent = %v, want the extension refusal", source, err)
	}
	if err := installAndPersistPackage(filepath.Dir(dir), sm, source, false, nil); err == nil {
		t.Fatalf("pig install %s from its parent reported success", source)
	}
	if packages := sm.GetGlobalSettings().Packages; len(packages) != 0 {
		t.Fatalf("refused install recorded packages %v", packages)
	}
}

// The guard must not reject a real package. This is the case that would make the
// fix worse than the bug.
func TestInstallAcceptsAPackageUsingConventionDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prompts", "review.md"), []byte("# review\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyPackageContributesResources(filepath.Dir(root), codingagent.NewSettingsManager(filepath.Dir(root), t.TempDir()), root, false); err != nil {
		t.Fatalf("a package with a conventional prompts/ directory was rejected: %v", err)
	}
}

// Every resource kind a package can contribute must keep it installable. A kind
// missing from the count would reject a package that legitimately ships only
// that kind, which is the silent inverse of the bug being fixed.
func TestEveryPackageResourceKindCountsAsAContribution(t *testing.T) {
	cases := map[string]packagecontent.Resources{
		"extensions":        {ExtensionEntries: []string{"e"}},
		"skills":            {SkillDirs: []string{"s"}},
		"prompts":           {PromptFiles: []string{"p"}},
		"themes":            {ThemeFiles: []string{"t"}},
		"agents":            {AgentFiles: []string{"a"}},
		"mcpServers":        {MCPFiles: []string{"m"}},
		"hooks":             {HookFiles: []string{"h"}},
		"agentEnvironments": {AgentEnvironments: []string{"ae"}},
	}
	for kind, resources := range cases {
		if packageResourceCount(resources) != 1 {
			t.Errorf("a package contributing only %s counts as empty and would be rejected", kind)
		}
	}
	if packageResourceCount(packagecontent.Resources{}) != 0 {
		t.Error("an empty package counts as contributing something; the guard would never fire")
	}
}

// The guard must prove an extension rather than infer a language. Language
// inference reports a spec for any recognized build file, so these would all be
// refused, and misnamed as extensions, if the guard trusted it. Each is a
// package Pi installs without complaint.
func TestInstallDoesNotRefuseDirectoriesThatMerelyLookLikeCode(t *testing.T) {
	cases := map[string]map[string]string{
		"plain npm package": {"package.json": `{"name":"demo","version":"1.0.0"}`},
		"npm commonjs entry": {
			"package.json": `{"name":"demo","version":"1.0.0","main":"index.js"}`,
			"index.js":     "module.exports = function isNumber(value) { return typeof value === 'number'; };\n",
		},
		"npm esm entry": {
			"package.json": `{"name":"demo","version":"1.0.0","type":"module"}`,
			"index.js":     "export default function isNumber(value) { return typeof value === 'number'; }\n",
		},
		"npm named exports only": {
			"package.json": `{"name":"demo","version":"1.0.0"}`,
			"index.js":     "export const answer = 42;\n",
		},
		"npm empty entry":                   {"package.json": `{"name":"demo","version":"1.0.0"}`, "index.js": ""},
		"typescript entry without manifest": {"index.ts": "throw new Error('install must not import code');\n"},
		"mjs entry without manifest":        {"main.mjs": "throw new Error('install must not import code');\n"},
		"npm package with pi key":           {"package.json": `{"name":"demo","pi":{"skills":["skills"]}}`},
		"go module with no factory": {
			"go.mod":  "module demo\n\ngo 1.26\n",
			"util.go": "package demo\n\nfunc Helper() {}\n",
		},
		"rust crate with no manifest": {"Cargo.toml": "[package]\nname = \"demo\"\n"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, body := range files {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := verifyPackageContributesResources(filepath.Dir(dir), codingagent.NewSettingsManager(filepath.Dir(dir), t.TempDir()), dir, false); err != nil {
				t.Errorf("refused a package Pi would install: %v", err)
			}
		})
	}
}

// Pi's installAndPersist records a source only after its install succeeds, so a
// failed npm install leaves nothing in settings to remove by hand.
func TestFailedNpmInstallPersistsNothing(t *testing.T) {
	f := newPackageProcessFixture(t, `throw new Error('fork/exec npm: permission denied');`)
	t.Chdir(f.cwd)
	_, _, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"install", "npm:plain-library@7.0.0"}) })
	if code == 0 {
		t.Fatal("install of a failing npm succeeded")
	}
	data, err := os.ReadFile(filepath.Join(f.agent, "settings.json"))
	if err == nil && strings.Contains(string(data), "plain-library") {
		t.Fatalf("failed install persisted its source: %s", data)
	}
}
