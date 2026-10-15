//go:build parity

package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// A cli-mode scenario's pig side can be a Go probe: `go run ./PACKAGE ARGS` in its pig args, or a script that runs the
// test binaries of the packages in [env] pig_go_tests through test/parity/testdata/prebuilt-go-test.sh. Building a
// probe inside a timed run made the run's runtime and timeout cover the Go toolchain, and on a loaded host a cold
// build cache alone outlasted a 60 s timeout. RunScenario therefore builds them first (goPrebuildCache.prepare) and
// the timed runs start the binaries. One TestParity run builds each package once and removes the binaries at its end.

// goTestsEnv names the directory holding a scenario's pig_go_tests binaries, laid out as prebuilt-go-test.sh reads
// them: <dir>/<package directory>/<its last element>.test.
const goTestsEnv = "PIG_PARITY_GO_TESTS"

// goPrebuilds holds the binaries built for TestParity's scenarios.
var goPrebuilds goPrebuildCache

type goPrebuildCache struct {
	mu     sync.Mutex
	root   string
	builds map[string]*goPrebuild
}

// goPrebuild is the build of one output path; err is final once done is closed.
type goPrebuild struct {
	done chan struct{}
	err  error
}

// prepare returns sc with its pig side starting binaries built before the call returns: its `go run ./PACKAGE`
// becomes the binary built from PACKAGE, and pig_go_tests adds goTestsEnv to the pig environment. A scenario with
// neither is returned as is.
func (c *goPrebuildCache) prepare(sc *Scenario, logf func(format string, args ...any)) (*Scenario, error) {
	prepared := *sc
	runArgs, run := goRunArgs(&prepared)
	if run < 0 && len(sc.Env.PigGoTests) == 0 {
		return sc, nil
	}
	if sc.CLI.CWD == "" || strings.Contains(sc.CLI.CWD, "{{") {
		return nil, fmt.Errorf("a Go probe needs a fixed cli.cwd: its packages are relative to it")
	}
	cwd := resolveScenarioCWD(sc.SourcePath, sc.CLI.CWD)
	root, err := c.dir()
	if err != nil {
		return nil, err
	}
	if run >= 0 {
		args := *runArgs
		if run+2 >= len(args) || !isLocalGoPackage(args[run+2]) {
			return nil, fmt.Errorf("pig args %q: the runner builds `go run ./PACKAGE ARGS` before the timed runs and takes no go run flags", args[run:])
		}
		pkg := args[run+2]
		binary := filepath.Join(root, "run", goPrebuildKey(filepath.Join(cwd, pkg)), path.Base(pkg)+exeSuffix())
		if err := c.build(binary, logf, cwd, "build", "-o", binary, pkg); err != nil {
			return nil, err
		}
		*runArgs = slices.Concat(args[:run], []string{binary}, args[run+3:])
	}
	if len(sc.Env.PigGoTests) > 0 {
		dir := filepath.Join(root, "test", goPrebuildKey(cwd))
		for _, pkg := range sc.Env.PigGoTests {
			rel := filepath.FromSlash(strings.TrimPrefix(pkg, "./"))
			binary := filepath.Join(dir, rel, filepath.Base(rel)+".test"+exeSuffix())
			if err := c.build(binary, logf, cwd, "test", "-c", "-o", binary, pkg); err != nil {
				return nil, err
			}
		}
		prepared.Env.Pig = append(slices.Clip(sc.Env.Pig), goTestsEnv+"="+dir)
	}
	return &prepared, nil
}

// dir returns the directory the binaries go in, creating it on first use.
func (c *goPrebuildCache) dir() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.root == "" {
		root, err := os.MkdirTemp("", "pig-parity-go-")
		if err != nil {
			return "", fmt.Errorf("create the Go probe build directory: %w", err)
		}
		c.root = root
	}
	return c.root, nil
}

// build runs `go args...` in dir to write output, once per output: concurrent scenarios that need the same binary
// wait for the first build.
func (c *goPrebuildCache) build(output string, logf func(format string, args ...any), dir string, args ...string) error {
	c.mu.Lock()
	b := c.builds[output]
	if b != nil {
		c.mu.Unlock()
		<-b.done
		return b.err
	}
	b = &goPrebuild{done: make(chan struct{})}
	if c.builds == nil {
		c.builds = map[string]*goPrebuild{}
	}
	c.builds[output] = b
	c.mu.Unlock()
	defer close(b.done)
	b.err = runGoPrebuild(output, logf, dir, args)
	return b.err
}

func runGoPrebuild(output string, logf func(format string, args ...any), dir string, args []string) error {
	command := "go " + strings.Join(args, " ")
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fmt.Errorf("%s: %w", command, err)
	}
	start := time.Now()
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s (built before the timed runs): %w\n%s", command, err, out)
	}
	if _, err := os.Stat(output); err != nil {
		return fmt.Errorf("%s wrote no binary: %w\n%s", command, err, out)
	}
	logf("prebuilt in %s: %s", time.Since(start).Round(time.Millisecond), command)
	return nil
}

// removeAll deletes the binaries and forgets their builds. TestParity calls it after its scenarios have finished.
func (c *goPrebuildCache) removeAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	root := c.root
	c.root, c.builds = "", nil
	if root == "" {
		return nil
	}
	return os.RemoveAll(root)
}

// goRunArgs finds the `go run` that a cli-mode scenario's /usr/bin/env pig_bin, with the base args overridden,
// starts after env's -u NAME and NAME=VALUE operands. It returns the argument list holding it, [env] pig_args or the
// [cli] pig_args that follow them, and the index of its `go`; or nil and -1 when the pig side is not `go run`.
func goRunArgs(sc *Scenario) (*[]string, int) {
	if sc.Driver != "cli-mode" || sc.Env.PigBin != posixEnvLauncher || !sc.Env.OverrideBaseArgs {
		return nil, -1
	}
	run, operandsOnly := goRunAfterEnvOperands(sc.Env.PigArgs)
	if run >= 0 {
		return &sc.Env.PigArgs, run
	}
	if operandsOnly && len(sc.Env.PigExtensions) == 0 {
		if run, _ := goRunAfterEnvOperands(sc.CLI.PigArgs); run >= 0 {
			return &sc.CLI.PigArgs, run
		}
	}
	return nil, -1
}

// goRunAfterEnvOperands returns the index of a `go run` that only env operands precede in args, or -1, and whether
// args holds nothing but env operands.
func goRunAfterEnvOperands(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-u":
			i++
		case strings.Contains(args[i], "=") && !strings.HasPrefix(args[i], "-"):
		case args[i] == "go" && i+1 < len(args) && args[i+1] == "run":
			return i, false
		default:
			return -1, false
		}
	}
	return -1, true
}

// isLocalGoPackage reports whether pkg names a package directory inside the current directory by a ./-relative
// slash path, the form the scenarios give go run and go test.
func isLocalGoPackage(pkg string) bool {
	rel, ok := strings.CutPrefix(pkg, "./")
	return ok && rel != "" && path.Clean(rel) == rel && rel != ".." && !strings.HasPrefix(rel, "../") &&
		!strings.Contains(rel, `\`) && !strings.Contains(rel, "...")
}

func goPrebuildKey(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
