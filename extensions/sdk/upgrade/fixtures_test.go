// SPDX-License-Identifier: MIT

package upgrade_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// sdkRoot is the SDK module the fixtures build against.
func sdkRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// stageFixture copies a fixture's input into a module that resolves the SDK
// from this checkout and returns its directory.
func stageFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join("testdata", "fixtures", name, "input")
	entries, err := os.ReadDir(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(input, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	goMod := "module example.com/" + strings.ReplaceAll(name, ".", "-") + "\n\ngo 1.26\n\nrequire " + upgrade.SDKModulePath + " v0.0.0\n\nreplace " + upgrade.SDKModulePath + " => " + filepath.ToSlash(sdkRoot(t)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// goEnv isolates the go command from the caller's workspace and caches.
func goEnv() []string {
	return append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")
}

func goBuild(t *testing.T, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = goEnv()
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func openFixture(t *testing.T, dir string) *upgrade.Session {
	t.Helper()
	session, err := upgrade.Open(context.Background(), upgrade.LoadOptions{Dir: dir, Roots: []string{dir}, Env: goEnv()})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func writeResult(t *testing.T, result *upgrade.Result) {
	t.Helper()
	for _, change := range result.Files {
		if err := os.WriteFile(change.Path, change.After, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Each fixture is an extension written for one past SDK shape. It must fail to
// build against this SDK, upgrade to the golden source, and then build.
func TestFixturesUpgradeAndBuild(t *testing.T) {
	for _, name := range []string{"sdk-dev", "sdk-0.2.0"} {
		t.Run(name, func(t *testing.T) {
			dir := stageFixture(t, name)
			if output, err := goBuild(t, dir); err == nil {
				t.Fatalf("the old extension builds against the current SDK:\n%s", output)
			}
			result, err := openFixture(t, dir).Plan()
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Remaining) != 0 || len(result.Skipped) != 0 || len(result.Errors) != 0 {
				t.Fatalf("remaining=%v skipped=%v errors=%v", result.Remaining, result.Skipped, result.Errors)
			}
			writeResult(t, result)
			for _, change := range result.Files {
				want, err := os.ReadFile(filepath.Join("testdata", "fixtures", name, "want", filepath.Base(change.Path)))
				if os.Getenv("PIG_UPDATE_GOLDEN") != "" {
					_ = os.MkdirAll(filepath.Join("testdata", "fixtures", name, "want"), 0o755)
					if err := os.WriteFile(filepath.Join("testdata", "fixtures", name, "want", filepath.Base(change.Path)), change.After, 0o600); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if string(change.After) != string(want) {
					t.Fatalf("%s differs from the golden source:\n%s", filepath.Base(change.Path), change.After)
				}
			}
			if output, err := goBuild(t, dir); err != nil {
				t.Fatalf("the upgraded extension does not build:\n%s", output)
			}
			again, err := openFixture(t, dir).Plan()
			if err != nil {
				t.Fatal(err)
			}
			if again.Changed() {
				t.Fatalf("a second upgrade changes the source again: %+v", again.Files)
			}
		})
	}
}

// An extension written for SDK 0.4.1 that already builds has nothing to upgrade.
func TestCurrentShapeNeedsNoUpgrade(t *testing.T) {
	dir := stageFixture(t, "sdk-0.4.1")
	if output, err := goBuild(t, dir); err != nil {
		t.Fatalf("the 0.4.1 extension does not build:\n%s", output)
	}
	result, err := openFixture(t, dir).Plan()
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed() || len(result.Remaining) != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
}
