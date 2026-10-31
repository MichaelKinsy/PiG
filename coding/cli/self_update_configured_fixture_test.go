package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// selfUpdateFixtureEnv makes a copy of this test binary the `pi-wrapper` update command or the `pig` on PATH of the
// configured self-update tests below, as Pi's package-command-paths.test.ts writes node shims named `pi-wrapper` and `pi`.
// The value is the file that receives the wrapper's arguments as a JSON array.
const selfUpdateFixtureEnv = "PIG_TEST_SELF_UPDATE_FIXTURE"

const (
	selfUpdateFixtureExitEnv    = "PIG_TEST_SELF_UPDATE_FIXTURE_EXIT"
	selfUpdateFixtureVersionEnv = "PIG_TEST_SELF_UPDATE_FIXTURE_VERSION"
)

// runSelfUpdateFixture is the program selfUpdateFixtureEnv selects: pi-wrapper records its arguments and exits with the
// configured code; pig prints the configured version. It reports false for any other name.
func runSelfUpdateFixture(record string) (int, bool) {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "self-update fixture:", err)
		return 2, true
	}
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe") {
	case "pi-wrapper":
		data, err := json.Marshal(os.Args[1:])
		if err == nil {
			err = os.WriteFile(record, data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "self-update fixture:", err)
			return 2, true
		}
		var code int
		_, _ = fmt.Sscan(os.Getenv(selfUpdateFixtureExitEnv), &code)
		return code, true
	case "pig":
		if len(os.Args) == 2 && os.Args[1] == "--version" {
			fmt.Println(os.Getenv(selfUpdateFixtureVersionEnv))
			return 0, true
		}
		return 2, true
	}
	return 0, false
}

// prepareSelfUpdateCommand puts a `pi-wrapper` update command and a `pig` that prints pigVersion first on PATH.
func prepareSelfUpdateCommand(t *testing.T, pigVersion string, exitCode int) (wrapper, record string) {
	t.Helper()
	bin := t.TempDir()
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	wrapper = filepath.Join(bin, "pi-wrapper"+suffix)
	copyTestBinary(t, wrapper)
	copyTestBinary(t, filepath.Join(bin, "pig"+suffix))
	record = filepath.Join(t.TempDir(), "self-update-record.json")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(selfUpdateFixtureEnv, record)
	t.Setenv(selfUpdateFixtureVersionEnv, pigVersion)
	t.Setenv(selfUpdateFixtureExitEnv, fmt.Sprint(exitCode))
	t.Setenv("PIG_SELF_UPDATE_COMMAND", "")
	return wrapper, record
}

func readSelfUpdateRecord(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("the update command did not run: %v", err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}
