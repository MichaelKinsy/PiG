package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Pi 1.1.0 builds its standalone binary with --no-compile-autoload-dotenv
// (packages/coding-agent/package.json build:binary, scripts/build-binaries.sh;
// #10473) because Bun loads .env, .env.local and .env.development from the
// launch directory into process.env. A Go binary has no such loader, so the
// equivalent contract is behavioral: files in the launch directory never reach
// pig's environment. The decoy agent directory holds an unparsable settings
// file, which pig reports on stderr only when PIG_CODING_AGENT_DIR points at it.
func TestLaunchDirectoryDotenvFilesDoNotReachPigEnvironment(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "cwd")
	decoy := filepath.Join(home, "decoy-agent")
	writeStartupFixtureFile(t, filepath.Join(decoy, "settings.json"), "{not json")
	dotenv := "PIG_CODING_AGENT_DIR=" + decoy + "\nPI_CODING_AGENT_DIR=" + decoy + "\n"
	for _, name := range []string{".env", ".env.local", ".env.development"} {
		writeStartupFixtureFile(t, filepath.Join(cwd, name), dotenv)
	}
	binary := buildPigBinaryForSignalTest(t)

	run := func(extraEnv ...string) string {
		t.Helper()
		cmd := exec.Command(binary, "--model", "test-faux/echo", "--offline", "--mode", "rpc")
		cmd.Dir = cwd
		var env []string
		for _, kv := range os.Environ() {
			switch strings.SplitN(kv, "=", 2)[0] {
			case "HOME", "PIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR":
			default:
				env = append(env, kv)
			}
		}
		cmd.Env = slices.Concat(env, []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_TEST_FAUX=1"}, extraEnv)
		cmd.Stdin = strings.NewReader(`{"id":"commands","type":"get_commands"}` + "\n")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("pig: %v\nstderr:\n%s", err, stderr.String())
		}
		if !strings.Contains(stdout.String(), `"id":"commands"`) {
			t.Fatalf("no get_commands response:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
		}
		return stderr.String()
	}

	if control := run("PIG_CODING_AGENT_DIR=" + decoy); !strings.Contains(control, "settings") {
		t.Fatalf("control run did not read the decoy agent directory; the probe cannot detect a loaded .env:\n%s", control)
	}
	if got := run(); strings.Contains(got, decoy) || strings.Contains(got, "settings") {
		t.Fatalf("launch-directory .env files reached pig's environment:\n%s", got)
	}
}
