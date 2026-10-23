package packagemanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// Ports packages/coding-agent/src/core/package-manager.ts (spawnCommand, runCommand).
// RunPackageProcess inherits child output without retaining it. When the host owns stdout, both streams go to stderr and stdin is ignored. The caller waits for completion before reporting an error or persisting settings.
func RunPackageProcess(dir, name string, args ...string) error {
	cmd := packageCommand(context.Background(), dir, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if codingagent.IsStdoutTakenOver() {
		cmd.Stdin, cmd.Stdout = nil, os.Stderr
	}
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			code := fmt.Sprint(exit.ExitCode())
			if exit.ExitCode() < 0 {
				code = "null"
			}
			return fmt.Errorf("%s %s failed with code %s", name, strings.Join(args, " "), code)
		}
		return err
	}
	return nil
}

// RunPackageCapture bounds metadata queries by Pi's network timeout and keeps their output off the command's inherited streams.
func RunPackageCapture(dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), UpdateCheckNetworkTimeout)
	defer cancel()
	cmd := packageCommand(ctx, dir, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s %s timed out after %dms", name, strings.Join(args, " "), UpdateCheckNetworkTimeout.Milliseconds())
		}
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			output := stderr.String()
			if output == "" {
				output = stdout.String()
			}
			return "", fmt.Errorf("%s %s failed with code %d: %s", name, strings.Join(args, " "), exit.ExitCode(), output)
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// packageCommand starts name as upstream's spawnProcess does. On Windows a git
// child also gets core.longpaths through its environment (GitLongPathsEnv), so
// its argv stays Pi's.
func packageCommand(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := crossspawn.Command(ctx, dir, name, args...)
	if runtime.GOOS == "windows" && name == "git" {
		cmd.Env = GitLongPathsEnv(runtime.GOOS, os.Environ())
	}
	return cmd
}

// GitLongPathsEnv is env with git's core.longpaths enabled for one process on
// Windows, which limits a path to MAX_PATH (260) unless told otherwise; a local
// file:// Git source's checkout path embeds the whole source path and can pass
// it. Git reads the setting from GIT_CONFIG_COUNT/KEY_n/VALUE_n (git 2.31+), so
// the command line stays Pi's and no config file is written; it is appended
// after any entries the environment already carries, and elsewhere env is
// returned unchanged.
// pig additive (D18): upstream rejects file:// sources.
func GitLongPathsEnv(goos string, env []string) []string {
	if goos != "windows" {
		return env
	}
	// Windows names environment variables case-insensitively.
	count, at := 0, -1
	for i, entry := range env {
		if name, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(name, "GIT_CONFIG_COUNT") {
			if n, err := strconv.Atoi(value); err == nil && n >= 0 { //portlint:allow numbers git itself, not Node or Pi, defines GIT_CONFIG_COUNT as an unsigned decimal; this appends to the count the child will read
				count, at = n, i
			}
		}
	}
	out := make([]string, 0, len(env)+3)
	for i, entry := range env {
		if i != at {
			out = append(out, entry)
		}
	}
	return append(out,
		fmt.Sprintf("GIT_CONFIG_KEY_%d=core.longpaths", count),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=true", count),
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", count+1),
	)
}
