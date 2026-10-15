package aotruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runtimeTreeSource is PiG's embedded runtime tree in the repository. The staging tests read it; they skip when the Piglet is built outside the repository.
func runtimeTreeSource(t *testing.T) string {
	t.Helper()
	tree, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "coding", "extension", "host", "subprocess", "runtime-node"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tree, "runtime-modules.mjs")); err != nil {
		t.Skipf("no runtime tree at %s", tree)
	}
	return tree
}

func stagedTree(t *testing.T) stage {
	t.Helper()
	st, err := stageRuntime(runtimeTreeSource(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// The staged copy shares files with the runtime tree where it can, so an edit of the copy must replace the file, never write through the link.
func TestSplitLargeLiteralsWrapsEachLargeDeclarationAndLeavesTheTreeAlone(t *testing.T) {
	tree := t.TempDir()
	big := "{ " + strings.Repeat("a: 1, ", largeLiteralBytes/4) + "}"
	chunk := "var small = { a: 1 };\nvar catalog_default = " + big + ";\nvar CATALOG = flatten(\"x\", catalog_default);\nexport { CATALOG };\n"
	path := filepath.Join(tree, "chunk.js")
	if err := os.WriteFile(path, []byte(chunk), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := stageRuntime(tree, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := st.splitLargeLiterals()
	if err != nil || wrapped != 1 {
		t.Fatalf("wrapped %d, %v; want 1", wrapped, err)
	}
	got, _ := os.ReadFile(filepath.Join(st.dir, stageTree, "chunk.js"))
	if !strings.Contains(string(got), "var catalog_default = (() => ("+big+"))();") {
		t.Errorf("the large declaration is not wrapped in an immediately invoked function")
	}
	if !strings.Contains(string(got), "var small = { a: 1 };") || !strings.Contains(string(got), "var CATALOG = flatten(") {
		t.Errorf("other lines changed:\n%.200s", got)
	}
	if original, _ := os.ReadFile(path); string(original) != chunk {
		t.Error("splitting wrote through to the runtime tree")
	}
}

func TestPatchHostExecutableReplacesEveryReadInThePiModules(t *testing.T) {
	st := stagedTree(t)
	changed, err := st.patchHostExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if changed < 3 {
		t.Fatalf("patched %d files; the vendored Pi modules read process.execPath in at least config.js, photon.js and native-module-path.js", changed)
	}
	root := filepath.Join(st.dir, stageTree, "shims", "pi-dist")
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || (filepath.Ext(path) != ".js" && filepath.Ext(path) != ".mjs") {
			return err
		}
		data, _ := os.ReadFile(path)
		stripped := strings.ReplaceAll(string(data), hostExecutable, "")
		if strings.Contains(stripped, "process.execPath") {
			t.Errorf("%s still reads process.execPath", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The replacement itself contains "process.execPath", so only its marker shows a write through to the tree.
	original, _ := os.ReadFile(filepath.Join(runtimeTreeSource(t), "shims", "pi-dist", "pi-coding-agent", "config.js"))
	if !strings.Contains(string(original), "process.execPath") || strings.Contains(string(original), "pig.hostExecutable") {
		t.Error("the runtime tree itself changed")
	}
}

// Patching a staged Pi module replaces the staged file; the runtime tree it is linked from keeps its bytes.
func TestPatchHostExecutableLeavesTheTreeAlone(t *testing.T) {
	tree := t.TempDir()
	module := "export const dir = dirname(process.execPath);\n"
	path := filepath.Join(tree, "shims", "pi-dist", "pi-coding-agent", "config.js")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := stageRuntime(tree, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := st.patchHostExecutable(); err != nil || changed != 1 {
		t.Fatalf("patched %d, %v; want 1", changed, err)
	}
	staged, _ := os.ReadFile(filepath.Join(st.dir, stageTree, "shims", "pi-dist", "pi-coding-agent", "config.js"))
	if string(staged) != "export const dir = dirname("+hostExecutable+");\n" {
		t.Errorf("staged module %q", staged)
	}
	if original, _ := os.ReadFile(path); string(original) != module {
		t.Errorf("patching wrote through to the runtime tree: %q", original)
	}
}

func TestShimPathsAndConfigMapEveryPiPackageName(t *testing.T) {
	st := stagedTree(t)
	configPath, err := st.writeConfig()
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	data, _ := os.ReadFile(configPath)
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []string{"@earendil-works/pi-coding-agent", "@mariozechner/pi-coding-agent", "@earendil-works/pi-tui", "@earendil-works/pi-ai", "@earendil-works/pi-agent-core", "typebox", "@sinclair/typebox/value"} {
		files := config.CompilerOptions.Paths[spec]
		if len(files) != 1 {
			t.Errorf("%s maps to %v", spec, files)
			continue
		}
		if _, err := os.Stat(files[0]); err != nil {
			t.Errorf("%s maps to %s: %v", spec, files[0], err)
		}
	}
}

// runtime-modules.mjs is the contract between the runtime tree and a bundling host. The entry the Piglet generates must serve every listed module except the
// ones it deliberately leaves out, and every worker; a list that grows without the Piglet noticing would fail at run time, in the extension's process.
func TestEntryServesEveryRuntimeModuleTheTreeLists(t *testing.T) {
	st := stagedTree(t)
	lists, err := st.runtimeModules()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.writeEntry([]Member{{Name: "a", Entry: "/ext/a/index.ts"}, {Name: "b", Entry: "/ext/b/main.ts"}}, "pig-aot-host test", false); err != nil {
		t.Fatal(err)
	}
	startData, _ := os.ReadFile(filepath.Join(st.dir, "aot-start.mjs"))
	start := string(startData)
	for _, spec := range lists.requires {
		served := strings.Contains(start, `"`+spec+`": () => require(`)
		if notCompiledIn[spec] == served {
			t.Errorf("require %s: served=%v, left out=%v", spec, served, notCompiledIn[spec])
		}
	}
	for _, spec := range lists.workers {
		if !strings.Contains(start, `"`+spec+`": "./runtime/`) {
			t.Errorf("worker %s is not served", spec)
		}
	}
	for spec := range notCompiledIn {
		found := false
		for _, listed := range lists.requires {
			found = found || listed == spec
		}
		if !found {
			t.Errorf("notCompiledIn names %s, which the tree no longer lists", spec)
		}
	}
	factories, _ := os.ReadFile(filepath.Join(st.dir, "aot-factories.mjs"))
	for _, want := range []string{`import * as m0 from "/ext/a/index.ts";`, `import * as m1 from "/ext/b/main.ts";`, `["/ext/a/index.ts", factoryOf(m0)]`, `["/ext/b/main.ts", factoryOf(m1)]`} {
		if !strings.Contains(string(factories), want) {
			t.Errorf("aot-factories.mjs lacks %q", want)
		}
	}
	for _, want := range []string{"--version", "process.exit(87)", `pig.hostExecutable`, "execFileSync(found"} {
		if !strings.Contains(start, want) {
			t.Errorf("aot-start.mjs lacks %q", want)
		}
	}
	// Extension modules import after the host's hooks and Node check: module evaluation is in import order.
	entry, _ := os.ReadFile(filepath.Join(st.dir, stageEntry))
	if i, j := strings.Index(string(entry), "aot-start.mjs"), strings.Index(string(entry), "aot-factories.mjs"); i < 0 || j < i {
		t.Errorf("the entry imports aot-start.mjs at %d and aot-factories.mjs at %d; the start module must come first", i, j)
	}
}

func TestLayoutAssetsPutsPiPackageFilesBesideTheExecutable(t *testing.T) {
	st := stagedTree(t)
	out := t.TempDir()
	if err := st.layoutAssets(out, "linux-x64"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "theme/dark.json", "export-html/template.html", "photon_rs_bg.wasm", "native/linux/prebuilds/linux-x64/linux-platform-x11.node"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("assets lack %s: %v", name, err)
		}
	}
}

func TestCopyNoticesPutsTheCompilersLicensesAndASourcePointerBesideTheImage(t *testing.T) {
	compiler, out := t.TempDir(), filepath.Join(t.TempDir(), "licenses")
	for name, text := range map[string]string{"LICENSE": "mit", "THIRD_PARTY_NOTICES.md": "lgpl", "bun": "not a notice"} {
		if err := os.WriteFile(filepath.Join(compiler, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyNotices(compiler, out); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"LICENSE": "mit", "THIRD_PARTY_NOTICES.md": "lgpl"} {
		if got, err := os.ReadFile(filepath.Join(out, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "bun")); err == nil {
		t.Error("the compiler executable was copied as a notice")
	}
	notice, err := os.ReadFile(filepath.Join(out, "NOTICE.txt"))
	if err != nil || !strings.Contains(string(notice), sourcesURL) || !strings.Contains(string(notice), "JavaScriptCore") {
		t.Errorf("NOTICE.txt = %q, %v", notice, err)
	}
}

func TestDynamicEntryCompilesJitiIn(t *testing.T) {
	// The test patches a staged file; it works on a copy of the tree so that a regression cannot reach the real one.
	source := t.TempDir()
	if err := os.CopyFS(source, os.DirFS(runtimeTreeSource(t))); err != nil {
		t.Fatal(err)
	}
	st, err := stageRuntime(source, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.writeEntry(nil, "pig-aot-host test", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(st.dir, "aot-start.mjs"))
	for spec := range notCompiledIn {
		if !strings.Contains(string(data), `"`+spec+`": () => require(`) {
			t.Errorf("a dynamic host does not serve %s", spec)
		}
	}
	lists, err := st.runtimeModules()
	if err != nil {
		t.Fatal(err)
	}
	sourceLoader := filepath.Join(source, "jiti-loader.mjs")
	before, _ := os.ReadFile(sourceLoader)
	if err := st.patchVirtualModules(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(sourceLoader); !bytes.Equal(after, before) {
		t.Fatal("patching the staged jiti loader changed the source runtime tree through a hard link")
	}
	if loader, _ := os.ReadFile(filepath.Join(st.dir, stageTree, "jiti-loader.mjs")); strings.Contains(string(loader), jitiVirtualRequire) || !strings.Contains(string(loader), "pig.shimRequire") {
		t.Error("the jiti loader still requires virtual modules by path")
	}
	shims, err := st.virtualModuleFiles()
	if err != nil || len(shims) < 5 {
		t.Fatalf("virtual module files = %v, %v", shims, err)
	}
	for _, spec := range shims {
		if !strings.Contains(string(data), "  "+strconv.Quote(spec)+": () => require(") {
			t.Errorf("a dynamic host does not bundle the virtual module file %s", spec)
		}
	}
	for _, spec := range lists.sources {
		if !strings.Contains(string(data), "  "+strconv.Quote(spec)+": ") {
			t.Errorf("a dynamic host does not embed the source of %s", spec)
		}
	}
}
