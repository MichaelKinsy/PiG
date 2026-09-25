package pigletbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildReportsResolvingPhaseBeforeManifestFailure(t *testing.T) {
	var stdout, stderr strings.Builder
	code := runBuild([]string{filepath.Join(t.TempDir(), "missing.yaml"), "--format", "binary"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Resolving manifest") || !strings.Contains(stderr.String(), "hint:") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.ContainsAny(stderr.String(), "\x1b\r") {
		t.Fatalf("non-TTY escapes: %q", stderr.String())
	}
}

func assertBuildPhaseOrder(t *testing.T, output string, phases []string) {
	t.Helper()
	remaining := output
	for _, phase := range phases {
		_, next, ok := strings.Cut(remaining, phase)
		if !ok {
			t.Fatalf("missing or out-of-order phase %q in:\n%s", phase, output)
		}
		remaining = next
	}
	if strings.ContainsAny(output, "\x1b\r") {
		t.Fatalf("non-TTY escapes: %q", output)
	}
}

func TestBuildVerboseStreamsCompilerFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	path := writeSmallPiglet(t, root)
	source := filepath.Join(root, "hello", "broken.go")
	if err := os.WriteFile(source, []byte("package hello\nfunc broken() { missingBuildSymbol() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A Windows Piglet Binary must be named with .exe, as realbuild_test.go's
	// outputs are; the build refuses any other name there.
	out := filepath.Join(root, "pig-small")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	var stdout, stderr strings.Builder
	code := runBuild([]string{path, "--format", "binary", "--verbose", "--out", out}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit=%d: %s", code, &stderr)
	}
	assertBuildPhaseOrder(t, stderr.String(), []string{"Resolving manifest", "Compiling and linking Go binary", "[small]", "missingBuildSymbol", "failed", "hint:"})
	if strings.Contains(stderr.String(), "Built ") {
		t.Fatalf("premature success: %s", &stderr)
	}
}

func TestBuildJSONKeepsProgressOffStdout(t *testing.T) {
	var stdout, stderr strings.Builder
	code := runBuild([]string{filepath.Join(t.TempDir(), "missing.yaml"), "--format", "binary", "--json"}, &stdout, &stderr)
	var result pigletBuildOutput
	if err := json.Unmarshal([]byte(stdout.String()), &result); err != nil {
		t.Fatal(err)
	}
	if code != 1 || result.Success || !strings.Contains(result.Error, "Resolving manifest failed") {
		t.Fatalf("exit=%d result=%+v", code, result)
	}
	if !strings.Contains(stderr.String(), "Resolving manifest") {
		t.Fatalf("no live phase: %s", &stderr)
	}
}

func TestBuildAcceptsVerbose(t *testing.T) {
	if _, _, _, _, err := parseArgs([]string{"small", "--format", "binary", "--verbose"}); err != nil {
		t.Fatal(err)
	}
}
