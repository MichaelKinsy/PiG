package codingagent

import (
	"context"
	"errors"
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

// Ports packages/coding-agent/test/clipboard-image-native-errors.test.ts:18-31. The native getImage call itself rejects; no generic reader or temporary-file failure substitutes for that boundary.
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
		editor: editor, tuiInst: renderer, clipboardCtx: t.Context(),
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
	if textReads != 0 || len(editorCalls.inserts) != 0 || renderer.requests != 0 {
		t.Fatalf("text reads=%d editor insertions=%q render requests=%d", textReads, editorCalls.inserts, renderer.requests)
	}
}
