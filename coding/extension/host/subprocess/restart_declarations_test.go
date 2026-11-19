package subprocess

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// waitDeclarationGeneration waits until the recovered member holds the Extension the runner held and its command reports want.
func waitDeclarationGeneration(t *testing.T, h *Host, name string, held *extension.Extension, want string) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for sleep := 10 * time.Millisecond; ; sleep = min(sleep*2, 200*time.Millisecond) {
		h.mu.Lock()
		me := h.exts[name]
		h.mu.Unlock()
		if me != nil && me.ext == held {
			if command, ok := held.Command("probe"); ok && command.Description == want {
				return
			}
		}
		if time.Now().After(deadline) {
			command, _ := held.Command("probe")
			var notices []string
			if log, found := crashNoticeLogs.Load(h); found {
				log := log.(*crashNotices)
				log.mu.Lock()
				notices = append(notices, log.notices...)
				log.mu.Unlock()
			}
			t.Fatalf("%s: restarted command description = %q, want %q (same Extension: %t; crash notices: %q)", name, command.Description, want, me != nil && me.ext == held, notices)
		}
		time.Sleep(sleep)
	}
}

func assertDeclarationGeneration(t *testing.T, ext *extension.Extension, want string, tool bool) {
	t.Helper()
	var got []string
	for _, command := range ext.RegisteredCommands() {
		if command.Name == "probe" {
			got = append(got, "command "+command.Description)
		}
	}
	for _, flag := range ext.RegisteredFlags() {
		got = append(got, fmt.Sprintf("flag %s %v", flag.Description, flag.Default))
	}
	for _, shortcut := range ext.RegisteredShortcuts() {
		got = append(got, "shortcut "+shortcut.Description)
	}
	expected := []string{"command " + want, "flag " + want + " " + want, "shortcut " + want}
	if tool {
		for _, registered := range ext.RegisteredTools() {
			got = append(got, "tool "+registered.Definition.Description)
		}
		expected = append(expected, "tool "+want)
	}
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("declarations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(expected, "\n"))
	}
}
