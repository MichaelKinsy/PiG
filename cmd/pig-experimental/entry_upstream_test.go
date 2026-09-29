package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func TestDevelopmentCLIEntryUpstream(t *testing.T) {
	t.Parallel()
	executable := "pig-experimental"
	goExecutable := "go"
	if runtime.GOOS == "windows" {
		executable += ".exe"
		goExecutable += ".exe"
	}
	goRootOutput, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("resolve GOROOT: %v", err)
	}
	goRoot := strings.TrimSpace(string(goRootOutput))
	goBinary := filepath.Join(goRoot, "bin", goExecutable)
	cacheCommand := exec.CommandContext(t.Context(), goBinary, "env", "-json", "GOCACHE", "GOMODCACHE", "GOPATH")
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		t.Fatalf("resolve Go caches before HOME isolation: %v", err)
	}
	var caches map[string]string
	if err := json.Unmarshal(cacheOutput, &caches); err != nil {
		t.Fatal(err)
	}

	buildHome := t.TempDir()
	buildEnv := []string{
		"PATH=" + filepath.Dir(goBinary) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GOROOT=" + goRoot,
		"HOME=" + buildHome,
		"USERPROFILE=" + buildHome,
		"PIG_CODING_AGENT_DIR=" + filepath.Join(buildHome, "pig-agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(buildHome, "pi-agent"),
		"PIG_HOME=" + filepath.Join(buildHome, "pig"),
		"PI_HOME=" + filepath.Join(buildHome, "pi"),
		"XDG_CONFIG_HOME=" + filepath.Join(buildHome, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(buildHome, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(buildHome, "data"),
		"XDG_STATE_HOME=" + filepath.Join(buildHome, "state"),
		"TMPDIR=" + buildHome,
		"TMP=" + buildHome,
		"TEMP=" + buildHome,
		"GOTOOLCHAIN=local",
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		buildEnv = append(buildEnv, key+"="+caches[key])
	}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	binary := filepath.Join(t.TempDir(), executable)
	build := exec.CommandContext(t.Context(), goBinary, "build", "-tags=pig_experimental", "-o", binary, "./cmd/pig")
	build.Dir = filepath.Join("..", "..")
	build.Env = buildEnv
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build development entrypoint: %v\n%s", err, output)
	}

	cases := []struct {
		name         string
		experimental string
		status       int
	}{
		// upstream: packages/coding-agent/test/experimental-cli-entry.test.ts:53.
		{"keeps experimental dispatch in the development entrypoint", "1", 1},
		// upstream: packages/coding-agent/test/experimental-cli-entry.test.ts:60.
		{"falls back to the stable CLI when experiments are disabled", "0", 0},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			command := exec.CommandContext(t.Context(), binary, "server", "--server-id", "invalid", "--version")
			command.Dir = directory
			command.Env = slices.Concat(buildEnv, []string{
				"HOME=" + directory,
				"USERPROFILE=" + directory,
				"PIG_CODING_AGENT_DIR=" + filepath.Join(directory, "pig-agent"),
				"PI_CODING_AGENT_DIR=" + filepath.Join(directory, "agent"),
				"PIG_HOME=" + filepath.Join(directory, "pig"),
				"PI_HOME=" + filepath.Join(directory, "pi"),
				"XDG_CONFIG_HOME=" + filepath.Join(directory, "config"),
				"XDG_CACHE_HOME=" + filepath.Join(directory, "cache"),
				"XDG_DATA_HOME=" + filepath.Join(directory, "data"),
				"XDG_STATE_HOME=" + filepath.Join(directory, "state"),
				"TMPDIR=" + directory,
				"TMP=" + directory,
				"TEMP=" + directory,
				"PI_OFFLINE=1",
				"PI_EXPERIMENTAL=" + tt.experimental,
			})
			var stderr strings.Builder
			command.Stderr = &stderr
			stdout, err := command.Output()
			status := 0
			if err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatalf("run development entrypoint: %v\nstderr: %s", err, stderr.String())
				}
				status = exitError.ExitCode()
			}
			if status != tt.status {
				t.Fatalf("exit status = %d, want %d; stderr: %s", status, tt.status, stderr.String())
			}
			// D63: PiG's version is composite; the entrypoint boundary otherwise matches Pi.
			if tt.experimental == "0" {
				if got := strings.TrimSpace(string(stdout)); got != pigversion.Version {
					t.Fatalf("stdout = %q, want %q", got, pigversion.Version)
				}
				return
			}
			if !strings.Contains(stderr.String(), "Invalid --server-id") {
				t.Fatalf("stderr = %q, want Invalid --server-id", stderr.String())
			}
			if strings.Contains(string(stdout), pigversion.Version) {
				t.Fatalf("experimental dispatch printed the stable version: %q", stdout)
			}
		})
	}
}
