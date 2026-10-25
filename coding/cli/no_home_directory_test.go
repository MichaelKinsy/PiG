package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/nodehome"
)

const noHomeChildEnv = "PIG_TEST_NO_HOME_DIRECTORY_CHILD"

// noHomeDirectoryMessage is what Node's os.homedir() throws where no HOME is set and the user database has no entry for the process's user.
const noHomeDirectoryMessage = "A system error occurred: uv_os_homedir returned ENOENT (no such file or directory)"

// noHomeDirectoryEnvironment is a pig environment with no home variable: HOME and, for Windows, USERPROFILE are gone, and the config root and agent directory are named, as
// PIG_HOME is the way to run PiG without a home directory.
func noHomeDirectoryEnvironment(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	var environment []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HOME", "USERPROFILE", "PIG_HOME", "PIG_CODING_AGENT_DIR", "XDG_CONFIG_HOME":
		default:
			environment = append(environment, kv)
		}
	}
	return append(environment, "PIG_HOME="+filepath.Join(root, "pighome"), "PIG_CODING_AGENT_DIR="+filepath.Join(root, "agent"), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_OFFLINE=1")
}

// Pi's resolve() starts with getHomeDir (package-manager.ts:2464), which throws where Node's os.homedir() does, and every mode loads resources through it: the config command's
// listing and a reload do not fall back to a path relative to the working directory either.
func TestResourceLoadingFailsWhereNoHomeDirectoryExists(t *testing.T) {
	t.Setenv("HOME", "")
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nodehome.Override(func() (string, error) { return "", &nodehome.Error{} }))
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	var thrown *nodehome.Error
	if _, err := collectConfigResourceItems(cwd, agentDir, sm); !errors.As(err, &thrown) {
		t.Errorf("the config command's resource listing: error = %v, want *nodehome.Error", err)
	}
	snapshot := reloadResourceSnapshotProvider(cwd, agentDir, sm, Args{}, nil)()
	if !errors.As(snapshot.Err, &thrown) {
		t.Errorf("a reload: error = %v, want *nodehome.Error", snapshot.Err)
	}
}

// TestStartupFailsWhereNoHomeDirectoryExists runs the real command line in print and RPC mode as a child of this test binary on a host that has no home directory (nodehome.Override
// in the child: no environment variable can remove the user database's entry for the test's user). Pi exits 1 with Node's SystemError and writes no response; so does pig.
func TestStartupFailsWhereNoHomeDirectoryExists(t *testing.T) {
	switch os.Getenv(noHomeChildEnv) {
	case "print":
		nodehome.Override(func() (string, error) { return "", &nodehome.Error{} })
		Main([]string{"--model", "test-faux/faux-1", "--no-extensions", "-p", "reply with exactly: ok"}, nil)
		return
	case "rpc":
		nodehome.Override(func() (string, error) { return "", &nodehome.Error{} })
		Main([]string{"--mode", "rpc", "--model", "test-faux/faux-1", "--no-extensions"}, nil)
		return
	case "version", "list":
		nodehome.Override(func() (string, error) { return "", &nodehome.Error{} })
		Main([]string{map[string]string{"version": "--version", "list": "list"}[os.Getenv(noHomeChildEnv)]}, nil)
		return
	}
	if testing.Short() {
		t.Skip("runs the command line in a child process")
	}
	for _, mode := range []string{"print", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStartupFailsWhereNoHomeDirectoryExists$")
			command.Env = append(noHomeDirectoryEnvironment(t), noHomeChildEnv+"="+mode)
			command.Dir = t.TempDir()
			command.Stdin = strings.NewReader(`{"id":"1","type":"get_state"}` + "\n")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("exit = %v, want exit status 1\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if !strings.Contains(stderr.String(), noHomeDirectoryMessage) {
				t.Errorf("stderr does not carry Node's message %q:\n%s", noHomeDirectoryMessage, &stderr)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing written before the failure", &stdout)
			}
		})
	}
}

// TestStartupFollowsNodeWhenThereIsNoHomeDirectory is the same startup against the binary and Node itself: whether `node -p "require('os').homedir()"` throws without HOME on this host
// decides whether the run fails (with Node's message, writing nothing) or succeeds, so the host's user database cannot change the verdict between the two.
func TestStartupFollowsNodeWhenThereIsNoHomeDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the pig binary")
	}
	environment := noHomeDirectoryEnvironment(t)
	probe := exec.CommandContext(t.Context(), "node", "-p", `require('node:os').homedir()`)
	probe.Env = environment
	_, probeErr := probe.Output()
	var probeExit *exec.ExitError
	nodeThrows := errors.As(probeErr, &probeExit)
	if probeErr != nil && !nodeThrows {
		t.Fatal(probeErr)
	}
	if nodeThrows && !strings.Contains(string(probeExit.Stderr), "uv_os_homedir") {
		t.Fatalf("node failed for another reason: %s", probeExit.Stderr)
	}
	binary := buildPigBinaryForSignalTest(t)
	command := exec.CommandContext(t.Context(), binary, "--model", "test-faux/faux-1", "--no-extensions", "-p", "reply with exactly: ok")
	command.Env = environment
	command.Dir = t.TempDir()
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if !nodeThrows {
		if err != nil || !strings.Contains(stdout.String(), "ok") {
			t.Fatalf("node finds a home directory here, so pig must start: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
		}
		return
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("node throws here (no HOME, no user database entry), so pig must exit 1: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	if !strings.Contains(stderr.String(), noHomeDirectoryMessage) || stdout.Len() != 0 {
		t.Errorf("stdout %q, stderr %q; want Node's message %q and no output", &stdout, &stderr, noHomeDirectoryMessage)
	}
}

// piHomedirThrows is a Node preload that makes os.homedir() throw the SystemError Node throws where no HOME is set and the user database has no entry for the process's user, so Pi's behavior there can
// be run on any host: no environment variable can remove the user database's entry for the test's user.
const piHomedirThrows = `const os = require("node:os");
os.homedir = () => {
  const error = new Error("` + noHomeDirectoryMessage + `");
  error.name = "SystemError";
  error.code = "ERR_SYSTEM_ERROR";
  throw error;
};
`

// TestNoHomeDirectoryPiCrashesAtImportWherePiGRunsResourceFreeCommands pins both sides of D200 (docs/parity/divergences/D200.md). With no home directory Pi's load fails in every mode, `--version` and `list`
// included, with Node's uncaught SystemError and exit status 1: tools-manager.ts:11 evaluates getBinDir() at import, which expands the agent directory through normalizePath, and normalizePath asks for the
// home directory before it looks at its input (utils/paths.ts:89). PiG, whose PIG_HOME needs no home directory, runs those commands and fails with the same error only where Pi's home lookup would run: the
// resource loading of TestStartupFailsWhereNoHomeDirectoryExists.
func TestNoHomeDirectoryPiCrashesAtImportWherePiGRunsResourceFreeCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("runs Pi and the command line in child processes")
	}
	piCLI, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/cli.js")
	if err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(t.TempDir(), "homedir-throws.cjs")
	if err := os.WriteFile(preload, []byte(piHomedirThrows), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--version"}, {"list"}} {
		t.Run(args[0], func(t *testing.T) {
			pi := exec.CommandContext(t.Context(), "node", append([]string{"--require", preload, piCLI}, args...)...)
			pi.Env = noHomeDirectoryEnvironment(t)
			pi.Env = append(pi.Env, "PI_CODING_AGENT_DIR="+filepath.Join(t.TempDir(), "agent"))
			pi.Dir = t.TempDir()
			var piStdout, piStderr bytes.Buffer
			pi.Stdout, pi.Stderr = &piStdout, &piStderr
			err := pi.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(piStderr.String(), noHomeDirectoryMessage) || piStdout.Len() != 0 {
				t.Fatalf("Pi %v with no home directory: %v\nstdout: %s\nstderr: %s\nwant exit status 1, Node's message on stderr and nothing on stdout", args, err, &piStdout, &piStderr)
			}
			mode := map[string]string{"--version": "version", "list": "list"}[args[0]]
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestStartupFailsWhereNoHomeDirectoryExists$")
			child.Env = append(noHomeDirectoryEnvironment(t), noHomeChildEnv+"="+mode)
			child.Dir = t.TempDir()
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Run(); err != nil {
				t.Fatalf("PiG %v with no home directory (D200: Pi crashes at import, PiG runs it): %v\nstdout: %s\nstderr: %s", args, err, &stdout, &stderr)
			}
			if strings.Contains(stderr.String(), noHomeDirectoryMessage) {
				t.Errorf("PiG %v reports %q on stderr:\n%s", args, noHomeDirectoryMessage, &stderr)
			}
		})
	}
}
