package codingagent

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Upstream handleClipboardPaste inserts clipboard text when the clipboard
// holds no image, and shows nothing: no preview, no status. Interactive mode
// showed "no image in clipboard" and never pasted text (GUARD-08).
func TestClipboardPasteWithoutAnImageInsertsText(t *testing.T) {
	previousGOOS, previousRun := clipboardGOOS, clipboardRun
	t.Cleanup(func() { clipboardGOOS, clipboardRun = previousGOOS, previousRun })
	clipboardGOOS = "darwin"
	withEnv(t, map[string]string{})
	clipboardRun = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "pbpaste" {
			return []byte("pasted text"), nil
		}
		return nil, errors.New("no image")
	}
	m := newSwitchTuiProbe(t)
	m.tuiInst.CancelPendingRender()
	t.Cleanup(m.teardownCurrentTui)

	m.handleClipboardImagePaste()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for m.editor.Text() != "pasted text" {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatalf("editor = %q, want the clipboard text", m.editor.Text())
		}
	}
	m.clipboardReads.Wait()

	if lines := m.chatContainer.Render(80); len(lines) != 0 {
		t.Fatalf("paste added to the transcript: %q", strings.Join(lines, "\n"))
	}
}

// Under WSL the Linux clipboard does not receive Windows screenshots, so
// upstream falls back to reading the Windows clipboard through PowerShell
// (5 s timeout) when Linux has no image (GUARD-08).
func TestReadClipboardImageWSLFallsBackToPowerShell(t *testing.T) {
	withEnv(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	png := []byte("\x89PNG\r\n\x1a\nwindows screenshot")
	pathPattern := regexp.MustCompile(`\$path = '([^']+)'`)
	var calls []string
	previous := clipboardRun
	t.Cleanup(func() { clipboardRun = previous })
	clipboardRun = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name)
		switch name {
		case "wslpath":
			return []byte(args[len(args)-1] + "\n"), nil
		case "powershell.exe":
			match := pathPattern.FindStringSubmatch(args[len(args)-1])
			if match == nil {
				return nil, errors.New("no path in script")
			}
			if err := os.WriteFile(match[1], png, 0o600); err != nil {
				return nil, err
			}
			return []byte("ok\n"), nil
		}
		return nil, errors.New(name + " unavailable")
	}

	data, mime, err := readClipboardImageLinux()
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || string(data) != string(png) {
		t.Fatalf("got mime=%q data=%q (calls %v), want the Windows clipboard image", mime, data, calls)
	}
}
