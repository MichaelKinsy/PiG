package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// stagedSDKDir returns the per-build directory this binary stages the lang SDK into under root.
func stagedSDKDir(t *testing.T, root, lang string) string {
	t.Helper()
	dir, err := pigsdklock.StagedSDKDir(root, lang)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// sdkLanguageStripped reports whether this build strips the SDK of a scaffold language, so the loops over the languages leave it out: a
// scaffold of that language is built from the staged SDK, which a build with the strip tag does not carry.
func sdkLanguageStripped(language string) bool {
	return pigstrip.Has(pigstrip.ListFeatures, "extension-sdk-"+language)
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

func TestRunExtensionInitNode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)

	dir := filepath.Join(t.TempDir(), "planner")
	if code := runExtensionInit([]string{dir, "--lang", "typescript", "--json"}); code != 0 {
		t.Fatalf("node init exit code = %d, want 0", code)
	}
	// Node has no SDK package to stage: the runtime supplies Pi's API module.
	if _, err := os.Stat(filepath.Join(home, "sdk")); err == nil {
		t.Fatal("node init staged an SDK")
	}
	source, err := os.ReadFile(filepath.Join(dir, "index.ts"))
	if err != nil {
		t.Fatalf("missing node index.ts: %v", err)
	}
	if !strings.Contains(string(source), "export default function (pi: ExtensionAPI)") || !strings.Contains(string(source), "planner_ping") {
		t.Fatalf("Node scaffold is not a Pi default-export factory with the ping tool:\n%s", source)
	}
	if strings.Contains(string(source), filepath.ToSlash(home)) {
		t.Fatalf("Node scaffold contains a machine-specific path:\n%s", source)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil || !strings.Contains(string(manifest), `"name": "planner"`) {
		t.Fatalf("package.json = %q, %v", manifest, err)
	}
	assertScaffoldHasNoYAML(t, dir)
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
	for _, language := range []string{"go", "python", "rust", "lisp"} {
		if sdkLanguageStripped(language) {
			continue
		}
		t.Run(language, func(t *testing.T) {
			requireSDKFixtureToolchain(t, language)
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

// A Node extension is Pi's default-export factory and an executable Node script has no SDK to speak the wire with, so there is no isolated form to scaffold.
func TestRunExtensionInitRejectsIsolatedNode(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "node-isolated")
	if code := runExtensionInit([]string{dir, "--lang", "node", "--isolated", "--json"}); code == 0 {
		t.Fatal("init scaffolded an isolated Node extension")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("init created a directory for an isolated Node extension")
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
