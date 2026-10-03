package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A POSIX path may hold a tab, a line feed, a carriage return or a percent sign, and Pi's loader imports such a source path (extensions/loader.ts loadExtensionModule). The admission line is tab- and line-delimited, so the path fields travel percent-encoded and the Node runtime decodes them.
func TestFactoryAdmissionCarriesAnyPathInItsEntryAndCwd(t *testing.T) {
	entry := "/a\tb/c\nd/e\rf/100%25/x.mjs"
	cwd := "/work\tdir/100%"
	line, err := factoryAdmission{Name: "ext", Socket: "/s/x.sock", Entry: entry, Cwd: cwd, ReloadPass: 3}.line()
	if err != nil {
		t.Fatalf("admission of a path with control characters failed: %v", err)
	}
	if strings.Count(line, "\n") != 1 || !strings.HasSuffix(line, "\n") || strings.Contains(line, "\r") {
		t.Fatalf("line = %q, want exactly one terminating line feed", line)
	}
	fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
	if len(fields) != 6 || fields[0] != "admit" || fields[1] != "ext" || fields[2] != "/s/x.sock" || fields[5] != "3" {
		t.Fatalf("fields = %q, want the six-field admission", fields)
	}
	if fields[3] != "/a%09b/c%0Ad/e%0Df/100%2525/x.mjs" || fields[4] != "/work%09dir/100%25" {
		t.Fatalf("entry = %q cwd = %q, want percent-encoded path fields", fields[3], fields[4])
	}
}

func TestNodeReloadReinvokesFactoryFromAPathWithControlCharacters(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths cannot hold these characters")
	}
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := filepath.Join(t.TempDir(), "config\tpart", "new\nline", "100%25")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Skipf("filesystem refuses control characters in a directory name: %v", err)
			}
			mjs, log := retainedNodeExtension(t, root, "probe.mjs", false)
			mjs.Isolation = isolation
			configs := []ExtConfig{mjs}
			cwd := filepath.Join(t.TempDir(), "cwd\tdir")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			h := NewHost(cwd)
			t.Cleanup(func() { h.Shutdown("test done") })
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			first := retainedProbeOf(t, h, mjs.Name)
			for reload := 1; reload <= 2; reload++ {
				if _, err := h.Reload(t.Context()); err != nil {
					t.Fatalf("reload %d: %v", reload, err)
				}
				if issues := h.LastReloadReport().Issues; len(issues) != 0 {
					t.Fatalf("reload %d issues = %q, want none", reload, issues)
				}
				if got := retainedProbeOf(t, h, mjs.Name); got.Pid != first.Pid || got.Calls != 1 {
					t.Fatalf("reload %d: probe = %+v, want pid %d with the module re-evaluated (1 factory call)", reload, got, first.Pid)
				}
			}
			// Probed with Pi 0.87.1's loadExtensions after clearExtensionCache per reload: for a path holding a tab, a line feed or a percent sign jiti does not use Node's ESM cache, so each reload evaluates the module and calls its factory, and the runtime process is the same.
			if modules, factories := retainedLogCounts(t, log); modules != 3 || factories != 3 {
				t.Errorf("module evaluated %d times with %d factory calls, want 3 and 3", modules, factories)
			}
		})
	}
}

// Pi keys its factory cache by the resolved configured cwd. A cwd holding a tab travels percent-encoded in the admission, and the runtime must decode it before comparing it with the cwd of the process's first generation, or a same-cwd Session replacement clears the cache.
func TestSessionReplacementKeepsTheCacheForACwdWithControlCharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths cannot hold these characters")
	}
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := t.TempDir()
			ts, log := retainedNodeExtension(t, root, "cached.ts", true)
			ts.Isolation = isolation
			configs := []ExtConfig{ts}
			cwd := filepath.Join(t.TempDir(), "cwd\tdir%25")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Skipf("filesystem refuses a tab in a directory name: %v", err)
			}
			retention := NewRuntimeRetention()
			t.Cleanup(retention.Close)
			first := NewHost(cwd)
			first.SetRuntimeRetention(retention)
			t.Cleanup(func() { first.Shutdown("test done") })
			if _, errs := first.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			before := retainedProbeOf(t, first, ts.Name)
			first.Retain()
			first.Shutdown("session replaced")

			second := NewHost(cwd)
			second.SetRuntimeRetention(retention)
			t.Cleanup(func() { second.Shutdown("test done") })
			if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			if got := retainedProbeOf(t, second, ts.Name); got.Pid != before.Pid || got.Calls != 2 {
				t.Errorf("replacement probe = %+v, want pid %d calling the cached factory (2 calls)", got, before.Pid)
			}
			if modules, factories := retainedLogCounts(t, log); modules != 1 || factories != 2 {
				t.Errorf("module evaluated %d times with %d factory calls, want 1 and 2", modules, factories)
			}
		})
	}
}

// A control character in a name or a socket path is legal where Pi loads it: loader.ts resolves the source path and imports it, and a Unix socket path may hold any byte but NUL. The admission line carries every field percent-encoded, so the host can send them and every runner decodes the same escapes.
func TestFactoryAdmissionEncodesNameAndSocketLikeThePaths(t *testing.T) {
	line, err := factoryAdmission{Name: "has\tcontrol%", Socket: "/run/with\ttab\nx/e-0.sock", Entry: "/e", Cwd: "/c", ReloadPass: 1}.line()
	if err != nil {
		t.Fatalf("admission of a name and socket with control characters failed: %v", err)
	}
	fields := strings.Split(strings.TrimSuffix(line, "\n"), "\t")
	if len(fields) != 6 || strings.Count(line, "\n") != 1 {
		t.Fatalf("line = %q, want one six-field line", line)
	}
	if fields[1] != "has%09control%25" || fields[2] != "/run/with%09tab%0Ax/e-0.sock" {
		t.Fatalf("name = %q socket = %q, want percent-encoded fields", fields[1], fields[2])
	}
	park, err := factoryAdmission{Op: "park", Socket: "/run/with\ttab/p.sock"}.line()
	if err != nil || !strings.Contains(park, "\t/run/with%09tab/p.sock\t") {
		t.Fatalf("park line = %q, %v, want the encoded socket", park, err)
	}
	if _, err := (factoryAdmission{Op: "a\tb"}).line(); err == nil {
		t.Fatal("an operation with a tab must be rejected: it is the host's own keyword")
	}
}

// controlRuntimeDir points XDG_RUNTIME_DIR at a directory whose name holds a tab, where the host places its sockets.
func controlRuntimeDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths cannot hold these characters")
	}
	base, err := os.MkdirTemp("", "d70")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	dir := filepath.Join(base, "run\ttab")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Skipf("filesystem refuses a tab in a directory name: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", dir)
	return dir
}

func TestNodeFactoryWithControlCharacterBasenameLoadsAndReloads(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows paths cannot hold these characters")
	}
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := t.TempDir()
			mjs, _ := retainedNodeExtension(t, root, "has\tcontrol.mjs", false)
			config, _, err := ResolveExtConfig(mjs.Source)
			if err != nil {
				t.Fatal(err)
			}
			if config.Name != "has\tcontrol" {
				t.Fatalf("derived name = %q, want the logical basename", config.Name)
			}
			config.Isolation = isolation
			configs := []ExtConfig{config}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			first := retainedProbeOf(t, h, config.Name)
			for reload := 1; reload <= 2; reload++ {
				if _, err := h.Reload(t.Context()); err != nil {
					t.Fatalf("reload %d: %v", reload, err)
				}
				if issues := h.LastReloadReport().Issues; len(issues) != 0 {
					t.Fatalf("reload %d issues = %q, want none", reload, issues)
				}
				// A path holding a tab is not cached by Node's ESM loader, so each reload re-evaluates the module in the same process.
				if got := retainedProbeOf(t, h, config.Name); got.Pid != first.Pid || got.Calls != 1 {
					t.Fatalf("reload %d: probe = %+v, want pid %d with the module re-evaluated (1 factory call)", reload, got, first.Pid)
				}
			}
		})
	}
}

func TestNodeFactoryLoadsAndReloadsWithControlCharactersInTheSocketDirectory(t *testing.T) {
	for _, isolation := range []string{"shared-ok", "isolated"} {
		t.Run(isolation, func(t *testing.T) {
			nodeCellRequireNode(t)
			runtimeDir := controlRuntimeDir(t)
			mjs, _ := retainedNodeExtension(t, t.TempDir(), "probe.mjs", false)
			mjs.Isolation = isolation
			configs := []ExtConfig{mjs}
			retention := NewRuntimeRetention()
			t.Cleanup(retention.Close)
			h := NewHost(t.TempDir())
			h.SetRuntimeRetention(retention)
			t.Cleanup(func() { h.Shutdown("test done") })
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			if !strings.HasPrefix(h.socketDir, runtimeDir) {
				t.Fatalf("socket directory = %q, want it under %q", h.socketDir, runtimeDir)
			}
			if _, errs := h.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			first := retainedProbeOf(t, h, mjs.Name)
			if _, err := h.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := retainedProbeOf(t, h, mjs.Name); got.Pid != first.Pid || got.Calls != 2 {
				t.Fatalf("reload probe = %+v, want pid %d with 2 factory calls", got, first.Pid)
			}
			// A replacement Session parks the process (the park line names a socket) and admits a generation on a new socket.
			h.Retain()
			h.Shutdown("session replaced")
			second := NewHost(t.TempDir())
			second.SetRuntimeRetention(retention)
			t.Cleanup(func() { second.Shutdown("test done") })
			if _, errs := second.LoadAll(t.Context(), configs); len(errs) > 0 {
				t.Fatal(errs)
			}
			if got := retainedProbeOf(t, second, mjs.Name); got.Pid != first.Pid || got.Calls != 3 {
				t.Fatalf("replacement probe = %+v, want the parked pid %d with 3 factory calls", got, first.Pid)
			}
		})
	}
}

// The Go, Python and Rust runners read the same admission line, so each decodes the escapes: a member whose name holds a tab is matched by its logical name, and a socket directory with a tab is dialed as the host created it.
func TestGoFactoryReloadWithControlCharactersInNameAndSocketDirectory(t *testing.T) {
	controlRuntimeDir(t)
	goFactoryRetainedCases(t, "retained\tgo")
}

func TestPythonFactoryReloadWithControlCharactersInNameAndSocketDirectory(t *testing.T) {
	controlRuntimeDir(t)
	pythonFactoryRetainedCases(t, "retained\tpy")
}

func TestRustFactoryReloadWithControlCharactersInNameAndSocketDirectory(t *testing.T) {
	controlRuntimeDir(t)
	rustFactoryRetainedCases(t, "retained\trs")
}
