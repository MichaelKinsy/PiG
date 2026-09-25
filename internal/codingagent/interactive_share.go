package codingagent

import (
	"context"
	"errors"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: tui/src/components/loader.ts:DEFAULT_INTERVAL_MS
const shareLoaderFrame = 80 * time.Millisecond

var errShareCancelled = errors.New("share cancelled")

// shareSessionWithLoader keeps the privacy notice in the transcript and runs
// the network upload off the input loop behind an Escape-cancellable loader.
func (m *InteractiveMode) shareSessionWithLoader(parent context.Context, session *Session, state ShareState, showStatus func(string)) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	if m.chatContainer == nil || m.editorContainer == nil || m.editor == nil || m.tuiInst == nil {
		return shareSession(parent, session, state, shareGatewayURL(), nil, showStatus)
	}

	// pig divergence (D64): keep PiG's gateway privacy notice in transcript history.
	m.appendChatBlock(tui.NewText(tui.ActiveTheme().Muted + sharePrivacyNotice + "\x1b[0m"))
	loader := tui.NewBorderedLoader("Uploading session...", true)
	uploadCtx, cancel := context.WithCancel(parent)
	stopLoaderCancel := context.AfterFunc(loader.CancellableContext().Context(), cancel)
	defer func() {
		stopLoaderCancel()
		cancel()
	}()

	m.editorContainer.SetChildren(loader)
	m.tuiInst.Render()
	defer func() {
		loader.Dispose()
		m.editorContainer.SetChildren(m.editor)
		m.tuiInst.RequestRender()
	}()

	inputCh, releaseInput := m.acquireModalInputChannel()
	defer releaseInput()

	type result struct {
		status string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		status, err := shareSession(uploadCtx, session, state, shareGatewayURL(), nil, func(string) {})
		done <- result{status: status, err: err}
	}()

	ticker := time.NewTicker(shareLoaderFrame)
	defer ticker.Stop()
	for {
		select {
		case buf := <-inputCh:
			for _, chunk := range dropKeyReleases(loader, []string{string(buf)}) {
				loader.HandleInput(chunk)
			}
			m.tuiInst.Render()
		case <-ticker.C:
			loader.NextFrame()
			m.tuiInst.Render()
		case outcome := <-done:
			if uploadCtx.Err() != nil {
				return "", errShareCancelled
			}
			return outcome.status, outcome.err
		}
	}
}
