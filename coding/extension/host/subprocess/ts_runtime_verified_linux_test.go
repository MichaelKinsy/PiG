//go:build linux

package subprocess

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// envSelfDigest makes a copy of this test binary print what it is, instead of running tests, so a test can see which file a start ran.
// The value "hold" makes it wait for its standard input to close after printing, so that a test can read what the kernel shows of the
// running process.
const envSelfDigest = "PIG_SUBPROCESS_TEST_SELF_DIGEST"

// envFakeHost makes a copy of this test binary behave as a fake slot runtime host named by its value, as the shell scripts of the
// other fake runtimes do: it answers --version, logs its start in $PIG_FAKE_LOG, and runs the tree's script on Node. A script cannot
// be started from a descriptor, so the tests that count what the launch holds open need an executable program.
const envFakeHost = "PIG_SUBPROCESS_TEST_FAKE_HOST"

func init() {
	if id := os.Getenv(envFakeHost); id != "" {
		runFakeHost(id)
	}
	mode := os.Getenv(envSelfDigest)
	if mode != "1" && mode != "hold" {
		return
	}
	data, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sum := sha256.Sum256(data)
	fmt.Println(hex.EncodeToString(sum[:]), os.Args[0], strings.Join(os.Args[1:], " "))
	if mode == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
	os.Exit(0)
}

func runFakeHost(id string) {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(2)
	}
	if args[0] == "--version" {
		fmt.Println("fake-1")
		os.Exit(0)
	}
	if log := os.Getenv("PIG_FAKE_LOG"); log != "" {
		if file, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = file.WriteString(id + "\n")
			_ = file.Close()
		}
	}
	mode, tree := args[0], os.Getenv("PIG_TS_RUNTIME_TREE")
	node, err := exec.LookPath("node")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	argv := append([]string{"node", "--import", filepath.Join(tree, "register-loader.mjs"), filepath.Join(tree, mode+".mjs")}, args[1:]...)
	env := append(os.Environ(), "PIG_FAKE_RUNTIME="+id, "PIG_FAKE_MODE="+mode)
	fmt.Fprintln(os.Stderr, syscall.Exec(node, argv, env))
	os.Exit(2)
}

func writeSelfCopy(t *testing.T, path, marker string) string {
	t.Helper()
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, marker...)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The process a launch starts runs the bytes that were verified, whatever is at the executable's path by then: the command runs the
// verified descriptor and keeps the path as argv[0].
func TestTypeScriptRuntimeLaunchRunsTheVerifiedFileAfterTheSwap(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	root := t.TempDir()
	dir := filepath.Join(root, "aot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "pig-ts-host")
	verified := writeSelfCopy(t, exe, "A")
	manifest := tsruntime.Manifest{ID: "aot", Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Image: &tsruntime.Image{Executable: "pig-ts-host", SHA256: verified}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sel := tsruntime.NewResolver(root, tsruntime.NewVerifier(nil)).Resolve(t.Context(), tsruntime.Request{ID: "aot", Source: tsruntime.SourceEnv}, tsruntime.Set{})
	if sel.Runtime != "aot" || sel.Verified == nil {
		t.Fatalf("selection %+v (fallback %+v), want the verified slot executable", sel, sel.Fallback)
	}
	defer releaseVerified(sel)

	replacement := filepath.Join(dir, "replacement")
	swapped := writeSelfCopy(t, replacement, "B")
	if err := os.Rename(replacement, exe); err != nil {
		t.Fatal(err)
	}
	cmd := typeScriptRuntimeCommand(t.Context(), sel, filepath.Join(root, "tree"), "cli.mjs", "node extension loader", []string{"/a.ts"})
	if target, err := os.Readlink(cmd.Path); err != nil || !strings.HasPrefix(target, "/proc/self/fd/") || filepath.Base(cmd.Path) != "pig-ts-host" || cmd.Args[0] != exe {
		t.Fatalf("command runs %q (-> %q, %v) with argv[0] %q, want a link named pig-ts-host to the verified descriptor with argv[0] %s", cmd.Path, target, err, cmd.Args[0], exe)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	fields := strings.Fields(string(out))
	releaseVerified(sel)
	if _, err := os.Lstat(filepath.Dir(cmd.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link directory survived the release: %v", err)
	}
	if len(fields) != 4 || fields[0] != verified || fields[1] != exe || fields[2] != "cli" || fields[3] != "/a.ts" {
		t.Fatalf("the process printed %q; want digest %s (not the swapped %s), argv[0] %s and the arguments cli /a.ts", out, verified, swapped, exe)
	}
	// The launch released the descriptor once the process started; a second command from the same selection is refused rather than
	// naming whatever file now holds that descriptor number.
	if again := typeScriptRuntimeCommand(t.Context(), sel, filepath.Join(root, "tree"), "cli.mjs", "node extension loader", nil); again.Err == nil {
		t.Fatalf("a command from a released selection runs %q", again.Path)
	}
}

// The process a launch starts is named after the executable: comm is the executable's base name, not the descriptor number.
func TestTypeScriptRuntimeLaunchNamesTheProcessAfterTheExecutable(t *testing.T) {
	t.Setenv(envSelfDigest, "hold")
	root := t.TempDir()
	dir := filepath.Join(root, "aot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "pig-ts-host")
	digest := writeSelfCopy(t, exe, "A")
	installExecutableManifest(t, dir, digest)
	// The probe waits on its standard input in hold mode; it gets none, which closes it.
	sel := tsruntime.NewResolver(root, tsruntime.NewVerifier(nil)).Resolve(t.Context(), tsruntime.Request{ID: "aot", Source: tsruntime.SourceEnv}, tsruntime.Set{})
	if sel.Verified == nil {
		t.Fatalf("selection %+v (fallback %+v)", sel, sel.Fallback)
	}
	defer releaseVerified(sel)
	cmd := typeScriptRuntimeCommand(t.Context(), sel, filepath.Join(root, "tree"), "cli.mjs", "node extension loader", nil)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", cmd.Process.Pid))
	_ = stdin.Close()
	_ = cmd.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(comm)); got != "pig-ts-host" {
		t.Fatalf("comm = %q, want pig-ts-host", got)
	}
}

func installExecutableManifest(t *testing.T, dir, digest string) {
	t.Helper()
	manifest := tsruntime.Manifest{ID: "aot", Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Image: &tsruntime.Image{Executable: "pig-ts-host", SHA256: digest}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeELFTier installs slot runtime id whose host is a copy of this test binary in fake host mode.
func fakeELFTier(t *testing.T, root, id string) string {
	t.Helper()
	t.Setenv(envFakeHost, id)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "host")
	digest := writeSelfCopy(t, exe, "A")
	manifest := tsruntime.Manifest{ID: id, Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Image: &tsruntime.Image{Executable: "host", SHA256: digest}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return exe
}

// openFDsOn counts the descriptors of this process that name path, including one whose file was replaced since.
func openFDsOn(t *testing.T, path string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name())); err == nil && strings.TrimSuffix(target, " (deleted)") == path {
			n++
		}
	}
	return n
}

// A launch holds the verified executable open from its selection until it returns, and releases it whatever the launch did: it started
// a process, re-admitted an extension into a running one (/reload), reused a retained packed process, or returned early with an error.
// A handle that was not released holds the file's descriptor, and a removed image's disk space, until a garbage collection.
func TestTypeScriptRuntimeLaunchesReleaseTheVerifiedHandle(t *testing.T) {
	cases := []struct {
		name      string
		isolation string
		names     []string
	}{
		{"isolated", "isolated", []string{"solo"}},
		{"packed", "", []string{"first", "second"}},
	}
	for _, c := range cases {
		t.Run(c.name+"/reload", func(t *testing.T) {
			f := newTierFixture(t, c.isolation, c.names...)
			exe := fakeELFTier(t, f.root, "tier1")
			h := f.host()
			f.reload(h)
			if got := openFDsOn(t, exe); got != 0 {
				t.Fatalf("%d descriptors on the host after the first load, want 0", got)
			}
			// /reload keeps the process (isolated: re-admits the extension; packed: reuses the retained process): no process starts, and the
			// selection's handle must still be released.
			f.reload(h)
			if got := openFDsOn(t, exe); got != 0 {
				t.Fatalf("%d descriptors on the host after /reload, want 0 (starts %v)", got, f.starts())
			}
			if starts := f.starts(); len(starts) != 1 {
				t.Fatalf("starts = %v, want one process for both loads (the reload kept it)", starts)
			}
		})
		t.Run(c.name+"/early error", func(t *testing.T) {
			f := newTierFixture(t, c.isolation, c.names...)
			exe := fakeELFTier(t, f.root, "tier1")
			h := f.host()
			// The launch fails after its selection, before it starts a process: it has no socket directory.
			h.sockRuntimeOnce.Do(func() {})
			h.sockRuntimeErr = errors.New("no socket directory")
			_, _ = h.Reload(f.ctx)
			if starts := f.starts(); len(starts) != 0 {
				t.Fatalf("starts = %v, want none", starts)
			}
			if _, ok := h.TypeScriptRuntimeSelections()[c.names[0]]; !ok && c.isolation == "isolated" {
				t.Fatalf("the launch never selected a runtime: the test does not reach the early return")
			}
			if got := openFDsOn(t, exe); got != 0 {
				t.Fatalf("%d descriptors on the host after a launch that failed early, want 0", got)
			}
		})
	}
}
