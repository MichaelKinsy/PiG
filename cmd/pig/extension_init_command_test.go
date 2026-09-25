package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestRunExtensionInitScaffoldsAndStagesSDK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "my-planner")
	code := runExtensionInit([]string{dir, "--json"})
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0", code)
	}

	for _, rel := range []string{"go.mod", "extension.go"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("missing scaffolded %s: %v", rel, err)
		}
	}

	stagedSDK := filepath.Join(home, "state", "pigsdk", "sdk")
	if _, err := os.Stat(filepath.Join(stagedSDK, "go.mod")); err != nil {
		t.Fatalf("init did not stage SDK at %s: %v", stagedSDK, err)
	}

	goMod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	if !strings.Contains(string(goMod), "require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0") {
		t.Fatalf("go.mod missing SDK requirement:\n%s", goMod)
	}
	if strings.Contains(string(goMod), "replace github.com/MichaelKinsy/PiG/extensions/sdk") || strings.Contains(string(goMod), filepath.ToSlash(stagedSDK)) {
		t.Fatalf("go.mod committed a machine-specific SDK path:\n%s", goMod)
	}

	main, err := os.ReadFile(filepath.Join(dir, "extension.go"))
	if err != nil {
		t.Fatalf("read extension.go: %v", err)
	}
	if !strings.Contains(string(main), `sdk.New("my-planner")`) {
		t.Fatalf("extension.go did not use the derived name:\n%s", main)
	}
	assertScaffoldHasNoYAML(t, dir)
}

func TestRunExtensionInitScaffoldsLoginExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	dir := filepath.Join(t.TempDir(), "science-pig")

	if code := runExtensionInit([]string{dir, "--login", "--json"}); code != 0 {
		t.Fatalf("login init exit code = %d, want 0", code)
	}
	source, err := os.ReadFile(filepath.Join(dir, "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ctx.SetLogin(loginDefinition())", "Custom Extension", "Extension-provided login", "Replace these grids and text"} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("login scaffold missing %q:\n%s", want, source)
		}
	}
	reports, err := validateExtensionRuntimes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || !reports[0].Valid {
		t.Fatalf("login scaffold validation reports = %#v", reports)
	}
	assertScaffoldHasNoYAML(t, dir)
}

func TestRunExtensionInitRejectsLoginForUnsupportedForm(t *testing.T) {
	for _, args := range [][]string{
		{"--login", "--lang", "python", "--json"},
		{"--login", "--isolated", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			dir := filepath.Join(t.TempDir(), "science-pig")
			if code := runExtensionInit(append([]string{dir}, args...)); code == 0 {
				t.Fatalf("login init accepted unsupported arguments %v", args)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("unsupported login scaffold created directory: %v", err)
			}
		})
	}
}

func TestRunExtensionInitPython(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "planner")
	if code := runExtensionInit([]string{dir, "--lang", "python", "--json"}); code != 0 {
		t.Fatalf("python init exit code = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(home, "state", "pigsdk", "sdk-py", "pig_sdk", "__init__.py")); err != nil {
		t.Fatalf("python init did not stage the Python SDK: %v", err)
	}
	mod := filepath.Join(dir, "planner.py")
	if _, err := os.Stat(mod); err != nil {
		t.Fatalf("missing python module: %v", err)
	}
	moduleSource, err := os.ReadFile(mod)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(moduleSource), filepath.ToSlash(home)) {
		t.Fatalf("Python scaffold contains a machine-specific SDK path:\n%s", moduleSource)
	}
	assertScaffoldHasNoYAML(t, dir)

}

func TestRunExtensionInitRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "planner")
	if code := runExtensionInit([]string{dir, "--name", "planner", "--lang", "rust", "--json"}); code != 0 {
		t.Fatalf("rust init exit code = %d, want 0", code)
	}

	if _, err := os.Stat(filepath.Join(home, "state", "pigsdk", "sdk-rs", "Cargo.toml")); err != nil {
		t.Fatalf("rust init did not stage the Rust SDK: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "lib.rs")); err != nil {
		t.Fatalf("missing rust src/lib.rs: %v", err)
	}
	cargo, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cargo), `name = "planner"`) {
		t.Fatalf("Cargo.toml package name should match factory identity:\n%s", cargo)
	}
	if !strings.Contains(string(cargo), `pig-sdk = "0.1.0"`) {
		t.Fatalf("Cargo.toml missing versioned pig-sdk dependency:\n%s", cargo)
	}
	stagedRs := filepath.Join(home, "state", "pigsdk", "sdk-rs")
	if strings.Contains(string(cargo), "path =") || strings.Contains(string(cargo), filepath.ToSlash(stagedRs)) {
		t.Fatalf("Cargo.toml committed a machine-specific SDK path:\n%s", cargo)
	}
	assertScaffoldHasNoYAML(t, dir)
}

func TestRunExtensionInitRefusesOverwriteWithoutForce(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runExtensionInit([]string{dir, "--name", "x", "--json"}); code == 0 {
		t.Fatal("init overwrote an existing go.mod without --force")
	}
	if code := runExtensionInit([]string{dir, "--name", "x", "--force", "--json"}); code != 0 {
		t.Fatalf("init --force failed: code %d", code)
	}
}

func TestRunExtensionInitRejectsUnknownLang(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "e")
	if code := runExtensionInit([]string{dir, "--lang", "cobol", "--json"}); code == 0 {
		t.Fatal("init accepted an unsupported --lang")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("init created a directory for an unsupported language")
	}
}

func TestRunExtensionInitIsolatedScaffoldsStandalone(t *testing.T) {
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			dir := filepath.Join(t.TempDir(), language+"-isolated")
			if code := runExtensionInit([]string{dir, "--lang", language, "--isolated", "--json"}); code != 0 {
				t.Fatalf("isolated init code = %d", code)
			}
			config, _, err := subprocess.ResolveExtConfig(dir)
			if err != nil {
				t.Fatal(err)
			}
			if config.EntrypointKind != "standalone" || config.Isolation != "isolated" {
				t.Fatalf("standalone config = %#v", config)
			}
			assertScaffoldHasNoYAML(t, dir)
		})
	}
}

func assertScaffoldHasNoYAML(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
			t.Errorf("scaffold emitted YAML file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSanitizeExtensionName(t *testing.T) {
	cases := map[string]string{
		"my-planner":     "my-planner",
		"My Planner":     "my-planner",
		"planner_v2":     "planner-v2",
		"  Foo!!Bar  ":   "foo-bar",
		"---edge---":     "edge",
		"github.com/x/y": "github-com-x-y",
	}
	for in, want := range cases {
		if got := sanitizeExtensionName(in); got != want {
			t.Errorf("sanitizeExtensionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtensionInitScaffoldsBuildAndRegister(t *testing.T) {
	if testing.Short() {
		t.Skip("scaffold registration builds language fixtures")
	}
	for _, language := range []string{"go", "python", "rust"} {
		for _, isolated := range []bool{false, true} {
			name := language + "-fixture"
			if isolated {
				name += "-isolated"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("PIG_HOME", t.TempDir())
				root := filepath.Join(t.TempDir(), name)
				args := []string{root, "--lang", language, "--json"}
				if isolated {
					args = append(args, "--isolated")
				}
				if code := runExtensionInit(args); code != 0 {
					t.Fatalf("init code = %d", code)
				}
				reports, err := validateExtensionRuntimes(root)
				if err != nil {
					t.Fatal(err)
				}
				if len(reports) != 1 || !reports[0].Valid || !reports[0].Registered || reports[0].Name != name {
					t.Fatalf("validation reports = %#v", reports)
				}
				wantForm := "factory"
				if isolated {
					wantForm = "standalone"
				}
				if reports[0].Definition == nil || reports[0].Definition.Form != wantForm {
					t.Fatalf("definition = %#v, want %s", reports[0].Definition, wantForm)
				}
			})
		}
	}
}

func TestScaffoldNormalizesDigitPrefixedLanguageIdentifiers(t *testing.T) {
	pythonFiles := scaffoldFiles("python", "9-test", "9-test", t.TempDir(), false, false)
	if len(pythonFiles) != 1 || pythonFiles[0].rel != "ext_9_test.py" || !strings.Contains(pythonFiles[0].body, "def new_extension()") {
		t.Fatalf("Python scaffold = %#v", pythonFiles)
	}
	rustFiles := scaffoldFiles("rust", "9-test", "9-test", t.TempDir(), false, false)
	if len(rustFiles) < 1 || !strings.Contains(rustFiles[0].body, `name = "ext-9-test"`) {
		t.Fatalf("Rust scaffold = %#v", rustFiles)
	}
}
