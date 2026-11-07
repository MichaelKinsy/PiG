// Clipboard paste handler.
//
// Wired to Ctrl+V (app.clipboard.pasteImage). Mirrors upstream
// handleClipboardPaste (interactive-mode.ts:3049): file paths, such as a
// Finder file copy, are inserted at the cursor; otherwise an image is written
// to a temp file whose path is inserted; otherwise clipboard text is inserted.
// A failed read or write is reported as an error line and inserts nothing.

package codingagent

import (
	"errors"
	"os"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// clipboardFilePathsInsertion is the text handleClipboardPaste inserts for clipboard file paths: shell-quoted arguments in bash mode, else one path per line, padded with a space where the cursor touches a non-space character. cursor.Col counts UTF-16 code units, as upstream's does.
func clipboardFilePathsInsertion(filePaths []string, bashMode bool, editorText string, cursor tui.EditorCursor) (string, error) {
	for _, filePath := range filePaths {
		if strings.IndexFunc(filePath, func(r rune) bool { return unicode.Is(unicode.Cc, r) }) >= 0 {
			return "", errors.New("Clipboard file path contains control characters")
		}
	}
	var paths string
	if bashMode {
		quoted := make([]string, len(filePaths))
		for i, filePath := range filePaths {
			quoted[i] = quoteIfNeeded(filePath)
		}
		paths = strings.Join(quoted, " ")
	} else {
		paths = strings.Join(filePaths, "\n")
	}

	var currentLine []uint16
	if lines := strings.Split(editorText, "\n"); cursor.Line >= 0 && cursor.Line < len(lines) {
		currentLine = jsstring.ToUTF16(lines[cursor.Line])
	}
	// A code unit at index i, or none when i is out of range (upstream indexes into a string).
	spaceless := func(index int) bool {
		return index >= 0 && index < len(currentLine) && !widthx.IsJSSpace(rune(currentLine[index]))
	}
	leadingSpace, trailingSpace := "", ""
	if spaceless(cursor.Col - 1) {
		leadingSpace = " "
	}
	if spaceless(cursor.Col) {
		trailingSpace = " "
	}
	return leadingSpace + paths + trailingSpace, nil
}

// handleClipboardImagePaste schedules the file-path, image, then text clipboard operation on the renderer-owned worker set. It posts editor and render mutations back to the owner loop.
func (m *InteractiveMode) handleClipboardImagePaste() {
	if m.clipboardCtx == nil || m.clipboardReads == nil || m.clipboardCtx.Err() != nil {
		return
	}
	ctx, reads := m.clipboardCtx, m.clipboardReads
	fail := func(err error) {
		if ctx.Err() != nil {
			return
		}
		m.runOnMain(ctx, func() {
			if ctx.Err() == nil {
				m.showError("Failed to paste from clipboard: " + err.Error())
			}
		})
	}
	reads.Go(func() {
		filePaths, err := readClipboardFilePaths(ctx)
		if err != nil {
			fail(err)
			return
		}
		if filePaths != nil {
			m.runOnMain(ctx, func() {
				if ctx.Err() != nil {
					return
				}
				text, err := clipboardFilePathsInsertion(filePaths, m.editor.IsBashMode(), m.editor.Text(), m.editor.GetCursor())
				if err != nil {
					m.showError("Failed to paste from clipboard: " + err.Error())
					return
				}
				m.editor.InsertTextAtCursor(text)
				m.requestRender()
			})
			return
		}
		bytes, mime, err := ReadClipboardImageContext(ctx)
		if err != nil {
			fail(err)
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
			fail(err)
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
