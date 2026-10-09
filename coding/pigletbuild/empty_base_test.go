package pigletbuild

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// emptyBasePiglet lists no extensions and no frontend: it only strips
// built-ins and sets a default model, as a base tier does.
const emptyBasePiglet = "name: base\nmodel:\n  provider: test-faux\n  name: faux-1\nstrip:\n  tools: [grep, find, ls]\n  commands: [/share]\n"

// buildNativeBinary runs a real `pig piglet build --format binary` of
// pigletPath with HOME and PIG_HOME isolated under root and returns the
// artifact path.
func buildNativeBinary(t *testing.T, root, pigletPath, name string, extraArgs ...string) string {
	t.Helper()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	// Go makes downloaded module-cache files read-only by default, which would
	// prevent testing.TempDir from removing this isolated HOME on Unix.
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	artifact := filepath.Join(root, name)
	args := append([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", artifact}, extraArgs...)
	var stdout, stderr strings.Builder
	if code := runBuild(args, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	return artifact
}

// A Piglet that lists no extensions builds a signed Binary of Stock PiG's own
// parts. The Binary verifies, answers a print-mode prompt with the baked
// default model, and applies the baked strip list.
func TestEmptyBasePigletBuildsASignedBinary(t *testing.T) {
	root := t.TempDir()
	pigletPath := filepath.Join(root, "base.yaml")
	if err := os.WriteFile(pigletPath, []byte(emptyBasePiglet), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "author.key")
	keyID, err := signature.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	artifact := buildNativeBinary(t, root, pigletPath, "pig-base", "--sign-key", keyPath)

	var stdout, stderr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "verify", artifact}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Signature: signed by "+keyID) {
		t.Fatalf("pig piglet verify: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	if output := fauxPrompt(t, artifact); output != "42" {
		t.Fatalf("empty base -p printed %q, want 42 from the baked default model", output)
	}
	code, output := startPigletBinary(t, artifact, "--help")
	if code != 0 || !strings.Contains(output, "  read       - ") || strings.Contains(output, "  grep       - ") || strings.Contains(output, "  find       - ") {
		t.Fatalf("empty base --help does not apply the baked strip list: exit %d\n%s", code, output)
	}
}

// fauxPrompt runs `-p "What is 20+22?"` in path against the test-faux
// provider, offline, and returns the trimmed output.
func fauxPrompt(t *testing.T, path string) string {
	t.Helper()
	t.Setenv("PIG_TEST_FAUX", "1")
	t.Setenv("PIG_OFFLINE", "1")
	code, output := startPigletBinary(t, path, "--no-session", "-p", "What is 20+22?")
	if code != 0 {
		t.Fatalf("%s -p: exit %d\n%s", filepath.Base(path), code, output)
	}
	return strings.TrimSpace(output)
}

// A child of an empty base that adds an extension builds, and its Binary
// carries the extension and the strip list it inherits.
func TestEmptyBaseChildWithAnExtensionBuilds(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "base.yaml"), []byte(emptyBasePiglet), 0o644); err != nil {
		t.Fatal(err)
	}
	small := writeSmallPiglet(t, root)
	child := filepath.Join(root, "child.yaml")
	if err := os.WriteFile(child, []byte("name: child\nextends:\n  source: local:./base.yaml\nextensions:\n  - name: hello\n    origins: [local:./hello]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(small); err != nil {
		t.Fatal(err)
	}
	artifact := buildNativeBinary(t, root, child, "pig-child")
	binary, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{componentMarker, `{"kind":"piglet-resolution","piglet":"child"`} {
		if !strings.Contains(string(binary), want) {
			t.Fatalf("child Binary lacks %q", want)
		}
	}
	if output := fauxPrompt(t, artifact); output != "42" {
		t.Fatalf("child -p printed %q, want 42 from the inherited default model", output)
	}
	code, output := startPigletBinary(t, artifact, "--help")
	if code != 0 || strings.Contains(output, "  grep       - ") {
		t.Fatalf("child --help does not apply the inherited strip list: exit %d\n%s", code, output)
	}
}

// pig piglet publish decides "lists none" from the effective Piglet as build
// does: an empty base passes the build verdict, and a Piglet whose listed
// entry has no origin (so resolution yields no cell and no warning) is still
// refused with "piglet resolves to no extensions" before any build runs.
func TestPublishVerdictCountsTheListedExtensions(t *testing.T) {
	dir := t.TempDir()
	noBuilders := func() ([]BuilderBackend, error) { return nil, nil }
	for _, tc := range []struct {
		name, source, wantErr string
	}{
		{"empty base", emptyBasePiglet, ""},
		{"listed without origin", "name: scoped\nextensions: [gone]\n", "Piglet will not build: piglet resolves to no extensions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".yaml")
			if err := os.WriteFile(path, []byte(tc.source), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := loadPiglet(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = publishRelease{pigletRef: path, piglet: p}.prepareBuilds(context.Background(), io.Discard, io.Discard, noBuilders)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("prepareBuilds: %v", err)
			}
			if tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr) {
				t.Fatalf("prepareBuilds error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
