package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extensionDirKinds writes the sources of one extension directory in each SDK language PiG loads. Pi has only the Node
// kind; the others are PiG's own, and every one of them meets the same Package test, which runs before the language is
// looked at (cliExtensionConfigs, configured_resources.go).
var extensionDirKinds = []struct {
	name  string
	files map[string]string
}{
	{"node", map[string]string{"index.ts": "export default function (pi) {}\n", "package.json": `{"name":"x","type":"module"}`}},
	{"go factory", map[string]string{"go.mod": "module example.com/x\n\ngo 1.26\n", "extension.go": "package x\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\nfunc Extension() *sdk.Extension { return nil }\n"}},
	{"go standalone", map[string]string{"go.mod": "module example.com/x\n\ngo 1.26\n", "main.go": "package main\nfunc main() {}\n"}},
	{"rust factory", map[string]string{"Cargo.toml": "[package]\nname = \"x\"\nversion = \"0.1.0\"\n", "src/lib.rs": "pub fn new_extension() -> pig_sdk::Extension { todo!() }\n"}},
	{"python factory", map[string]string{"x.py": "def new_extension() -> Extension:\n    pass\n"}},
}

type extensionDirFixture struct {
	cwd, agentDir, dir, prompt string
	sm                         *codingagent.SettingsManager
}

// newExtensionDirFixture builds one extension directory of the kind, with a prompts/ directory when withPrompts is set, and
// settings that name it under settingsKey ("extensions", "packages", or "" for none).
func newExtensionDirFixture(t *testing.T, files map[string]string, withPrompts bool, settingsKey string) extensionDirFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	f := extensionDirFixture{cwd: filepath.Join(root, "project"), agentDir: filepath.Join(root, "agent"), dir: filepath.Join(root, "x")}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		write(filepath.Join(f.dir, name), content)
	}
	if withPrompts {
		f.prompt = filepath.Join(f.dir, "prompts", "dir-prompt.md")
		write(f.prompt, "---\ndescription: dir prompt\n---\nbody\n")
	}
	if err := os.MkdirAll(f.cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	if settingsKey != "" {
		write(filepath.Join(f.agentDir, "settings.json"), `{"`+settingsKey+`":["`+filepath.ToSlash(f.dir)+`"],"defaultProjectTrust":"always"}`)
	}
	f.sm = codingagent.NewSettingsManager(f.cwd, f.agentDir)
	return f
}

func (f extensionDirFixture) extensionSources(flags Args) []string {
	var sources []string
	for _, config := range collectExtensionConfigs(f.cwd, f.agentDir, f.sm, flags, nil) {
		source := config.Source
		if source == "" {
			source = config.Path
		}
		sources = append(sources, source)
	}
	return sources
}

// loadsFromDir reports whether one of the sources is the directory or an entry file inside it: Pi's settings entry collects a
// directory's own index.ts (collectAutoExtensionEntries, package-manager.ts:597-601), so a Node directory loads as that file.
func (f extensionDirFixture) loadsFromDir(sources []string) bool {
	return slices.ContainsFunc(sources, func(source string) bool {
		return source == f.dir || strings.HasPrefix(source, f.dir+string(filepath.Separator))
	})
}

func (f extensionDirFixture) promptPaths(flags Args) []string {
	return collectPromptPaths(f.cwd, f.agentDir, f.sm, flags, true)
}

// Pi package-manager.ts:1379-1385 resolveLocalExtensionSource: `-e <dir>` goes through collectPackageResources, and with no
// "pi" block an extensions, skills, prompts or themes directory makes the directory a Package, so its prompts register and
// its entry point (index.ts, go.mod, Cargo.toml, a Python factory) loads nothing. The directory is an extension only when
// no such directory exists (:1383-1385). Probed against Pi 1.3.0 with an index.ts and a prompts/ directory
// (scenario extensions-runtime/85-extension-dir-with-prompts).
func TestCLIExtensionDirectoryWithResourceDirIsPackage(t *testing.T) {
	for _, kind := range extensionDirKinds {
		t.Run(kind.name, func(t *testing.T) {
			withPrompts := newExtensionDirFixture(t, kind.files, true, "")
			flags := Args{Extensions: []string{withPrompts.dir}}
			if got := withPrompts.extensionSources(flags); len(got) != 0 {
				t.Fatalf("-e directory with prompts/ loaded extensions %v, want none (it is a Package)", got)
			}
			if got := withPrompts.promptPaths(flags); !slices.Contains(got, withPrompts.prompt) {
				t.Fatalf("-e directory with prompts/ registered prompts %v, want %s", got, withPrompts.prompt)
			}

			plain := newExtensionDirFixture(t, kind.files, false, "")
			flags = Args{Extensions: []string{plain.dir}}
			if got := plain.extensionSources(flags); !plain.loadsFromDir(got) {
				t.Fatalf("-e directory without a resource directory loaded %v, want the directory", got)
			}
		})
	}
}

// Pi package-manager.ts:2390-2411 resolveLocalEntries and :2579-2596 collectFilesFromPaths: a directory named by the settings
// `extensions` list is collected for extensions only, so a directory with an entry point and prompts/ is an extension and
// registers no prompt. The same directory under `packages` is a Package (:1282-1300), as `-e` is.
func TestSettingsExtensionDirectoryWithPromptsIsAnExtensionAndPackageIsAPackage(t *testing.T) {
	for _, kind := range extensionDirKinds {
		t.Run(kind.name, func(t *testing.T) {
			asExtension := newExtensionDirFixture(t, kind.files, true, "extensions")
			if got := asExtension.extensionSources(Args{}); !asExtension.loadsFromDir(got) {
				t.Fatalf("settings extensions entry loaded %v, want the directory", got)
			}
			if got := asExtension.promptPaths(Args{}); slices.Contains(got, asExtension.prompt) {
				t.Fatalf("settings extensions entry registered its prompts/ %v", got)
			}

			asPackage := newExtensionDirFixture(t, kind.files, true, "packages")
			if got := asPackage.extensionSources(Args{}); asPackage.loadsFromDir(got) {
				t.Fatalf("settings packages entry with prompts/ loaded %v, want none (it is a Package)", got)
			}
			if got := asPackage.promptPaths(Args{}); !slices.Contains(got, asPackage.prompt) {
				t.Fatalf("settings packages entry registered prompts %v, want %s", got, asPackage.prompt)
			}

			plainPackage := newExtensionDirFixture(t, kind.files, false, "packages")
			if got := plainPackage.extensionSources(Args{}); !plainPackage.loadsFromDir(got) {
				t.Fatalf("settings packages entry without a resource directory loaded %v, want the directory", got)
			}
		})
	}
}
