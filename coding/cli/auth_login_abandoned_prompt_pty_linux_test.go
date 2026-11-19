//go:build linux

package cli

// pi: packages/coding-agent/src/cli/auth-terminal.ts (readSecret and the login's abort, on a pseudo-terminal)

import (
	"context"
	"io"
	"os"
	"testing"
)

// readSecretTerminal asks its secret through readSecret on a pseudo-terminal and prints the login's error lines to the same output, so the order of the two is the
// order of the bytes a user sees.
type readSecretTerminal struct {
	*scriptedTerminal
	slave  *os.File
	cancel context.CancelFunc
	out    *syncBuffer
}

func (t *readSecretTerminal) Secret(ctx context.Context, message string) (string, error) {
	return readSecret(ctx, t.slave, t.out, message, t.cancel)
}

func (t *readSecretTerminal) Error(line string) { _, _ = io.WriteString(t.out, line+"\n") }

// Ctrl+C in the secret prompt of `pig auth login --api-key`, with the real readSecret on a pseudo-terminal behind a runtime that abandons the prompt on cancel: when
// the command returns, the terminal is cooked again, and the output is the prompt, the newline of finish(), then "Login cancelled.", as Pi's abort listener orders them.
func TestAuthLoginCtrlCInTheSecretPromptReturnsAfterTheTerminalIsCooked(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), authLoginTestProviderID))
	override := loginRuntimeOverride{AuthLoginRuntime: runtime, login: abandoningLogin}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out syncBuffer
	terminal := &readSecretTerminal{scriptedTerminal: newScriptedTerminal(), slave: slave, cancel: cancel, out: &out}
	done := make(chan int, 1)
	go func() {
		done <- runAuthLoginForTest(ctx, []string{authLoginTestProviderID, "--api-key"}, override, terminal, nonexistentAgentDir)
	}()
	waitForRawMode(t, slave)
	writeMaster(t, master, "\x03")
	if code := <-done; code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("the command returned with the terminal still raw: %#v, was %#v", after, cooked)
	}
	if got, want := out.String(), "Enter Login Test API key: \nLogin cancelled.\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}
