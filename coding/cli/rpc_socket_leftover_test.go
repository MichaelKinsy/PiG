package cli

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A pig that is killed leaves the private socket directory it made (h<uint32> in <socket directory>/pig): nothing runs
// to remove it, and Pi's in-process extensions have no socket to leave. The directory holds harness-argv.json and the
// socket file of each generation that had bound and not yet connected (a connected generation's listener is closed and
// its file removed). A pig started next beside it, in the same socket directory, makes a directory of its own (the name
// is os.MkdirTemp's), so the file at 1.s that its first generation binds is not the file the killed pig left at 1.s, and
// its extension loads and answers. Whatever the next pig removes when it exits, it removes nothing of the killed pig's.
func TestAPigStartedBesideAKilledPigsSocketDirectoryLoadsAndLeavesTheLeftover(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the pig binary and starts Python extensions")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"lo_a.py":      pythonModuleStateProbe("lo_a", false, "lo_helper"),
		"lo_helper.py": "VERSION = \"v1\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, cwd, sessionDir := t.TempDir(), t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"quietStartup":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The socket directory is <socketBase>/pig: $XDG_RUNTIME_DIR/pig on Unix and <temporary directory>/pig on Windows.
	socketBase := testenv.ShortTempDir(t, "pig-sock-")
	socketRoot := filepath.Join(socketBase, "pig")
	wait := testbudget.Wait(t)
	start := func(probeLog string) *rpcProcess {
		env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1",
			"RELOAD_PROBE_LOG=" + probeLog, "XDG_RUNTIME_DIR=" + socketBase, "TMPDIR=" + socketBase, "TEMP=" + socketBase, "TMP=" + socketBase}
		process := startRPCProcessAt(t, cwd, env, "--no-extensions", "--model", "test-faux/faux-1", "--session-dir", sessionDir, "-e", filepath.Join(dir, "lo_a.py"))
		awaitPythonProbeLines(t, filepath.Join(probeLog, "lo_a.log"), 1, wait)
		return process
	}

	killed := start(t.TempDir())
	killedDirs := directoryNames(t, socketRoot)
	if len(killedDirs) != 1 {
		t.Fatalf("the first pig made the socket directories %v, want one", killedDirs)
	}
	leftover := filepath.Join(socketRoot, killedDirs[0])
	_ = killed.cmd.Process.Kill()
	_ = killed.cmd.Wait()
	killed.exited = true
	if !slices.Contains(directoryNames(t, leftover), "harness-argv.json") {
		t.Fatalf("a killed pig's socket directory %s holds %v, want its harness-argv.json", leftover, directoryNames(t, leftover))
	}
	// The socket file of a generation the killed pig had bound and not yet connected, at the name the next pig's first generation gets. Whether the killed pig's own file was gone by the kill depends on how far its connection had come, so it is replaced.
	const stale = "left by the killed pig"
	if err := os.Remove(filepath.Join(leftover, "1.s")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "1.s"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	leftNames := directoryNames(t, leftover)

	next := start(t.TempDir())
	dirs := directoryNames(t, socketRoot)
	if len(dirs) != 2 || !slices.Contains(dirs, killedDirs[0]) {
		t.Fatalf("with the second pig running the socket directories are %v, want the killed pig's %s and one of its own", dirs, killedDirs[0])
	}
	next.send(`{"id":"state","type":"get_state"}`)
	next.await("the second pig's state response", func(r rpcRecord) bool { return isSuccessResponse(r, "state") })
	next.closeAndWait("the second pig beside a killed pig's socket directory")
	if got := directoryNames(t, socketRoot); !slices.Contains(got, killedDirs[0]) {
		t.Fatalf("after the second pig exited the socket directories are %v, the killed pig's %s is gone", got, killedDirs[0])
	}
	if got := directoryNames(t, leftover); !slices.Equal(got, leftNames) {
		t.Fatalf("the killed pig's socket directory held %v and holds %v after the second pig", leftNames, got)
	}
	if got, err := os.ReadFile(filepath.Join(leftover, "1.s")); err != nil || string(got) != stale {
		t.Fatalf("the killed pig's 1.s is %q, %v after the second pig, want %q", got, err, stale)
	}
}

// directoryNames lists the entries of dir by name, sorted.
func directoryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
