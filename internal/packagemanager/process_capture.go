package packagemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// capturedOutputTailChars is the end of a captured command's output kept for its error message (package-manager.ts CAPTURED_OUTPUT_TAIL_CHARS).
// It counts UTF-16 code units, as the String.slice(-8000) that keeps it does.
//
//portlint:allow numbers Pi's own constant: the length of the output tail a captured command's error carries
const capturedOutputTailChars = 8000

// gitCredentialsHint is added to the error of a git command that failed because it could not ask for credentials (package-manager.ts GIT_CREDENTIALS_HINT).
// pig divergence (D2): the message names pig where Pi's names pi ("Product name in messages").
var gitCredentialsHint = "git cannot ask for a password or passphrase while " + codingagent.AppName + " runs. Set up a git credential helper or an ssh agent, or install the package once from the shell with `" + codingagent.AppName + " install <source>`."

// gitPromptFailureNeedles are the lower-case texts of Pi's GIT_PROMPT_FAILURE, /terminal prompts disabled|could not read (Username|Password)|Permission denied \(publickey|Host key verification failed|passphrase/i.
var gitPromptFailureNeedles = []string{
	"terminal prompts disabled",
	"could not read username",
	"could not read password",
	"permission denied (publickey",
	"host key verification failed",
	"passphrase",
}

// isGitPromptFailure reports whether output is from git, or from ssh run by git, that failed because it could not ask for credentials (package-manager.ts GIT_PROMPT_FAILURE).
// A JavaScript /i pattern without the u flag folds only through toUpperCase and never maps a character outside ASCII to one inside it, so only the ASCII letters fold here:
// Go's (?i) would also match the Kelvin sign for k and the long s for s.
func isGitPromptFailure(output string) bool {
	folded := []byte(output)
	for i, b := range folded {
		if b >= 'A' && b <= 'Z' {
			folded[i] = b + ('a' - 'A')
		}
	}
	text := string(folded)
	for _, needle := range gitPromptFailureNeedles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// nonInteractiveGitEnv makes git and ssh fail instead of prompting on the terminal, which a captured command cannot answer (package-manager.ts getNonInteractiveGitEnv).
// A user-defined GIT_SSH_COMMAND is kept: it may be needed to reach the host at all.
func nonInteractiveGitEnv(env []string) []string {
	sameName := func(entry, name string) bool {
		key, _, _ := strings.Cut(entry, "=")
		if runtime.GOOS == "windows" {
			return strings.EqualFold(key, name)
		}
		return key == name
	}
	sshCommand, userSSH := "", false
	out := make([]string, 0, len(env)+2)
	for _, entry := range env {
		switch {
		case sameName(entry, "GIT_TERMINAL_PROMPT"):
			continue
		case sameName(entry, "GIT_SSH_COMMAND"):
			if !userSSH {
				_, sshCommand, _ = strings.Cut(entry, "=")
				userSSH = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !userSSH {
		sshCommand = "ssh -o BatchMode=yes"
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND="+sshCommand)
}

// runPackageProcessCaptured runs a command without touching the terminal: its output is piped and git and ssh cannot prompt. Only the end of the output is kept, for the error message (package-manager.ts runCommandCaptured).
// Like waitForChildProcess it waits for the child and then for its pipes to end or to fall idle, so a background process an npm script left holding them does not block the call.
// Ports packages/coding-agent/src/core/package-manager.ts (runCommandCaptured, CAPTURED_OUTPUT_TAIL_CHARS, GIT_CREDENTIALS_HINT, GIT_PROMPT_FAILURE).
func runPackageProcessCaptured(dir, name string, args ...string) error {
	//portlint:allow ctxbackground Pi's runCommandCaptured (package-manager.ts:2995, Pi b513b64d8ac8) takes only {cwd}: no AbortSignal, so no caller can cancel the child there either (the same as spawnCommand/runCommand, whose RunPackageProcess sibling is baselined)
	cmd := packageCommand(context.Background(), dir, name, args...)
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = nonInteractiveGitEnv(env)
	stdoutRead, stdoutWrite, err := childwait.NewPipe()
	if err != nil {
		return err
	}
	stderrRead, stderrWrite, err := childwait.NewPipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, stdoutWrite, stderrWrite
	prepareCaptureGroup(cmd)
	startErr := cmd.Start()
	// The child holds its own ends now; the parent's copies must close so that the pipes end with the child's tree.
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	if startErr != nil {
		_ = stdoutRead.Close()
		_ = stderrRead.Close()
		return startErr
	}
	attachCaptureGroup(cmd.Process)
	defer releaseCaptureGroup(cmd.Process)

	output := ""
	pipes := childwait.Start([]*os.File{stdoutRead, stderrRead}, func(_ int, chunk []byte) {
		output = jsstring.Slice(jsstring.Concat(output, jsstring.FromUTF8(chunk)), -capturedOutputTailChars)
	}, nil)
	waitErr := cmd.Wait()
	pipes.Wait(func() bool { return captureGroupMayHoldOutput(cmd.Process) })
	state := cmd.ProcessState
	if state == nil {
		return waitErr
	}
	if state.ExitCode() == 0 {
		return nil
	}
	code := "null"
	if state.ExitCode() >= 0 {
		code = fmt.Sprint(state.ExitCode())
	}
	tail := jsstring.Trim(output)
	message := fmt.Sprintf("%s %s failed with code %s", name, strings.Join(args, " "), code)
	if tail != "" {
		message += ":\n" + tail
	}
	if name == "git" && isGitPromptFailure(tail) {
		message += "\n" + gitCredentialsHint
	}
	return errors.New(string(jsstring.ToUTF8(message)))
}
