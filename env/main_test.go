package env

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/env/daemon"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain makes the test binary a pi-env daemon when it is started as `<binary> serve --token <hex>`, so the tests
// need no build step: Connection commands name the test binary, as Pi's tests name the cargo-built daemon. A test run scopes
// the temporary directory and removes it afterwards: RemoteExecutionEnv.createTempFile leaves its tmp-* directory behind by
// design (remote-env.ts cleanup only kills commands), so the test that creates one cannot clean it alone.
func TestMain(m *testing.M) {
	if os.Getenv(fakeSSHEnv) != "" {
		os.Exit(runFakeSSH(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(os.Args) >= 5 && os.Args[1] == "old-daemon" && os.Args[2] == "serve" && os.Args[3] == "--token" {
		os.Exit(runOldDaemon(os.Stdin, os.Stdout, os.Args[4]))
	}
	if len(os.Args) >= 4 && os.Args[1] == "serve" && os.Args[2] == "--token" {
		if err := daemon.Serve(os.Stdin, os.Stdout, os.Args[3], daemon.Options{Version: "test"}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	code := testenv.RunScoped(m, "pig-env-")
	removeOracleBuild()
	os.Exit(code)
}

// daemonBinary is the daemon the tests run: PI_ENV_DAEMON (CI points it at release builds), else this test binary.
func daemonBinary(t testing.TB) string {
	t.Helper()
	if configured := os.Getenv("PI_ENV_DAEMON"); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			t.Fatalf("PI_ENV_DAEMON %s: %v", configured, err)
		}
		return configured
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(executable)
}
