package env

// The oracle for the daemon: Pi's own Rust daemon, built from the Pi source the repository is pinned to
// (.upstream/current/packages/env/daemon). Tests of daemon behavior run against both daemons, so a behavior PiG's Go
// daemon shares with Pi's is proved rather than assumed.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var oracle struct {
	once   sync.Once
	target string
	binary string
	err    error
}

// removeOracleBuild deletes the oracle's build directory; TestMain runs it after the tests.
func removeOracleBuild() {
	if oracle.target != "" {
		_ = os.RemoveAll(oracle.target)
	}
}

// oracleDaemon is Pi's daemon, built once per test run. It fails the test when it cannot be built: Cargo, the pinned
// toolchain and the locked crates are test prerequisites (`make test` requires the Rust toolchain).
func oracleDaemon(t testing.TB) string {
	t.Helper()
	oracle.once.Do(func() {
		source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "env", "daemon")
		if _, err := os.Stat(filepath.Join(source, "Cargo.toml")); err != nil {
			oracle.err = fmt.Errorf("the pinned Pi has no daemon source: %w", err)
			return
		}
		// A persistent target keyed by the daemon's lock: Pi's daemon dependencies compile once per machine, and a changed pin selects a fresh directory.
		target, err := testenv.SharedCargoTargetDir("env-oracle", filepath.Join(source, "Cargo.lock"), filepath.Join(source, "Cargo.toml"))
		if err != nil {
			oracle.err = err
			return
		}
		command := exec.Command("cargo", "build", "--release", "--locked", "--quiet", "--manifest-path", filepath.Join(source, "Cargo.toml"))
		command.Env = append(os.Environ(), "CARGO_TARGET_DIR="+target)
		for key, fallback := range map[string]string{"CARGO_HOME": ".cargo", "RUSTUP_HOME": ".rustup"} {
			if os.Getenv(key) == "" {
				//portlint:allow testisolation the toolchain's own cargo and rustup directories, not state under test: the oracle build reads the locked crates from them
				if home, err := os.UserHomeDir(); err == nil {
					command.Env = append(command.Env, key+"="+filepath.Join(home, fallback))
				}
			}
		}
		if output, err := command.CombinedOutput(); err != nil {
			oracle.err = fmt.Errorf("cargo build of Pi's daemon: %w\n%s", err, output)
			return
		}
		name := "pi-env"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		oracle.binary = filepath.Join(target, "release", name)
	})
	if oracle.err != nil {
		t.Fatal(oracle.err)
	}
	return oracle.binary
}

// terminalDaemon is the daemon the terminal tests run against; eachDaemon sets it for the duration of a subtest.
var terminalDaemon string

// skipWithoutPinnedTerminals skips a test that needs Pi's terminal operations when the pinned Pi has none: a pin older than
// packages/env/daemon/src/pty.rs builds a daemon that answers every pty request with "unknown operation". The pig run still
// executes; the pi run starts as soon as a pin contains pty.rs.
func skipWithoutPinnedTerminals(t testing.TB) {
	t.Helper()
	pty := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "env", "daemon", "src", "pty.rs")
	if _, err := os.Stat(pty); err != nil {
		t.Skipf("the pinned Pi has no terminal operations (%s is absent)", filepath.Base(pty))
	}
}

// eachDaemon runs body against PiG's Go daemon and Pi's Rust daemon.
func eachDaemon(t *testing.T, body func(t *testing.T)) {
	t.Helper()
	for _, daemon := range []struct {
		name string
		path func(testing.TB) string
	}{{"pig", daemonBinary}, {"pi", oracleDaemon}} {
		t.Run(daemon.name, func(t *testing.T) {
			if daemon.name == "pi" {
				skipWithoutPinnedTerminals(t)
			}
			terminalDaemon = daemon.path(t)
			t.Cleanup(func() { terminalDaemon = "" })
			body(t)
		})
	}
}
