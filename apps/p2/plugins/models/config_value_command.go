package models

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// Ports executeWithDefaultShell of plugins/models/config-value.ts: Node's
// `execSync(command, { shell: process.env.SHELL, timeout: 10000, stdio: ["ignore", "pipe", "ignore"] })`. The command
// runs under $SHELL (unlike the coding agent's resolver, which runs /bin/sh), with the process environment, a 10 s limit
// and Node's 1 MiB maxBuffer (past it the value is undefined). As in Node, the call waits for the output to close, so a
// background process that holds it delays the value until it ends or the limit passes; at the limit the whole process
// group is killed.

// commandTimeout is the limit of a command (a variable for the tests).
var commandTimeout = 10 * time.Second

const commandOutputMax = 1024 * 1024

// limitedOutput collects a command's stdout up to the limit, and calls exceeded when more arrives.
type limitedOutput struct {
	data     []byte
	over     bool
	exceeded context.CancelFunc
}

func (o *limitedOutput) Write(p []byte) (int, error) {
	if o.over {
		return len(p), nil
	}
	if len(o.data)+len(p) > commandOutputMax {
		o.over = true
		o.exceeded()
		return len(p), nil
	}
	o.data = append(o.data, p...)
	return len(p), nil
}

// runConfigCommand runs command as Node's execSync does and returns its trimmed output; ok is false when it fails, runs
// too long or prints too much, or prints nothing.
func runConfigCommand(command string) (string, bool) {
	//portlint:allow ctxbackground execSync is synchronous and cannot be cancelled by its caller; the 10 s limit is its own
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch shell, set := os.LookupEnv("SHELL"); {
	case set && shell == "":
		// Node treats an empty `shell` option as none: the command string is the program.
		cmd = linkerexec.Command(command)
	case set:
		cmd = linkerexec.Command(shell, "-c", command)
	case runtime.GOOS == "android":
		cmd = linkerexec.Command("/system/bin/sh", "-c", command)
	default:
		cmd = linkerexec.Command("/bin/sh", "-c", command)
	}
	inGroup(cmd)
	out := &limitedOutput{exceeded: cancel}
	cmd.Stdout = out
	if err := cmd.Start(); err != nil {
		return "", false
	}
	waited := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killGroup(cmd)
		case <-waited:
		}
	}()
	err := cmd.Wait()
	close(waited)
	if err != nil || out.over || ctx.Err() != nil {
		return "", false
	}
	text := ai.TrimJSWhitespace(string(out.data))
	return text, text != ""
}
