package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The grouped verifier must prebuild the same source each consumer builds
// directly: host integration and cross-SDK conformance use different fixtures.
func TestFixtureBuildsExportDistinctGoSDKConsumers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("builds the Linux CI images with Linux tools; it runs on the Linux CI hosts")
	}
	root := repoRoot(t)
	bin := t.TempDir()
	out := filepath.Join(t.TempDir(), "fixture output")
	t.Setenv("PIG_TEST_FIXTURE_DIR", out)
	t.Setenv("CARGO_TARGET_DIR", filepath.Join(t.TempDir(), "cargo target"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for name, script := range map[string]string{
		"go": `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == build && "$2" == -o && "$4" == . ]]
printf '%s\n' "$PWD" > "$3"
`,
		"cargo": `#!/usr/bin/env bash
set -euo pipefail
mkdir -p "$CARGO_TARGET_DIR/release"
printf 'rust fixture\n' > "$CARGO_TARGET_DIR/release/rust-sdk-fixture"
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(t.Context(), testenv.Bash(t), "-c", `
set -euo pipefail
exports=$(./automation/ci/test-fixtures.sh --exports)
eval "$exports"
printf '%s\n' "$PIG_TEST_SDK_FIXTURE_BIN" "${PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN:-}"
`)
	cmd.Dir = root
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("build fixture exports: %v\n%s", err, output)
	}
	paths := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	wantSources := []string{
		"coding/extension/host/subprocess/testdata/sdk-fixture",
		"tests/extension-conformance/testfixture/cmd",
	}
	if len(paths) != len(wantSources) {
		t.Fatalf("exported fixture paths = %q, want one per source %q", paths, wantSources)
	}
	if paths[0] == paths[1] {
		t.Fatalf("host and conformance fixtures share output %q", paths[0])
	}
	for i, source := range wantSources {
		data, err := os.ReadFile(paths[i])
		if err != nil {
			t.Fatalf("fixture for %s: %v", source, err)
		}
		if got, want := strings.TrimSpace(string(data)), filepath.Join(root, source); got != want {
			t.Errorf("fixture %q built from %q, want %q", paths[i], got, want)
		}
	}
}
