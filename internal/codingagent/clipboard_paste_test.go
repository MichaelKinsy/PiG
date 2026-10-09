package codingagent

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream handleClipboardPaste inserts clipboard text when the clipboard
// holds no image, and shows nothing: no preview, no status. Interactive mode
// showed "no image in clipboard" and never pasted text (GUARD-08).
func TestClipboardPasteWithoutAnImageInsertsText(t *testing.T) {
	withEnv(t, map[string]string{})
	useClipboardTextTestSeams(t, "darwin", map[string]string{},
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no image") },
		func() *tui.NativeClipboard {
			return &tui.NativeClipboard{
				GetImage: func(context.Context) ([]byte, bool, error) { return nil, true, nil },
				GetText:  func(context.Context) (*string, bool, error) { return new("pasted text"), true, nil },
			}
		},
	)
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

// Termux reports platform "android" (#10391): Ctrl+V inserts the text that
// termux-clipboard-get returns.
func TestClipboardPasteInsertsTermuxTextOnAndroid(t *testing.T) {
	withEnv(t, map[string]string{})
	var commands []string
	useClipboardTextTestSeams(t, "android", map[string]string{"TERMUX_VERSION": "0.119"},
		func(_ context.Context, name string, _ ...string) ([]byte, error) {
			commands = append(commands, name)
			return []byte("termux text"), nil
		},
		func() *tui.NativeClipboard { return nil },
	)
	m := newSwitchTuiProbe(t)
	m.tuiInst.CancelPendingRender()
	t.Cleanup(m.teardownCurrentTui)

	m.handleClipboardImagePaste()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for m.editor.Text() != "termux text" {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline.C:
			t.Fatalf("editor = %q, want the Termux clipboard text", m.editor.Text())
		}
	}
	m.clipboardReads.Wait()
	if len(commands) != 1 || commands[0] != "termux-clipboard-get" {
		t.Fatalf("commands = %v, want only termux-clipboard-get", commands)
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
