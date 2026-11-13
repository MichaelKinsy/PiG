package coding

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"testing"
	"time"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// closeGatedSettingsStorage is a settings storage whose write waits in entered.
type closeGatedSettingsStorage struct{ entered func() }

func (s *closeGatedSettingsStorage) WithLock(_ string, fn func(current *string) (*string, error)) error {
	next, err := fn(nil)
	if err != nil || next == nil {
		return err
	}
	s.entered()
	return nil
}

// The Runtime a mode exits through waits for a settings write that a setter on another goroutine is performing, as flush() lets a caller do in Pi (settings-manager.ts:670-672), before it retires its resources.
func TestRuntimeCloseWaitsForASettingsWriteInProgress(t *testing.T) {
	runtime, _, _, respond := rotatingOAuthRuntime(t)
	defer respond()
	entered, release := make(chan struct{}), make(chan struct{})
	manager := icodingagent.NewSettingsManagerFromStorage(&closeGatedSettingsStorage{entered: func() { close(entered); <-release }}, icodingagent.SettingsManagerCreateOptions{})
	set := make(chan error, 1)
	go func() { set <- manager.SetTheme("dark") }()
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- runtime.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned %v while a settings write was in progress", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-set; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the settings write finished")
	}
}
