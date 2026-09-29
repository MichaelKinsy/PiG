package codingagent

import (
	"strings"
	"testing"
	"time"
)

// Pi awaits Promise.all([runTmuxShow("extended-keys"), runTmuxShow("extended-keys-format")]).
func TestTmuxKeyboardQueriesStartConcurrently(t *testing.T) {
	expected := map[string]bool{"extended-keys": false, "extended-keys-format": false}
	started := make(chan string, len(expected))
	release := make(chan struct{})
	done := make(chan string, 1)
	go func() {
		done <- tmuxKeyboardSetup(func(option string) string {
			started <- option
			<-release
			if option == "extended-keys" {
				return "off"
			}
			return "csi-u"
		})
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for range expected {
		select {
		case option := <-started:
			if _, ok := expected[option]; !ok {
				t.Errorf("unexpected option: %s", option)
			}
			expected[option] = true
		case <-timer.C:
			close(release)
			<-done
			t.Fatal("one query waited for the other's completion")
		}
	}
	close(release)
	if warning := <-done; !strings.Contains(warning, "tmux extended-keys is off") {
		t.Fatalf("warning = %q", warning)
	}
}
