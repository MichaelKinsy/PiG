package extensionconformance

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var fixtureRoot, fixtureSourceRoot string

// sharedSources holds the sources the tests write for cells that a Host's shared cache (shareCellCaches) builds. A cell's cache key names its source directory, so
// a per-process directory would give every process its own cell. This one is the same for every process of the same checkout and contents.
var sharedSources string

// busBuildRoot is the config root of every event-bus rig's Host, shared the way concurrent PiG sessions share ~/.pig: a fixture compiles once for the package run and its builds are reused by later rigs.
var busBuildRoot string

func TestMain(m *testing.M) {
	parent := ""
	if runtime.GOOS != "windows" {
		parent = "/tmp" // Unix socket paths must also fit macOS's 104-byte limit.
	}
	testRoot, err := os.MkdirTemp(parent, "pig-conf-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test home:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve source SDK roots:", err)
		_ = os.RemoveAll(testRoot)
		os.Exit(2)
	}
	fixtureRoot, fixtureSourceRoot = testRoot, sourceRoot
	busBuildRoot = filepath.Join(testRoot, "bus-builds")
	// Each Host creates its own socket directory beneath this short package root.
	for key, value := range map[string]string{
		"XDG_RUNTIME_DIR": testRoot,
		"PIG_HOME":        filepath.Join(testRoot, "pig"),
		"PIG_SDK_GO_ROOT": filepath.Join(sourceRoot, "extensions", "sdk"),
		"PIG_SDK_PY_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-py"),
		"PIG_SDK_RS_ROOT": filepath.Join(sourceRoot, "extensions", "sdk-rs"),
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated %s: %v\n", key, err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	ensureUserToolchainsOnPath()
	// The test binary also runs as a helper command the tests start (os.Executable) from another working directory, where the repository root above is wrong
	// and nothing builds a cell, so only the process that is not itself a helper links the cell caches.
	primary := os.Getenv(cellCacheMarker) == ""
	if primary {
		flag.Parse() // m.Run parses again; the setup needs -test.timeout and -test.list.
	}
	// go test -list (check-shard-union.sh, test-shard-pattern.sh) only names the tests: it links no cache, hashes no sources and builds nothing.
	listing := flagValue("test.list") != ""
	if primary && !listing {
		if err := shareCellCaches(sourceRoot, filepath.Join(testRoot, "pig"), busBuildRoot); err != nil {
			fmt.Fprintln(os.Stderr, "share the extension cell cache:", err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
		_ = os.Setenv(cellCacheMarker, "1")
	}
	if os.Getenv("CARGO_TARGET_DIR") == "" {
		sdk := filepath.Join(sourceRoot, "extensions", "sdk-rs")
		target, err := testenv.SharedCargoTargetDir("conformance", filepath.Join(sdk, "Cargo.lock"), filepath.Join(sdk, "Cargo.toml"))
		if err == nil {
			err = os.Setenv("CARGO_TARGET_DIR", target)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "set package Cargo target:", err)
			_ = os.RemoveAll(testRoot)
			os.Exit(2)
		}
	}
	if primary && !listing {
		started := time.Now()
		if err := warmFixtures(); err != nil {
			// A build that failed is reported, not fatal: the tests that need it build it themselves and fail with its own error, and the tests of the other SDKs run.
			fmt.Fprintln(os.Stderr, "warning: building the shared fixtures before the tests failed; the tests that need what failed will fail, the others run:", err)
		}
		deductFromTimeout(time.Since(started))
	}
	code := m.Run()
	if err := os.RemoveAll(testRoot); err != nil {
		fmt.Fprintln(os.Stderr, "remove isolated test home:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}

// cellCacheMarker is set once a process has linked the cell caches, and children inherit it.
const cellCacheMarker = "PIG_TEST_CONFORMANCE_CELLS_SHARED"

// cellCacheInputs are the sources a cell's build reads beyond its own content address, relative to the repository root: the host's cell builders and what they
// import, the SDKs, and the conformance fixtures. A change to any of them selects another shared cell cache (testenv.SharedCellCacheDir).
var cellCacheInputs = []string{"go.mod", "go.sum", "coding/extension", "internal", "extensions", "test/extension-conformance/testfixture", "test/extension-conformance/testdata"}

// shareCellCaches points each config root's cache (cache/ext and cache/cells, where a Host builds its Go, Rust, Python and Lisp cells) at one directory shared by
// every go test process of the package, because the suite runs as 20 sharded processes and each would otherwise compile every Rust cell and save every Lisp image
// again, about a minute of setup charged to the first test that needs each. The cache is the product's own: content-addressed, built under a per-entry file lock
// and published by one rename (runtimecell.PublishArtifact), so processes that run at once or one after another share it as concurrent PiG sessions share
// ~/.pig/cache. The directory is named for a hash of the sources a build reads (testenv.SharedCellCacheDir), so a change to the builders, the SDKs or a fixture starts a new one. The package's own test files are not hashed, so every cell the tests generate
// carries a content hash of the text it writes (sourceHash). It also holds the sources the tests write for those cells (sharedSources). Windows may deny a link
// to an unprivileged user; each process then keeps its own cache and sources, as it always did.
func shareCellCaches(sourceRoot string, configRoots ...string) error {
	inputs := make([]string, len(cellCacheInputs))
	for i, input := range cellCacheInputs {
		inputs[i] = filepath.Join(sourceRoot, filepath.FromSlash(input))
	}
	shared, err := testenv.SharedCellCacheDir("conformance", inputs...)
	if err != nil {
		return err
	}
	cache := filepath.Join(shared, "cache")
	sharedSources = filepath.Join(shared, "sources")
	for _, dir := range []string{cache, sharedSources} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, root := range configRoots {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return err
		}
		if err := testenv.DirectoryLink(cache, filepath.Join(root, "cache")); err != nil {
			if runtime.GOOS != "windows" {
				return err
			}
			sharedSources = filepath.Join(fixtureRoot, "sources")
		}
	}
	return nil
}

// deductFromTimeout takes the time the setup used out of -test.timeout, which the testing package counts from m.Run: the tests' own deadlines (internal/testbudget
// reads t.Deadline) then end where go test's would, and not after the setup.
func deductFromTimeout(used time.Duration) {
	timeout, err := time.ParseDuration(flagValue("test.timeout"))
	if err != nil || timeout <= 0 {
		return
	}
	_ = flag.Set("test.timeout", max(timeout-used, time.Second).String())
}

// sourceHash names the content of generated fixture sources: the first 16 hex digits of the SHA-256 of the parts. A cell the tests generate carries it as its
// content hash, because the shared cell cache is keyed by the hashed fixtures and not by this package's test files.
func sourceHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "%d\x00%s", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// writeSharedSource writes a fixture source file of a shared cell (sharedSources) unless it already holds text. A process that starts while another compiles from
// the same directory must never see a half-written file, so a change is written beside the file and renamed over it.
func writeSharedSource(path, text string) error {
	if have, err := os.ReadFile(path); err == nil && string(have) == text {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(text)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// ensureUserToolchainsOnPath appends the per-user toolchain directories (mise shims, rustup's cargo bin) for each SDK tool that PATH does not already resolve. Non-interactive shells such as ssh commands never source the profile that adds them, so a host that has node and cargo installed would otherwise report them missing. A tool already on PATH is never overridden.
func ensureUserToolchainsOnPath() {
	// The missing-tool probe runs with a deliberately empty PATH and must see the tools as absent.
	if os.Getenv("PIG_TEST_MISSING_CONFORMANCE_TOOL") != "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	candidates := map[string][]string{
		"node":  {filepath.Join(home, ".local", "share", "mise", "shims")},
		"cargo": {filepath.Join(home, ".local", "share", "mise", "shims"), filepath.Join(home, ".cargo", "bin")},
	}
	path := os.Getenv("PATH")
	for _, tool := range []string{"node", "cargo"} {
		if _, err := exec.LookPath(tool); err == nil {
			continue
		}
		for _, dir := range candidates[tool] {
			if _, err := os.Stat(filepath.Join(dir, testExecutableName(tool))); err != nil || strings.Contains(string(os.PathListSeparator)+path+string(os.PathListSeparator), string(os.PathListSeparator)+dir+string(os.PathListSeparator)) {
				continue
			}
			path += string(os.PathListSeparator) + dir
			_ = os.Setenv("PATH", path)
			break
		}
	}
}
