//go:build unix

package models

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Pi runs a `!command` value as Node's `execSync(command, { shell: process.env.SHELL, timeout: 10000, ... })`.
func TestACommandValueRunsUnderTheShellOfTheEnvironment(t *testing.T) {
	shell := filepath.Join(t.TempDir(), "myshell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'shell:%s:%s' \"$1\" \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	if got, ok := ResolveConfigValueUncached("!echo hi", nil); !ok || got != "shell:-c:echo hi" {
		t.Errorf("= %q, %v", got, ok)
	}
}

func TestACommandValueWithoutAShellRunsUnderBinSh(t *testing.T) {
	t.Setenv("SHELL", "")
	os.Unsetenv("SHELL") // t.Setenv restores it afterwards
	if got, ok := ResolveConfigValueUncached("!echo hi", nil); !ok || got != "hi" {
		t.Errorf("= %q, %v", got, ok)
	}
}

func TestACommandValueTakesTheOutputOfAShellOnlySyntax(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	t.Setenv("SHELL", bash)
	if got, ok := ResolveConfigValueUncached(`!x=abc; [[ $x == a* ]] && echo "bash:${x^^}"`, nil); !ok || got != "bash:ABC" {
		t.Errorf("= %q, %v", got, ok)
	}
}

func TestACommandValueIsUndefinedPastTheOutputLimit(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	// Node's maxBuffer is 1 MiB: as many bytes is the value, one more is none.
	if got, ok := ResolveConfigValueUncached("!head -c 1048575 /dev/zero | tr '\\0' a; echo", nil); !ok || len(got) != 1048575 {
		t.Errorf("1 MiB: %d bytes, %v", len(got), ok)
	}
	if got, ok := ResolveConfigValueUncached("!head -c 1048576 /dev/zero | tr '\\0' a; echo", nil); ok {
		t.Errorf("past 1 MiB: %d bytes", len(got))
	}
}

func TestACommandValueEndsAtTheLimitWithItsWholeProcessGroup(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	was := commandTimeout
	commandTimeout = 300 * time.Millisecond
	defer func() { commandTimeout = was }()
	start := time.Now()
	if got, ok := ResolveConfigValueUncached("!sleep 30 & echo started; wait", nil); ok {
		t.Errorf("a command past the limit resolved to %q", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %v", elapsed)
	}
}

// As Node's execSync does, the call waits for a background process that holds the output, and takes what was printed.
func TestACommandValueWaitsForTheOutputToClose(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	start := time.Now()
	if got, ok := ResolveConfigValueUncached("!(sleep 0.4) & echo hi", nil); !ok || got != "hi" {
		t.Errorf("= %q, %v", got, ok)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("took %v: the output was not awaited", elapsed)
	}
}
