package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a Binary without the Node runtime reports a TypeScript extension as
// stripped through Pi's extension load failure (exit 1 with the `-ne` hint),
// whether the extension is discovered or named with -e; without it the run
// completes with the faux model.
func TestStrippedBinaryReportsTypeScriptExtensionStripped(t *testing.T) {
	project := t.TempDir()
	extensions := filepath.Join(project, ".pig", "extensions")
	if err := os.MkdirAll(extensions, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensions, "hello.ts"), []byte("export default function (pi: any) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := pigstrip.Error("The TypeScript and JavaScript extension runtime", pigstrip.ListFeatures, pigstrip.NodeExtensions).Error()
	for _, args := range [][]string{{"--approve"}, {"-ne", "-e", "./.pig/extensions/hello.ts"}} {
		args = append(append([]string{"-p", "--model", "test-faux/faux-1"}, args...), "reply with exactly: done")
		stdout, stderr, code := runStrippedPig(t, project, args...)
		if code != 1 || !strings.Contains(stderr, `Failed to load extension "`) || !strings.Contains(stderr, want) || !strings.Contains(stderr, "-ne") {
			t.Fatalf("pig %v: exit %d\nstdout: %q\nstderr: %q\nwant a load failure naming %q", args, code, stdout, stderr, want)
		}
	}
	stdout, stderr, code := runStrippedPig(t, project, "-p", "--approve", "-ne", "--model", "test-faux/faux-1", "reply with exactly: done")
	if code != 0 || strings.TrimSpace(stdout) != "done" {
		t.Fatalf("pig -ne: exit %d\nstdout: %q\nstderr: %q", code, stdout, stderr)
	}
}

// D92: a Binary without the extension SDKs answers `pig reload --sdk-path
// <lang>` and `pig extension init --lang <lang>` with the strip, not an
// unknown language or a missing stage.
func TestStrippedBinaryReportsExtensionSDKsStripped(t *testing.T) {
	for _, tc := range []struct{ lang, what, id string }{
		{"go", "The Go extension SDK", pigstrip.ExtensionSDKGo},
		{"python", "The Python extension SDK", pigstrip.ExtensionSDKPython},
		{"rust", "The Rust extension SDK", pigstrip.ExtensionSDKRust},
	} {
		want := pigstrip.Error(tc.what, pigstrip.ListFeatures, tc.id).Error()
		stdout, stderr, code := runStrippedPig(t, "", "reload", "--sdk-path", tc.lang)
		if code != 1 || strings.TrimSpace(stderr) != "pig reload: "+want || stdout != "" {
			t.Fatalf("pig reload --sdk-path %s: exit %d\nstdout: %q\nstderr: %q", tc.lang, code, stdout, stderr)
		}
		project := filepath.Join(t.TempDir(), "hello")
		stdout, stderr, code = runStrippedPig(t, "", "extension", "init", "--lang", tc.lang, project)
		if code != 1 || !strings.Contains(stdout+stderr, "stage "+tc.lang+" SDK: "+want) {
			t.Fatalf("pig extension init --lang %s: exit %d\nstdout: %q\nstderr: %q", tc.lang, code, stdout, stderr)
		}
	}
}

// D92: the stripped Binary links none of the embedded SDK sources. The Go SDK
// package stays (fused extensions build on it), so the check is by symbol.
func TestStrippedBinaryOmitsExtensionSDKSources(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a stripped cmd/pig")
	}
	binary, err := strippedTestBinary()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("go", "tool", "nm", binary).Output()
	if err != nil {
		t.Fatalf("go tool nm: %v", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		for _, symbol := range []string{"/extensions/sdk.Source", "/extensions/sdk-py.Source", "/extensions/sdk-rs.Source", "klauspost/compress/zstd."} {
			if strings.Contains(line, symbol) {
				t.Errorf("stripped pig links %s", strings.TrimSpace(line))
			}
		}
	}
}
