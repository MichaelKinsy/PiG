package codingagent

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// sendModalKey delivers key to the open modal. The modal reads one key at a
// time, so when a send returns the previous key has been handled. The route
// is looked up again on every attempt because a modal that closes and
// reopens installs a new channel.
func sendModalKey(t *testing.T, m *InteractiveMode, key string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if input, _ := m.modalRoute(); input != nil {
			select {
			case input <- []byte(key):
				return
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no modal took %q", key)
}

// Upstream SettingsList applies a change through onChange and stays open
// (settings-list.ts:264-291), and showSettingsSelector prints nothing for it
// (interactive-mode.ts:4744-4990), so the search and the selected row stay as
// they were.
func TestSettingsChangeAppliesInPlace(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	m.opts.SettingsManager = NewSettingsManager(t.TempDir(), t.TempDir())
	sc := m.buildSlashContext(t.Context())
	done := make(chan error, 1)
	go func() { done <- settingsHandler(sc) }()
	// The final no-op key proves Enter was fully handled.
	for _, key := range []string{"B", "l", "o", "c", "k", "\r", "\x1b[C"} {
		sendModalKey(t, m, key)
	}
	if !m.opts.SettingsManager.GetBlockImages() {
		t.Fatal("Block images was not saved")
	}
	if chat := stripANSI(strings.Join(m.chatContainer.Render(100), "\n")); strings.Contains(chat, "Block images") {
		t.Fatalf("the change printed to the transcript:\n%s", chat)
	}
	list := stripANSI(strings.Join(m.editorContainer.Render(100), "\n"))
	if row := regexp.MustCompile(`→ Block images +true`); !strings.Contains(list, "> Block") || !row.MatchString(list) {
		t.Fatalf("the list did not stay open on the searched row:\n%s", list)
	}
	sendModalKey(t, m, "\x1b")
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not close /settings")
	}
}
