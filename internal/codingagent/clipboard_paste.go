// Clipboard paste handler.
//
// Wired to Ctrl+V (app.clipboard.pasteImage). Mirrors upstream
// handleClipboardPaste: an image is written to a temp file whose path is
// inserted at the cursor; otherwise clipboard text is inserted. Errors are
// silently ignored, and nothing else is shown.

package codingagent

import "os"

// handleClipboardImagePaste schedules the image-then-text clipboard operation
// on the renderer-owned worker set. It posts editor and render mutations back
// to the owner loop.
func (m *InteractiveMode) handleClipboardImagePaste() {
	if m.clipboardCtx == nil || m.clipboardReads == nil || m.clipboardCtx.Err() != nil {
		return
	}
	ctx, reads := m.clipboardCtx, m.clipboardReads
	reads.Go(func() {
		bytes, mime, err := ReadClipboardImageContext(ctx)
		if err != nil {
			return
		}
		if len(bytes) == 0 {
			text := readClipboardText(ctx)
			if text == "" || ctx.Err() != nil {
				return
			}
			m.runOnMain(ctx, func() {
				if ctx.Err() == nil {
					m.editor.InsertTextAtCursor(text)
					m.requestRender()
				}
			})
			return
		}
		path, err := SaveClipboardImageToTempFile(bytes, mime)
		if err != nil {
			return
		}
		m.runOnMain(ctx, func() {
			if ctx.Err() != nil {
				_ = os.Remove(path)
				return
			}
			m.editor.InsertTextAtCursor(path)
			m.requestRender()
		})
		if ctx.Err() != nil {
			_ = os.Remove(path)
		}
	})
}
