package codingagent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type clipboardNativeErrorEditorSpy struct {
	tui.EditorRemote
	inserts []string
}

func (s *clipboardNativeErrorEditorSpy) InsertTextAtCursor(text string) {
	s.inserts = append(s.inserts, text)
}

type clipboardNativeErrorRendererSpy struct {
	tui.Renderer
	requests int
}

func (s *clipboardNativeErrorRendererSpy) RequestRender() { s.requests++ }

// Ports .upstream/v0.99.1/packages/coding-agent/test/clipboard-image-native-errors.test.ts:22-39. The native getImage call itself rejects; no generic reader or temporary-file failure substitutes for that boundary. Upstream's mock context supplies showError and expects no requestRender; Go's showError is the real one, which requests exactly one render for its own chat line, so that one request is the error's.
func TestNativeImageErrorsAbortPasteWithoutReadingTextOrChangingEditor(t *testing.T) {
	imageReads, textReads := 0, 0
	useClipboardTextTestSeams(t, "linux", map[string]string{"TERMUX_VERSION": ""},
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unavailable") },
		func() *tui.NativeClipboard {
			return &tui.NativeClipboard{
				GetImage: func(context.Context) ([]byte, bool, error) {
					imageReads++
					return nil, true, errors.New("Native clipboard operation failed")
				},
				GetText: func(context.Context) (*string, bool, error) {
					textReads++
					return nil, true, nil
				},
			}
		},
	)
	editorCalls := &clipboardNativeErrorEditorSpy{}
	editor := tui.NewEditor()
	editor.SetRemote(editorCalls)
	renderer := &clipboardNativeErrorRendererSpy{}
	m := &InteractiveMode{
		editor: editor, tuiInst: renderer, chatContainer: tui.NewContainer(), clipboardCtx: t.Context(),
		clipboardReads: &sync.WaitGroup{}, uiTaskCh: make(chan func(), 1),
	}
	m.handleClipboardImagePaste()
	m.clipboardReads.Wait()
	// Awaiting Pi's paste includes its UI effects. Drain the joined Go operation's owner-loop callbacks before checking the same observers.
	for len(m.uiTaskCh) > 0 {
		(<-m.uiTaskCh)()
	}
	if imageReads != 1 {
		t.Fatalf("native image reads=%d, want the one paste operation to reach the failing native call", imageReads)
	}
	if textReads != 0 || len(editorCalls.inserts) != 0 || renderer.requests != 1 {
		t.Fatalf("text reads=%d editor insertions=%q render requests=%d", textReads, editorCalls.inserts, renderer.requests)
	}
	if want, got := "Error: Failed to paste from clipboard: Native clipboard operation failed", strings.Join(m.chatContainer.Render(200), "\n"); strings.Count(got, want) != 1 {
		t.Fatalf("chat=%q, want %q exactly once", got, want)
	}
}
