//go:build !pig_strip_extension_sdk_go && !pig_strip_extension_sdk_rust && !pig_strip_extension_sdk_python

package pigsdk

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// treeState records every path under root with its size and modification time, so a test can prove an operation wrote nothing there.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		state[rel] = fmt.Sprint(info.Mode(), info.ModTime().UnixNano(), info.Size())
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return state
}

// installSDKs writes this binary's SDKs into a fresh directory and links it in as the installed SDK root for the test.
func installSDKs(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "share", "pig", "sdk")
	if _, err := install(dir); err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Cleanup(pigsdklock.SetInstalledSDKRootForTest(dir))
	return dir
}

// An installed binary reads each SDK from the installed root, under the name it would stage as, and every staging entry point only
// verifies it: no SDK copy appears under the config root and the installed root is never written.
func TestInstalledSDKsAreReadInPlaceAndNeverStaged(t *testing.T) {
	installed := installSDKs(t)
	configRoot := t.TempDir()
	before := treeState(t, installed)

	for _, b := range bundles() {
		dir, err := SDKDirFor(configRoot, b.lang)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := b.hash()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(installed, sdkBase(b.lang)+"-"+hash); dir != want {
			t.Fatalf("SDKDirFor(%s) = %s, want %s", b.lang, dir, want)
		}
		marker, err := os.ReadFile(filepath.Join(dir, markerFile))
		if err != nil || strings.TrimSpace(string(marker)) != hash {
			t.Fatalf("installed %s marker = %q, %v; want %s", b.lang, marker, err, hash)
		}
	}

	if err := EnsureSynced(configRoot); err != nil {
		t.Fatalf("EnsureSynced: %v", err)
	}
	for _, lang := range []string{"go", "python", "rust"} {
		if err := EnsureSyncedLang(configRoot, lang); err != nil {
			t.Fatalf("EnsureSyncedLang(%s): %v", lang, err)
		}
	}
	counts, _, err := Sync(configRoot, false)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("Sync staged %v, want nothing", counts)
	}
	staged, err := filepath.Glob(filepath.Join(configRoot, "state", "pigsdk", "sdk*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("config root holds staged SDKs %v (%v), want none", staged, err)
	}
	if after := treeState(t, installed); !maps.Equal(before, after) {
		t.Fatalf("installed root changed:\nbefore %v\nafter  %v", before, after)
	}

	statuses, err := Status(configRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range statuses {
		if !st.Installed || !st.Current || !strings.HasPrefix(st.Dir, installed) {
			t.Fatalf("Status(%s) = %+v, want current installed SDK under %s", st.Lang, st, installed)
		}
	}
	for _, buildType := range []string{"go", "rust"} {
		dir, err := SDKDirFor(configRoot, buildType)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyStaged(configRoot)(dir, buildType); err != nil {
			t.Fatalf("VerifyStaged(%s): %v", buildType, err)
		}
	}
}

// A missing or mismatched installed SDK is an installation fault. Every entry point reports it, names the installation rather than
// `pig reload`, and falls back to staging nothing.
func TestInstalledSDKMissingOrMismatchedIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, dir string)
		want   string
	}{
		{"missing directory", func(t *testing.T, dir string) {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}, "is missing at"},
		{"missing marker", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, markerFile)); err != nil {
				t.Fatal(err)
			}
		}, "has no .pig-sdk-version marker"},
		{"mismatched marker", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("0000000000000000\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "is 0000000000000000 but this pig embeds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installSDKs(t)
			configRoot := t.TempDir()
			goDir, err := SDKDirFor(configRoot, "go")
			if err != nil {
				t.Fatal(err)
			}
			tc.damage(t, goDir)

			for name, err := range map[string]error{
				"EnsureSynced":     EnsureSynced(configRoot),
				"EnsureSyncedLang": EnsureSyncedLang(configRoot, "go"),
				"VerifyStaged":     VerifyStaged(configRoot)(goDir, "go"),
				"CheckInstalled":   pigsdklock.CheckInstalledSDK("go"),
			} {
				if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "reinstall pig") || strings.Contains(err.Error(), "pig reload") {
					t.Fatalf("%s error = %v, want one containing %q that names the installation", name, err, tc.want)
				}
			}
			if _, _, err := Sync(configRoot, false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Sync error = %v, want %q", err, tc.want)
			}
			if err := EnsureSyncedLang(configRoot, "python"); err != nil {
				t.Fatalf("an intact installed Python SDK reports %v", err)
			}
			if staged, _ := filepath.Glob(filepath.Join(configRoot, "state", "pigsdk", "sdk*")); len(staged) != 0 {
				t.Fatalf("a broken installation staged %v under the config root", staged)
			}
		})
	}
}

// Extension builds compiled against an installed SDK stay reachable: a prune keeps a build whose fingerprint is the installed SDK's and
// drops one whose SDK is gone.
func TestInstalledSDKFingerprintsAreLive(t *testing.T) {
	installSDKs(t)
	configRoot := t.TempDir()
	live, err := LiveFingerprints(configRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles() {
		dir, err := SDKDirFor(configRoot, b.lang)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := subprocess.StagedSDKFingerprint(dir, b.lang)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := live[fp]; !ok {
			t.Fatalf("installed %s SDK fingerprint %s is not live: %v", b.lang, fp, live)
		}
	}
	if _, err := Prune(configRoot, false, true); err != nil {
		t.Fatalf("Prune on an installed binary with no staged SDK: %v", err)
	}
}

// `pig reload --install-sdks <dir>` writes the installation; `--sdk-path` then names the installed directory, and a second install into
// the same directory is refused rather than overwriting it.
func TestReloadInstallSDKsCommand(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "sdk")
	var stdout, stderr bytes.Buffer
	if code := RunCommand([]string{"reload", "--install-sdks", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("install exit %d: %s", code, stderr.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	for _, base := range []string{"sdk-", "sdk-py-", "sdk-rs-", "node-runtime-"} {
		if !slices.ContainsFunc(names, func(name string) bool {
			return strings.HasPrefix(name, base) && len(name) == len(base)+16
		}) {
			t.Fatalf("installed %v, missing %s<hash16>", names, base)
		}
	}
	if strings.Count(stdout.String(), "pig reload: installed ") != 4 {
		t.Fatalf("install output %q, want one line per installed directory", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunCommand([]string{"reload", "--install-sdks=" + dir}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("second install exit %d, stderr %q; want a refusal", code, stderr.String())
	}
	if code := RunCommand([]string{"reload", "--install-sdks"}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), "needs a directory") {
		t.Fatalf("install without a directory exit %d, stderr %q", code, stderr.String())
	}

	t.Cleanup(pigsdklock.SetInstalledSDKRootForTest(dir))
	stdout.Reset()
	stderr.Reset()
	if code := RunCommand([]string{"reload", "--sdk-path", "rust"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--sdk-path exit %d: %s", code, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); filepath.Dir(got) != dir || !strings.HasPrefix(filepath.Base(got), "sdk-rs-") {
		t.Fatalf("--sdk-path rust = %q, want the installed Rust SDK under %s", got, dir)
	}
	stdout.Reset()
	if code := RunCommand([]string{"reload"}, &stdout, &stderr); code != 0 {
		t.Fatalf("reload exit %d: %s", code, stderr.String())
	}
	if got := stdout.String(); strings.Count(got, "pig reload: using installed ") != 3 || strings.Contains(got, "staged") {
		t.Fatalf("reload output %q, want the three installed SDKs and nothing staged", got)
	}
}

// Every SDK resolver reports a broken installation instead of falling back to another copy of the SDK: the packed Go, Rust and Python
// cells and the isolated Go and Rust builds name the installation, and a mismatched Python SDK is never imported.
func TestInstalledSDKFaultsReachEveryResolver(t *testing.T) {
	ctx := t.Context()
	for _, tc := range []struct {
		lang  string
		build func(root string) error
	}{
		{"go", func(root string) error {
			writeFixture(t, root, "go.mod", "module example.com/broken\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n")
			writeFixture(t, root, "extension.go", "package broken\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"broken\") }\n")
			_, err := runtimecell.BuildGoPackedCell(ctx, t.TempDir(), "packed:go", []runtimecell.GoExtension{{Name: "broken", Root: root, ModulePath: "example.com/broken", Package: "example.com/broken", Factory: "Extension"}})
			return err
		}},
		{"rust", func(root string) error {
			writeFixture(t, root, "Cargo.toml", "[package]\nname = \"broken\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dependencies]\npig-sdk = \"0.1.0\"\n")
			writeFixture(t, root, "src/lib.rs", "pub fn new_extension() -> pig_sdk::Extension { pig_sdk::Extension::new(\"broken\") }\n")
			_, err := runtimecell.BuildRustPackedCell(ctx, t.TempDir(), "packed:rust", []runtimecell.RustExtension{{Name: "broken", Root: root, Package: "broken", Crate: "broken", Factory: "new_extension"}})
			return err
		}},
		{"python", func(root string) error {
			writeFixture(t, root, "broken.py", "import pig_sdk\n\ndef new_extension():\n    return pig_sdk.Extension(\"broken\")\n")
			_, err := runtimecell.BuildPythonPackedCell(ctx, t.TempDir(), "packed:python", []runtimecell.PythonExtension{{Name: "broken", Root: root, Package: "broken", Factory: "new_extension"}})
			return err
		}},
	} {
		for _, damage := range []string{"missing", "mismatched"} {
			t.Run(tc.lang+"/"+damage, func(t *testing.T) {
				installSDKs(t)
				dir, err := SDKDirFor(t.TempDir(), tc.lang)
				if err != nil {
					t.Fatal(err)
				}
				if damage == "missing" {
					err = os.RemoveAll(dir)
				} else {
					err = os.WriteFile(filepath.Join(dir, markerFile), []byte("0000000000000000\n"), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
				err = tc.build(t.TempDir())
				if err == nil || !strings.Contains(err.Error(), "reinstall pig") || strings.Contains(err.Error(), "PIG_SDK_") {
					t.Fatalf("packed %s build over a %s installed SDK: %v; want the installation named", tc.lang, damage, err)
				}
			})
		}
	}
}

func writeFixture(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
