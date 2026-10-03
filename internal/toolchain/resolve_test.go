//go:build unix

package toolchain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeGoScript imitates the parts of the go command that decide which GOROOT
// compiles a build: `env GOROOT` reports $GOROOT or the install the binary
// lives in, and `build` fails the way a compiler from another release does.
const fakeGoScript = `#!/bin/sh
self=$(cd "${0%/*}/.." && pwd -P)
if [ -n "$FAKE_GO_PROBE_LOG" ]; then echo "$@" >> "$FAKE_GO_PROBE_LOG"; fi
case "$1" in
env) echo "${GOROOT:-$self}" ;;
build)
  if [ -n "$GOROOT" ] && [ "$GOROOT" != "$self" ]; then
    echo '# runtime'
    read -r have < "$GOROOT/VERSION"
    read -r want < "$self/VERSION"
    echo "compile: version \"$have\" does not match go tool version \"$want\""
    exit 1
  fi ;;
esac
`

// fakeGoRoot builds an install laid out like a real one: bin/go beside pkg/tool.
func fakeGoRoot(t *testing.T, version string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"bin", filepath.Join("pkg", "tool")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte(version), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte(fakeGoScript), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func selectGo(t *testing.T, pathDir, goroot string) {
	t.Helper()
	t.Setenv("PATH", pathDir)
	t.Setenv("GOROOT", goroot)
	t.Setenv("GOTOOLCHAIN", "")
	t.Setenv("PIG_HOME", t.TempDir())
}

func envValue(env []string, key string) (string, int) {
	value, count := "", 0
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			value, count = v, count+1
		}
	}
	return value, count
}

// An inherited GOROOT that belongs to another install must not reach a build:
// the go command would run that install's compiler and refuse it.
func TestResolveGoReplacesInheritedGOROOTOfAnotherInstall(t *testing.T) {
	onPath := fakeGoRoot(t, "go1.26.1")
	inherited := fakeGoRoot(t, "go1.26.7")
	selectGo(t, filepath.Join(onPath, "bin"), inherited)

	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Root != onPath || tc.Command != filepath.Join(onPath, "bin", "go") {
		t.Fatalf("toolchain = %+v, want the install on PATH %s", tc, onPath)
	}
	env := tc.Environ(os.Environ())
	if got, n := envValue(env, "GOROOT"); got != onPath || n != 1 {
		t.Fatalf("GOROOT = %q (%d entries), want %q once", got, n, onPath)
	}
	cmd := exec.Command(tc.Command, "build")
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build with the coherent environment failed: %v\n%s", err, out)
	}
	// The uncorrected environment is the reported failure.
	cmd = exec.Command(tc.Command, "build")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "does not match go tool version") {
		t.Fatalf("inherited environment should reproduce the mismatch, got err=%v\n%s", err, out)
	}
}

// A shim (mise, asdf) picks its Go by working directory, so it must be
// resolved once to a real install and that install used for every build.
func TestResolveGoResolvesShimToInstall(t *testing.T) {
	real := fakeGoRoot(t, "go1.26.1")
	shims := t.TempDir()
	shim := "#!/bin/sh\nexec " + filepath.Join(real, "bin", "go") + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shims, "go"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	selectGo(t, shims, fakeGoRoot(t, "go1.26.7"))

	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Root != real || tc.Command != filepath.Join(real, "bin", "go") {
		t.Fatalf("toolchain = %+v, want the install behind the shim %s", tc, real)
	}
}

// A go command inside a recognisable install is resolved without running it.
func TestResolveGoDoesNotSpawnForARecognisableInstall(t *testing.T) {
	root := fakeGoRoot(t, "go1.26.1")
	selectGo(t, filepath.Join(root, "bin"), "")
	log := filepath.Join(t.TempDir(), "probe.log")
	t.Setenv("FAKE_GO_PROBE_LOG", log)

	if _, err := ResolveGo(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(log); err == nil {
		t.Fatalf("go was spawned to resolve an ordinary install: %s", data)
	}
}

// A distribution binary (/usr/bin/go) does not sit inside its GOROOT, so the
// go command itself is the authority on where its root is.
func TestResolveGoAsksGoForRootOutsideItsInstall(t *testing.T) {
	root := fakeGoRoot(t, "go1.26.1")
	bin := t.TempDir()
	// The install layout is bin/go beside root/, so bin/.. is not a GOROOT.
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\ncase \"$1\" in env) echo "+root+" ;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	selectGo(t, bin, fakeGoRoot(t, "go1.26.7"))

	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Root != root {
		t.Fatalf("Root = %q, want %q reported by the command", tc.Root, root)
	}
	if got, _ := envValue(tc.Environ(os.Environ()), "GOROOT"); got != root {
		t.Fatalf("GOROOT = %q, want %q", got, root)
	}
}

func TestResolveGoKeepsAMatchingGOROOT(t *testing.T) {
	root := fakeGoRoot(t, "go1.26.1")
	selectGo(t, filepath.Join(root, "bin"), root)
	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if got, n := envValue(tc.Environ(os.Environ()), "GOROOT"); got != root || n != 1 {
		t.Fatalf("GOROOT = %q (%d entries), want %q once", got, n, root)
	}
}

func TestResolveGoWithoutGoIsTheSetupError(t *testing.T) {
	selectGo(t, t.TempDir(), "")
	if _, err := ResolveGo(); err == nil || !strings.Contains(err.Error(), "pig setup go") {
		t.Fatalf("err = %v, want the pig setup go remedy", err)
	}
}

func TestReleaseMismatchNamesBothReleasesAndTheFix(t *testing.T) {
	tc := GoToolchain{Command: "/opt/go1.26.1/bin/go", Root: "/opt/go1.26.1"}
	out := []byte("# internal/goarch\ncompile: version \"go1.26.7\" does not match go tool version \"go1.26.1\"\n# runtime\ncompile: version \"go1.26.7\" does not match go tool version \"go1.26.1\"\n")
	line, ok := tc.ReleaseMismatch(out)
	if !ok || strings.Contains(line, "\n") {
		t.Fatalf("ReleaseMismatch = %q, %v; want one line", line, ok)
	}
	for _, want := range []string{"go1.26.7", "go1.26.1", "/opt/go1.26.1/bin/go", "reinstall"} {
		if !strings.Contains(line, want) {
			t.Fatalf("%q lacks %q", line, want)
		}
	}
	if _, ok := tc.ReleaseMismatch([]byte("# x\nmain.go:1:1: syntax error")); ok {
		t.Fatal("an ordinary compile error is not a release mismatch")
	}
}

// Sixteen cells resolving the toolchain at once ask a shim once and all get the
// same installation.
func TestResolveGoIsOnceForConcurrentBuilds(t *testing.T) {
	real := fakeGoRoot(t, "go1.26.1")
	shims := t.TempDir()
	log := filepath.Join(t.TempDir(), "asked.log")
	shim := "#!/bin/sh\necho asked >> " + log + "\nexec " + filepath.Join(real, "bin", "go") + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shims, "go"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	selectGo(t, shims, "")
	results := make([]GoToolchain, 64)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() {
			tc, err := ResolveGo()
			if err != nil {
				t.Error(err)
			}
			results[i] = tc
		})
	}
	wg.Wait()
	for i, tc := range results {
		if tc.Root != real || tc != results[0] {
			t.Fatalf("result %d = %+v, want the install %s", i, tc, real)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Count(string(data), "asked") != 1 {
		t.Fatalf("shim asked %q (%v), want once", data, err)
	}
}

// A go command that cannot say where its root is is still located, and no
// inherited GOROOT reaches it: the build then reports the command's own failure.
func TestResolveGoKeepsACommandThatCannotReportItsRoot(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\necho broken installation >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	selectGo(t, bin, fakeGoRoot(t, "go1.26.7"))
	tc, err := ResolveGo()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Command != filepath.Join(bin, "go") || tc.Root != "" {
		t.Fatalf("toolchain = %+v", tc)
	}
	if _, n := envValue(tc.Environ(os.Environ()), "GOROOT"); n != 0 {
		t.Fatalf("GOROOT still inherited (%d entries)", n)
	}
}
