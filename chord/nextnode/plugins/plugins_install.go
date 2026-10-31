package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

// Ports packages/chord/src/next-node/plugins/install.ts. Installing a plugin's npm dependencies (slice 7c §5, decision
// "Slice 7c's points" (3)): as coding-agent installs a package's dependencies, `npm install` in the plugin's real directory
// without dev dependencies, host peers or lifecycle scripts; the command overridable.

// InstallOptions is what InstallDependencies takes besides the plugin: the npm command (`["npm"]` by default, e.g.
// `["pnpm"]`). A nil NpmCommand is upstream's undefined (npm); an empty, non-nil one is refused.
type InstallOptions struct {
	NpmCommand []string
}

// installArgs is the install's arguments: production dependencies only, no host peers, no scripts, no audit or funding
// notes.
var installArgs = []string{"install", "--omit=dev", "--legacy-peer-deps", "--ignore-scripts", "--no-audit", "--no-fund"}

// InstallDependencies installs plugin's `dependencies` into its own `node_modules` (`npm install --omit=dev
// --legacy-peer-deps --ignore-scripts --no-audit --no-fund` in its real directory). It blocks until the command ends and
// returns an error carrying the command's output when it fails, and the abort's reason (context.Cause) when ctx aborts
// (the command is sent SIGTERM, as Node's `spawn` signal does).
func InstallDependencies(plugin Plugin, ctx context.Context, options InstallOptions) error {
	command := options.NpmCommand
	if command == nil {
		command = []string{"npm"}
	}
	if len(command) == 0 {
		return errors.New("installDependencies: npmCommand is empty")
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	child := exec.CommandContext(ctx, command[0], append(append([]string{}, command[1:]...), installArgs...)...)
	child.Dir = plugin.Dir
	child.Cancel = func() error {
		if err := child.Process.Signal(syscall.SIGTERM); err != nil {
			return child.Process.Kill()
		}
		return nil
	}
	// One buffer for both streams: the output is in the order it was written.
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	err := child.Run()
	if ctx.Err() != nil && err != nil {
		return context.Cause(ctx)
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err
	}
	// Node's `code` is null for a command a signal ended.
	code := "null"
	if exit.ExitCode() >= 0 {
		code = strconv.Itoa(exit.ExitCode())
	}
	return fmt.Errorf("plugin %q: %s install failed (exit %s): %s", plugin.Name, command[0], code, string(bytes.TrimSpace(output.Bytes())))
}
