package runtimecell

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func requireGoPackedHash(t *testing.T, key string, extensions []GoExtension, sdkRoot string) string {
	t.Helper()
	hash := goPackedCellHash(t.TempDir(), key, extensions, sdkRoot)
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

func requireRustPackedHash(t *testing.T, key string, extensions []RustExtension, sdkRoot string) string {
	t.Helper()
	hash := rustPackedCellHash(t.TempDir(), key, extensions, sdkRoot)
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

func requirePythonPackedHash(t *testing.T, key string, extensions []PythonExtension, sdkRoot string) string {
	t.Helper()
	hash := pythonPackedCellHash(t.TempDir(), key, extensions, sdkRoot)
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

func requireTreeHash(t *testing.T, root string) string {
	t.Helper()
	hash := hashTree(root)
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

func TestGoPackedCellHashFollowsSymlinkedSDKRoot(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sdk")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(target, "protocol.go")
	if err := os.WriteFile(source, []byte("package sdk\nconst protocol = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absoluteLink := filepath.Join(base, "sdk-absolute")
	testenv.Symlink(t, target, absoluteLink)
	relativeLink := filepath.Join(base, "sdk-relative")
	testenv.Symlink(t, "sdk", relativeLink)
	chainedLink := filepath.Join(base, "sdk-chained")
	testenv.Symlink(t, "sdk-relative", chainedLink)
	extensions := []GoExtension{{
		Name:       "ask",
		ModulePath: "example.com/ask",
		Package:    "example.com/ask",
		Factory:    "Extension",
		Hash:       "source",
	}}
	links := map[string]string{
		"absolute": absoluteLink,
		"relative": relativeLink,
		"chained":  chainedLink,
	}

	firstTarget := requireGoPackedHash(t, "packed-go", extensions, target)
	firstLinks := make(map[string]string, len(links))
	for name, link := range links {
		firstLinks[name] = requireGoPackedHash(t, "packed-go", extensions, link)
		if firstLinks[name] != firstTarget {
			t.Fatalf("%s symlinked SDK cell hash %q differs from target cell hash %q", name, firstLinks[name], firstTarget)
		}
	}
	if err := os.WriteFile(source, []byte("package sdk\nconst protocol = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondTarget := requireGoPackedHash(t, "packed-go", extensions, target)
	for name, link := range links {
		second := requireGoPackedHash(t, "packed-go", extensions, link)
		if second == firstLinks[name] {
			t.Fatalf("%s symlinked SDK cell hash did not change after target source changed: %q", name, second)
		}
		if second != secondTarget {
			t.Fatalf("updated %s symlinked SDK cell hash %q differs from target cell hash %q", name, second, secondTarget)
		}
	}
}

func TestPackedCellHashesTrackSymlinkedSDKChanges(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "sdk")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(target, "protocol.source")
	if err := os.WriteFile(source, []byte("sdk-source-a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "sdk-link")
	testenv.Symlink(t, "sdk", link)
	hashers := map[string]func(string) string{
		"go": func(sdkRoot string) string {
			return requireGoPackedHash(t, "packed-go", []GoExtension{{Name: "go", ModulePath: "example.com/go", Package: "example.com/go", Factory: "Extension", Hash: "source"}}, sdkRoot)
		},
		"rust": func(sdkRoot string) string {
			return requireRustPackedHash(t, "packed-rust", []RustExtension{{Name: "rust", Package: "rust", Crate: "rust", Factory: "new_extension", Hash: "source"}}, sdkRoot)
		},
		"python": func(sdkRoot string) string {
			return requirePythonPackedHash(t, "packed-python", []PythonExtension{{Name: "python", Package: "python", Factory: "new_extension", Hash: "source"}}, sdkRoot)
		},
	}
	first := make(map[string]string, len(hashers))
	for language, hash := range hashers {
		first[language] = hash(link)
	}
	if err := os.WriteFile(source, []byte("protocol-v2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for language, hash := range hashers {
		if second := hash(link); second == first[language] {
			t.Errorf("%s packed cell hash did not change after symlinked SDK source changed: %q", language, second)
		}
	}
}

func TestPackedCellHashesTrackSDKLocation(t *testing.T) {
	firstRoot := filepath.Join(t.TempDir(), "sdk")
	secondRoot := filepath.Join(t.TempDir(), "sdk")
	for _, root := range []string{firstRoot, secondRoot} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "protocol.source"), []byte("same-protocol\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hashers := map[string]func(string) string{
		"go": func(sdkRoot string) string {
			return requireGoPackedHash(t, "packed-go", []GoExtension{{Name: "go", ModulePath: "example.com/go", Package: "example.com/go", Factory: "Extension", Hash: "source"}}, sdkRoot)
		},
		"rust": func(sdkRoot string) string {
			return requireRustPackedHash(t, "packed-rust", []RustExtension{{Name: "rust", Package: "rust", Crate: "rust", Factory: "new_extension", Hash: "source"}}, sdkRoot)
		},
		"python": func(sdkRoot string) string {
			return requirePythonPackedHash(t, "packed-python", []PythonExtension{{Name: "python", Package: "python", Factory: "new_extension", Hash: "source"}}, sdkRoot)
		},
	}
	for language, hash := range hashers {
		if first, second := hash(firstRoot), hash(secondRoot); first == second {
			t.Errorf("%s packed cell hash did not change after SDK relocation: %q", language, first)
		}
	}
}

func TestHashTreeRejectsBrokenAndCyclicRootSymlinks(t *testing.T) {
	base := t.TempDir()
	broken := filepath.Join(base, "broken")
	testenv.Symlink(t, "missing", broken)
	cycleA := filepath.Join(base, "cycle-a")
	cycleB := filepath.Join(base, "cycle-b")
	testenv.Symlink(t, "cycle-b", cycleA)
	testenv.Symlink(t, "cycle-a", cycleB)
	for name, root := range map[string]string{"broken": broken, "cyclic": cycleA} {
		if got := hashTree(root); !strings.HasPrefix(got, "error:") {
			t.Errorf("%s root hash = %q, want an error identity", name, got)
		}
	}
}

func TestHashTreeFailsClosedOnUnreadableSubtree(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission denial requires a non-root Unix user")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "a-blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "source.go"), []byte("package blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })
	if got := hashTree(root); !strings.HasPrefix(got, "error:") {
		t.Fatalf("unreadable subtree produced reusable hash %q", got)
	}
}

func TestPackedCellHashDoesNotReuseKeyWhenSDKTraversalFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission denial requires a non-root Unix user")
	}
	root := t.TempDir()
	blocked := filepath.Join(root, "a-blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "source.go"), []byte("package blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o700) })

	hash := goPackedCellHash(t.TempDir(), "packed-go", []GoExtension{{
		Name: "extension", ModulePath: "example.com/extension", Package: "example.com/extension", Factory: "Extension", Hash: "source",
	}}, root)
	if !strings.HasPrefix(hash, "error:") {
		t.Fatalf("packed cell traversal failure produced reusable hash %q", hash)
	}
}

func TestHashTreeHashesHiddenAdmittedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".sdk")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "sdk.go")
	if err := os.WriteFile(source, []byte("package sdk\nconst version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := requireTreeHash(t, root)
	if err := os.WriteFile(source, []byte("package sdk\nconst version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := requireTreeHash(t, root); after == before {
		t.Fatalf("hidden admitted root kept stale hash %q", after)
	}
}

func TestHashTreeDoesNotTraverseNestedDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sdk.go"), []byte("package sdk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsideSource := filepath.Join(outside, "outside.go")
	if err := os.WriteFile(outsideSource, []byte("package outside\nconst version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, outside, filepath.Join(root, "nested"))
	before := requireTreeHash(t, root)
	if err := os.WriteFile(outsideSource, []byte("package outside\nconst version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := requireTreeHash(t, root); after != before {
		t.Fatalf("nested directory symlink changed tree hash: before=%q after=%q", before, after)
	}
}

func TestCommandVersionProbeHelper(t *testing.T) {
	logPath := os.Getenv("PIG_TEST_TOOLCHAIN_PROBE_LOG")
	if logPath == "" {
		return
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(logFile, "probe"); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(os.Stdout, os.Getenv("PIG_TEST_TOOLCHAIN_VERSION")); err != nil {
		t.Fatal(err)
	}
}

// An unchanged warm startup reads the durable version record without spawning
// the tool. Replacing the selected executable changes its file identity and
// forces a new probe; a failed build can explicitly invalidate the same record.
func TestCommandVersionCachesWarmProbeAndDetectsReplacement(t *testing.T) {
	cacheRoot := t.TempDir()
	binDir := t.TempDir()

	logPath := filepath.Join(t.TempDir(), "probes.log")
	t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", logPath)
	args := []string{"-test.run=^TestCommandVersionProbeHelper$"}
	tools := []string{"go", "rustc", "cargo", "python3"}
	paths := make(map[string]string, len(tools))
	outputs := make(map[string]string, len(tools))
	for _, name := range tools {
		toolPath := filepath.Join(binDir, exeNameForRuntime(name))
		copyTestExecutable(t, toolPath)
		paths[name] = toolPath
		t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", name+"-v1")
		first := commandVersion(cacheRoot, toolPath, args...)
		second := commandVersion(cacheRoot, toolPath, args...)
		if !strings.Contains(first, name+"-v1") || second != first {
			t.Fatalf("%s warm outputs = %q, %q", name, first, second)
		}
		outputs[name] = first
	}
	if probes := probeCount(t, logPath); probes != len(tools) {
		t.Fatalf("warm probes = %d, want %d cold probes and 0 warm probes", probes, len(tools))
	}

	goPath := paths["go"]
	copyTestExecutable(t, goPath)
	t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", "go-v2")
	replaced := commandVersion(cacheRoot, goPath, args...)
	if !strings.Contains(replaced, "go-v2") || replaced == outputs["go"] {
		t.Fatalf("replacement output = %q, first = %q", replaced, outputs["go"])
	}
	if probes := probeCount(t, logPath); probes != len(tools)+1 {
		t.Fatalf("replacement probes = %d, want %d", probes, len(tools)+1)
	}

	invalidateCommandVersion(cacheRoot, goPath, args...)
	if got := commandVersion(cacheRoot, goPath, args...); got != replaced {
		t.Fatalf("re-probed output = %q, want %q", got, replaced)
	}
	if probes := probeCount(t, logPath); probes != len(tools)+2 {
		t.Fatalf("post-failure probes = %d, want %d", probes, len(tools)+2)
	}
}

func probeCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "probe\n")
}
