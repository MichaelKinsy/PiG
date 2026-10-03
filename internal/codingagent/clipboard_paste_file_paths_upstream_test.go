package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// pasteFilePathsHarness runs .upstream/v0.99.1/packages/coding-agent/test/clipboard-paste-file-paths.test.ts's paste through the real InteractiveMode worker and owner-loop hop. Upstream mocks readClipboardFilePaths, readClipboardImage, and readClipboardText; here the same three reads come from the native clipboard seam, so the image and text reads are counted at that boundary.
type pasteFilePathsHarness struct {
	m                     *InteractiveMode
	imageReads, textReads int
}

func newPasteFilePathsHarness(t *testing.T, filePaths func(context.Context) ([]string, bool, error)) *pasteFilePathsHarness {
	t.Helper()
	withEnv(t, map[string]string{})
	h := &pasteFilePathsHarness{}
	useClipboardTextTestSeams(t, "darwin", map[string]string{},
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") },
		func() *tui.NativeClipboard {
			return &tui.NativeClipboard{
				GetFilePaths: filePaths,
				GetImage: func(context.Context) ([]byte, bool, error) {
					h.imageReads++
					return upstreamClipboardPNG, true, nil
				},
				GetText: func(context.Context) (*string, bool, error) {
					h.textReads++
					return nil, true, nil
				},
			}
		},
	)
	h.m = newSwitchTuiProbe(t)
	h.m.tuiInst.CancelPendingRender()
	t.Cleanup(h.m.teardownCurrentTui)
	return h
}

// paste starts the paste and joins it, running each owner-loop callback it posts, like awaiting Pi's handleClipboardPaste.
func (h *pasteFilePathsHarness) paste(t *testing.T) {
	t.Helper()
	h.m.handleClipboardImagePaste()
	joined := make(chan struct{})
	go func() {
		h.m.clipboardReads.Wait()
		close(joined)
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case task := <-h.m.uiTaskCh:
			task()
		case <-joined:
			for len(h.m.uiTaskCh) > 0 {
				(<-h.m.uiTaskCh)()
			}
			return
		case <-deadline.C:
			t.Fatal("clipboard paste did not finish")
		}
	}
}

func (h *pasteFilePathsHarness) chat() string {
	return strings.Join(h.m.chatContainer.Render(200), "\n")
}

func constantFilePaths(paths ...string) func(context.Context) ([]string, bool, error) {
	return func(context.Context) ([]string, bool, error) { return paths, true, nil }
}

// moveCursorTo places the editor cursor at a UTF-16 column of the first line, after SetText left it at the end.
func moveCursorTo(t *testing.T, editor *tui.Editor, col int) {
	t.Helper()
	for editor.GetCursor().Col > col {
		editor.HandleInput("\x1b[D")
	}
	if got := editor.GetCursor(); got.Line != 0 || got.Col != col {
		t.Fatalf("cursor=%+v, want column %d", got, col)
	}
}

// .upstream/v0.99.1/packages/coding-agent/test/clipboard-paste-file-paths.test.ts:22.
func TestClipboardPasteFilePaths(t *testing.T) {
	// clipboard-paste-file-paths.test.ts:22 (regression test for #9999).
	t.Run("Finder file paths take precedence over their icon image", func(t *testing.T) {
		filePaths := []string{"/tmp/screenshot.png", "/tmp/My Photos/photo.png"}
		h := newPasteFilePathsHarness(t, constantFilePaths(filePaths...))
		h.paste(t)
		if got, want := h.m.editor.Text(), strings.Join(filePaths, "\n"); got != want {
			t.Fatalf("editor=%q, want %q", got, want)
		}
		if h.imageReads != 0 {
			t.Fatalf("image reads=%d, want none after file paths", h.imageReads)
		}
	})

	// clipboard-paste-file-paths.test.ts:46.
	t.Run("clipboard file paths containing terminal control characters are rejected", func(t *testing.T) {
		h := newPasteFilePathsHarness(t, constantFilePaths("/tmp/photo\x1b]0;unsafe\x07.png"))
		h.paste(t)
		if got := h.m.editor.Text(); got != "" {
			t.Fatalf("editor=%q, want no insertion", got)
		}
		if h.imageReads != 0 {
			t.Fatalf("image reads=%d, want none", h.imageReads)
		}
		if want := "Error: Failed to paste from clipboard: Clipboard file path contains control characters"; strings.Count(h.chat(), want) != 1 {
			t.Fatalf("chat=%q, want %q exactly once", h.chat(), want)
		}
	})

	// clipboard-paste-file-paths.test.ts:68.
	t.Run("native file-path errors are shown without falling through to the icon image", func(t *testing.T) {
		h := newPasteFilePathsHarness(t, func(context.Context) ([]string, bool, error) {
			return nil, true, errors.New("Native clipboard file read failed")
		})
		h.paste(t)
		if got := h.m.editor.Text(); got != "" {
			t.Fatalf("editor=%q, want no insertion", got)
		}
		if h.imageReads != 0 {
			t.Fatalf("image reads=%d, want none", h.imageReads)
		}
		if want := "Error: Failed to paste from clipboard: Native clipboard file read failed"; strings.Count(h.chat(), want) != 1 {
			t.Fatalf("chat=%q, want %q exactly once", h.chat(), want)
		}
	})

	// clipboard-paste-file-paths.test.ts:89.
	for _, tc := range []struct {
		description, editorText string
		cursorCol               int
	}{{"punctuation", "Review:", 7}, {"Unicode text", "確認", 2}} {
		t.Run("clipboard file paths are separated from preceding "+tc.description, func(t *testing.T) {
			h := newPasteFilePathsHarness(t, constantFilePaths("/tmp/photo.png"))
			h.m.editor.SetText(tc.editorText)
			moveCursorTo(t, h.m.editor, tc.cursorCol)
			h.paste(t)
			if got, want := h.m.editor.Text(), tc.editorText+" /tmp/photo.png"; got != want {
				t.Fatalf("editor=%q, want %q", got, want)
			}
			if h.imageReads != 0 {
				t.Fatalf("image reads=%d, want none", h.imageReads)
			}
		})
	}

	// clipboard-paste-file-paths.test.ts:114. Upstream's mock context sets `isBashMode` directly on text "catDEST"; Go derives bash mode from the editor's leading "!", so the same text follows a "!" and the cursor sits at the same offset within it.
	t.Run("bash mode shell-quotes file paths and inserts them as arguments", func(t *testing.T) {
		h := newPasteFilePathsHarness(t, constantFilePaths("/tmp/My Photos/photo.png", "/tmp/$(touch hacked).png", "/tmp/plain.png"))
		h.m.editor.SetText("!catDEST")
		moveCursorTo(t, h.m.editor, 4)
		h.paste(t)
		if got, want := h.m.editor.Text(), "!cat '/tmp/My Photos/photo.png' '/tmp/$(touch hacked).png' /tmp/plain.png DEST"; got != want {
			t.Fatalf("editor=%q, want %q", got, want)
		}
		if h.imageReads != 0 {
			t.Fatalf("image reads=%d, want none", h.imageReads)
		}
	})
}

// A file-path read that reports unsupported (upstream `undefined`) or no files (`null`) falls through to the image and then the text read, so paste keeps its pre-0.99 behavior on Linux and Windows.
func TestClipboardPasteWithoutFilePathsFallsThroughToImage(t *testing.T) {
	for name, filePaths := range map[string]func(context.Context) ([]string, bool, error){
		"unsupported": nil,
		"null":        func(context.Context) ([]string, bool, error) { return nil, true, nil },
		"empty":       func(context.Context) ([]string, bool, error) { return []string{}, true, nil },
	} {
		t.Run(name, func(t *testing.T) {
			h := newPasteFilePathsHarness(t, filePaths)
			h.paste(t)
			if h.imageReads != 1 || !strings.Contains(h.m.editor.Text(), "clipboard-") {
				t.Fatalf("image reads=%d editor=%q, want the image temp path", h.imageReads, h.m.editor.Text())
			}
		})
	}
}
