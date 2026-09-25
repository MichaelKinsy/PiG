package runtimecell

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// An installed pig (running outside its own source tree, no env override, and
// no replace in the extension's go.mod) must still resolve the SDK from the
// source tree it staged at install time.
func TestFindSDKRoot_InstalledSourceRoot(t *testing.T) {
	// Run outside any pig checkout so the cwd-walk strategy cannot fire.
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("PIG_SDK_GO_ROOT", "")

	srcRoot := filepath.Join(tmp, "pig-source")
	sdk := filepath.Join(srcRoot, "extensions", "sdk")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SOURCE_ROOT", srcRoot)

	got, err := findSDKRoot(nil)
	if err != nil {
		t.Fatalf("findSDKRoot: %v", err)
	}
	if got != sdk {
		t.Fatalf("findSDKRoot = %q, want install-relative %q", got, sdk)
	}
}

// Without any resolvable source root, findSDKRoot reports an actionable error
// rather than returning a bogus path.
func TestFindSDKRoot_NoSourceRootErrors(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", filepath.Join(tmp, "home"))
	t.Setenv("PIG_SDK_GO_ROOT", "")
	t.Setenv("PIG_SOURCE_ROOT", filepath.Join(tmp, "does-not-exist"))

	if _, err := findSDKRoot(nil); err == nil {
		t.Fatal("expected error when no SDK source root is resolvable")
	}
}

func TestFindSDKRoot_ExplicitExtensionOverrideWins(t *testing.T) {
	t.Setenv("PIG_SDK_GO_ROOT", "")
	root := t.TempDir()
	sdk := filepath.Join(t.TempDir(), "author-sdk")
	if err := os.MkdirAll(sdk, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/ext\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + modfile.AutoQuote(filepath.ToSlash(sdk)) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "state", "pigsdk", "sdk")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", filepath.Dir(filepath.Dir(filepath.Dir(staged))))

	got, err := findSDKRoot([]GoExtension{{Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if got != sdk {
		t.Fatalf("findSDKRoot = %q, want explicit override %q", got, sdk)
	}
}

func TestFindSDKRoot_DefaultUsesRunningConfigStage(t *testing.T) {
	t.Setenv("PIG_SDK_GO_ROOT", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/ext\ngo 1.26\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pigHome := t.TempDir()
	staged := filepath.Join(pigHome, "state", "pigsdk", "sdk")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "go.mod"), []byte("module github.com/MichaelKinsy/PiG/extensions/sdk\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", pigHome)

	got, err := findSDKRoot([]GoExtension{{Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	if got != staged {
		t.Fatalf("findSDKRoot = %q, want staged SDK %q", got, staged)
	}
}

func TestFindRustSDKRootExplicitOverrideWins(t *testing.T) {
	t.Setenv("PIG_SDK_RS_ROOT", "")
	extRoot := t.TempDir()
	sdkRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(sdkRoot, "Cargo.toml"), []byte("[package]\nname='pig-sdk'\nversion='0.1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cargo := "[package]\nname='ext'\nversion='0.1.0'\n[dependencies]\npig-sdk={path='" + filepath.ToSlash(sdkRoot) + "'}\n"
	if err := os.WriteFile(filepath.Join(extRoot, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findRustSDKRoot([]RustExtension{{Root: extRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if got != sdkRoot {
		t.Fatalf("findRustSDKRoot = %q, want %q", got, sdkRoot)
	}
}

func TestFindPythonSDKRootUsesRunningConfigStage(t *testing.T) {
	t.Setenv("PIG_SDK_PY_ROOT", "")
	pigHome := t.TempDir()
	staged := filepath.Join(pigHome, "state", "pigsdk", "sdk-py", "pig_sdk")
	if err := os.MkdirAll(staged, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "__init__.py"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", pigHome)
	got, err := findPythonSDKRoot()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Dir(staged)
	if got != want {
		t.Fatalf("findPythonSDKRoot = %q, want %q", got, want)
	}
}
