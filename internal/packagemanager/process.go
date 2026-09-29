package packagemanager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// Ports packages/coding-agent/src/core/package-manager.ts (spawnCommand, runCommand).
// RunPackageProcess inherits child output without retaining it. When the host owns stdout, both streams go to stderr and stdin is ignored. The caller waits for completion before reporting an error or persisting settings.
func RunPackageProcess(dir, name string, args ...string) error {
	cmd := crossspawn.Command(context.Background(), dir, name, args...)
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
	cmd := crossspawn.Command(ctx, dir, name, args...)
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
