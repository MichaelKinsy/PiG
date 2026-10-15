package runtimecell

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// testSDKHash is the fixed bundle hash this package registers, since it does not link the real pigsdk bundles.
func testSDKHash() (string, error) { return "0123456789abcdef", nil }

func init() {
	pigsdklock.RegisterSDK("go", "sdk", testSDKHash)
	pigsdklock.RegisterSDK("python", "sdk-py", testSDKHash)
	pigsdklock.RegisterSDK("rust", "sdk-rs", testSDKHash)
}

// testStagedSDKDir returns the directory lang's SDK is staged in under configRoot.
func testStagedSDKDir(t *testing.T, configRoot, lang string) string {
	t.Helper()
	dir, err := pigsdklock.StagedSDKDir(configRoot, lang)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// stageSDKMarker writes the marker file that identifies a staged SDK root so the
// resolver treats dir as a valid SDK.
func stageSDKMarker(t *testing.T, dir, marker string) {
	t.Helper()
	full := filepath.Join(dir, marker)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStagedSDKRootsDoNotFallBackOutsideExplicitConfigRoot(t *testing.T) {
	home := t.TempDir()
	configRoot := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))

	got := stagedSDKRoots("go")
	want := testStagedSDKDir(t, configRoot, "go")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("staged SDK roots = %q, want only %q", got, want)
	}
}

// A dead env override (e.g. a PIG_SDK_GO_ROOT left over from a relocated
// checkout) must not be returned: the resolver falls through to the stable
// staged SDK instead of baking a nonexistent replace path into the generated
// manifest. This is the field bug where a moved repo broke every packed cell.

func TestFindSDKRootDeadEnvFallsThroughToStaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	staged := testStagedSDKDir(t, home, "go")
	stageSDKMarker(t, staged, "go.mod")
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(t.TempDir(), "gone"))

	got, err := findSDKRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != staged {
		t.Fatalf("dead PIG_SDK_GO_ROOT should fall through to staged SDK; got %q want %q", got, staged)
	}
}

func TestFindSDKRootValidEnvOverrideWins(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	override := t.TempDir()
	stageSDKMarker(t, override, "go.mod")
	t.Setenv("PIG_SDK_GO_ROOT", override)

	got, err := findSDKRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Fatalf("valid PIG_SDK_GO_ROOT should win; got %q want %q", got, override)
	}
}

func TestFindRustSDKRootDeadEnvFallsThroughToStaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	staged := testStagedSDKDir(t, home, "rust")
	stageSDKMarker(t, staged, "Cargo.toml")
	t.Setenv("PIG_SDK_RS_ROOT", filepath.Join(t.TempDir(), "gone"))

	got, err := findRustSDKRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != staged {
		t.Fatalf("dead PIG_SDK_RS_ROOT should fall through to staged SDK; got %q want %q", got, staged)
	}
}

func TestFindPythonSDKRootDeadEnvFallsThroughToStaged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	staged := testStagedSDKDir(t, home, "python")
	stageSDKMarker(t, staged, filepath.Join("pig_sdk", "__init__.py"))
	t.Setenv("PIG_SDK_PY_ROOT", filepath.Join(t.TempDir(), "gone"))

	got, err := findPythonSDKRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != staged {
		t.Fatalf("dead PIG_SDK_PY_ROOT should fall through to staged SDK; got %q want %q", got, staged)
	}
}
