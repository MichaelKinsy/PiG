package ciimages

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestPorterValidationIgnoresCallerConfiguration(t *testing.T) {
	root := repoRoot(t)
	name := "pig"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	build := exec.CommandContext(t.Context(), "go", "build", "-buildvcs=false", "-o", binary, "./cmd/pig")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build validation binary: %v\n%s", err, output)
	}
	cacheCommand := exec.CommandContext(t.Context(), "go", "env", "GOCACHE", "GOMODCACHE", "GOROOT")
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	caches := strings.Split(strings.TrimSpace(string(cacheOutput)), "\n")
	if len(caches) != 3 {
		t.Fatalf("compiler cache paths = %q", cacheOutput)
	}
	for _, piDirs := range []string{"", "1"} {
		t.Run("Pi directories="+piDirs, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"trust.json.lock":    "parent trust lock",
				"settings.json.lock": "parent settings lock",
				"settings.json":      "invalid parent configuration",
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(agentDir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{
				{"install", "--validate-only", "--json", filepath.Join(root, "piglets/porter/extensions/pig-porter")},
				{"piglet", "validate", filepath.Join(root, "piglets/porter/pig-porter.yaml")},
			} {
				cmd := exec.CommandContext(t.Context(), testenv.Bash(t), append([]string{filepath.Join(root, "automation/ci/with-isolated-pig-home.sh"), binary}, args...)...)
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home,
					"PIG_HOME="+filepath.Join(home, ".pig"), "XDG_CONFIG_HOME="+filepath.Join(home, "config"),
					"PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
					"PIG_USE_PI_DIRS="+piDirs, "GOWORK=off", "GOCACHE="+caches[0], "GOMODCACHE="+caches[1], "GOROOT="+caches[2])
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				output, err := cmd.Output()
				if err != nil {
					t.Fatalf("validate %v: %v\n%s\n%s", args, err, output, &stderr)
				}
				if stderr.Len() != 0 {
					t.Errorf("validation must not diagnose the invoking user's configuration: %s", &stderr)
				}
			}
			entries, err := os.ReadDir(agentDir)
			if err != nil || len(entries) != len(files) {
				t.Fatalf("validation changed the caller's agent directory: %v, %v", entries, err)
			}
			for name, want := range files {
				if data, err := os.ReadFile(filepath.Join(agentDir, name)); err != nil || string(data) != want {
					t.Errorf("validation changed parent %s: %q, %v", name, data, err)
				}
			}
		})
	}
}

func TestIsolatedPigHomePreservesCommandAndCleansUp(t *testing.T) {
	goRootOutput, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	goRoot := strings.TrimSpace(string(goRootOutput))
	rootKeys := []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"}
	keys := append(slices.Clone(rootKeys), "GOCACHE", "GOMODCACHE")
	for _, exit := range []int{0, 37} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			parent := t.TempDir()
			cache, modules := filepath.Join(parent, "compiler cache"), filepath.Join(parent, "module cache")
			shim := t.TempDir()
			if err := os.WriteFile(filepath.Join(shim, "go"), []byte("#!/usr/bin/env bash\nprintf 'unresolved Go shim used\\n' >&2\nexit 98\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			args := []string{"two words", "quote'and\"double", "*"}
			// The MSYS runtime shows HOME to an MSYS program in POSIX form (/tmp/...) and to a native program as the Windows path the wrapper exported; the probe prints the Windows form.
			script := `go version >/dev/null || exit $?
[[ "$(go env GOTELEMETRY)" == off ]] || exit 99
for key in HOME USERPROFILE APPDATA LOCALAPPDATA XDG_CONFIG_HOME PIG_HOME PIG_CODING_AGENT_DIR PI_CODING_AGENT_DIR GOCACHE GOMODCACHE; do
  value=${!key}
  case "$key:$OSTYPE" in HOME:msys*|HOME:cygwin*) value=$(cygpath -m "$value") ;; esac
  printf '%s\n' "$value"
done
status="$1"; shift
printf '%s\n' "$@"
exit "$status"`
			bash := testenv.Bash(t)
			command := bash
			if runtime.GOOS == "windows" {
				// Git for Windows' bin\bash.exe is a native launcher. From the MSYS wrapper it would receive the arguments through a Windows command line, which its MSYS bash parses and globs again; the MSYS bash on the wrapper's PATH receives them as argv.
				command = "bash"
			}
			commandArgs := []string{filepath.Join(repoRoot(t), "automation/ci/with-isolated-pig-home.sh"), command, "-c", script, "probe", strconv.Itoa(exit)}
			cmd := exec.CommandContext(t.Context(), bash, append(commandArgs, args...)...)
			testenv.VerbatimArgs(cmd)
			cmd.Env = append(os.Environ(), "HOME="+parent, "USERPROFILE="+parent, "TMPDIR="+parent,
				"GOCACHE="+cache, "GOMODCACHE="+modules, "GOROOT="+goRoot,
				"PATH="+shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := cmd.Output()
			if exit == 0 && err != nil {
				t.Fatal(err)
			}
			if exit != 0 {
				var status *exec.ExitError
				if !errors.As(err, &status) || status.ExitCode() != exit {
					t.Fatalf("exit = %v, want %d", err, exit)
				}
			}
			lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
			if len(lines) != len(keys)+len(args) {
				t.Fatalf("probe output = %q", output)
			}
			home := lines[0]
			if filepath.Clean(home) == filepath.Clean(parent) {
				t.Fatal("validator inherited the caller's HOME")
			}
			for i, key := range rootKeys {
				rel, err := filepath.Rel(home, lines[i])
				if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					t.Errorf("%s=%q is outside isolated HOME %q", key, lines[i], home)
				}
			}
			if lines[len(rootKeys)] != cache || lines[len(rootKeys)+1] != modules || !slices.Equal(lines[len(keys):], args) {
				t.Fatalf("compiler cache or arguments changed: %q", lines)
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("isolated HOME not removed after exit %d: %v", exit, err)
			}
		})
	}
}
