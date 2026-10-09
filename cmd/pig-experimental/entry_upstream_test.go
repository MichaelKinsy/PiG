package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func TestDevelopmentCLIEntryUpstream(t *testing.T) {
	t.Parallel()
	binary, buildEnv := buildDevelopmentEntry(t)

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
