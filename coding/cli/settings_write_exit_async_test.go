package cli

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// exitGatedSettingsStorage is a settings storage whose write waits in entered and reports stored once it is done.
type exitGatedSettingsStorage struct {
	entered func()
	stored  func()
}

func (s *exitGatedSettingsStorage) WithLock(_ string, fn func(current *string) (*string, error)) error {
	next, err := fn(nil)
	if err != nil || next == nil {
		return err
	}
	s.entered()
	s.stored()
	return nil
}

// A settings write that a setter on another goroutine is performing when the process exits is finished first, as flush() lets a caller do in Pi (settings-manager.ts:670-672): exitProcess waits for it before it writes the profiles and exits. The write is released 100 ms after the exit has passed the step before the wait, so an exit that does not wait ends without it.
func TestExitProcessWaitsForASettingsWriteInProgress(t *testing.T) {
	const helper = "PIG_TEST_SETTINGS_WRITE_EXIT_HELPER"
	if os.Getenv(helper) != "" {
		path := os.Getenv(helper)
		appendTrace := func(text string) {
			file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				panic(err)
			}
			if _, err := file.WriteString(text + "\n"); err != nil {
				panic(err)
			}
			if err := file.Close(); err != nil {
				panic(err)
			}
		}
		entered, release := make(chan struct{}), make(chan struct{})
		manager := codingagent.NewSettingsManagerFromStorage(&exitGatedSettingsStorage{
			entered: func() { close(entered); <-release },
			stored:  func() { appendTrace("settings write stored") },
		}, codingagent.SettingsManagerCreateOptions{})
		go func() { _ = manager.SetTheme("dark") }()
		<-entered
		stopModelServices = func() {}
		stopStartupExtensions = func() { time.AfterFunc(100*time.Millisecond, func() { close(release) }) }
		stopProfiles = func() { appendTrace("profiles stopped") }
		exitProcess(7)
		return
	}
	path := filepath.Join(t.TempDir(), "exit-order.txt")
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExitProcessWaitsForASettingsWriteInProgress$")
	command.Env = append(os.Environ(), helper+"="+path)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v output=%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "settings write stored\nprofiles stopped\n" {
		t.Fatalf("exit order=%q, want the settings write stored before the profiles stop", data)
	}
}
