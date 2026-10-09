package codingagent

import (
	"context"
	"runtime"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// clipboardTextTimeout bounds each clipboard text command, matching upstream
// readClipboardText's 5 s per-command timeout.
// upstream: coding-agent/src/utils/clipboard.ts:timeoutMs
const clipboardTextTimeout = 5 * time.Second

// clipboardGOOS is the platform readClipboardText reads for; tests override it.
var clipboardGOOS = runtime.GOOS

// nativeClipboardText reads text through the native helper. A nil result represents null or undefined.
type nativeClipboardText func(context.Context) (*string, error)

// readClipboardText returns the system clipboard text, or "" when there is
// none or it cannot be read. Mirrors upstream readClipboardText
// (utils/clipboard.ts): termux-clipboard-get runs under Termux on any platform;
// on Linux wl-paste, then xclip and xsel follow, all before the native
// helper's getText, which is the only reader on macOS and Windows.
func readClipboardText(parent context.Context) string {
	var commands [][]string
	// Termux reports its platform as android, not linux (#10391).
	if clipboardEnv("TERMUX_VERSION") != "" {
		commands = append(commands, []string{"termux-clipboard-get"})
	}
	if clipboardGOOS == "linux" {
		if clipboardEnv("WAYLAND_DISPLAY") != "" {
			commands = append(commands, []string{"wl-paste", "--no-newline", "--type", "text"})
		}
		if clipboardEnv("DISPLAY") != "" {
			commands = append(commands, []string{"xclip", "-selection", "clipboard", "-out"}, []string{"xsel", "--clipboard", "--output"})
		}
	}
	for _, command := range commands {
		if parent.Err() != nil {
			return ""
		}
		ctx, cancel := context.WithTimeout(parent, clipboardTextTimeout)
		out, err := clipboardRun(ctx, command[0], command[1:]...)
		cancel()
		if err == nil {
			return jsstring.FromUTF8(out)
		}
	}
	native := hostNativeClipboardText()
	if native == nil || parent.Err() != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(parent, clipboardTextTimeout)
	text, err := native(ctx)
	cancel()
	if err != nil || text == nil {
		return ""
	}
	return *text
}

// hostNativeClipboardText looks up the native helper lazily, after the commands, as upstream does. It returns nil without a helper or GetText; an unavailable read yields no text.
// upstream: packages/coding-agent/src/utils/clipboard.ts:readClipboardText
func hostNativeClipboardText() nativeClipboardText {
	helper := getNativeClipboard()
	if helper == nil || helper.GetText == nil {
		return nil
	}
	return func(ctx context.Context) (*string, error) {
		text, available, err := helper.GetText(ctx)
		if err != nil || !available {
			return nil, err
		}
		return text, nil
	}
}

// bracketedPaste wraps text as a terminal bracketed paste.
func bracketedPaste(text string) string { return "\x1b[200~" + text + "\x1b[201~" }

func (m *InteractiveMode) readClipboardTextAsync(apply func(string)) {
	if m.clipboardCtx == nil || m.clipboardReads == nil || m.clipboardCtx.Err() != nil {
		return
	}
	ctx, reads := m.clipboardCtx, m.clipboardReads
	reads.Go(func() {
		text := readClipboardText(ctx)
		if text == "" || ctx.Err() != nil {
			return
		}
		m.runOnMain(ctx, func() {
			if ctx.Err() == nil {
				apply(text)
			}
		})
	})
}

// handleRightClickPaste pastes clipboard text into the focused component, as
// a bracketed paste, when the fullscreen renderer reports a Windows
// right-click. The read runs off the owner loop; the paste lands only if focus
// has not moved meanwhile. Clipboard errors are ignored. Mirrors upstream
// InteractiveMode.handleRightClickPaste.
func (m *InteractiveMode) handleRightClickPaste() {
	if m.altScreen == nil || m.clipboardCtx == nil || m.clipboardCtx.Err() != nil {
		return
	}
	altScreen := m.altScreen
	target := altScreen.GetFocusedComponent()
	handler, ok := target.(interface{ HandleInput(data string) })
	if !ok {
		return
	}
	m.readClipboardTextAsync(func(text string) {
		if m.altScreen != altScreen || altScreen.GetFocusedComponent() != target {
			return
		}
		handler.HandleInput(bracketedPaste(text))
		m.requestRender()
	})
}

// readClipboardFilePaths reads file paths, such as Finder file copies, from the native clipboard, or nil when there are none or the helper cannot read them. Mirrors upstream readClipboardFilePaths (utils/clipboard.ts).
func readClipboardFilePaths(ctx context.Context) ([]string, error) {
	helper := getNativeClipboard()
	if helper == nil || helper.GetFilePaths == nil {
		return nil, nil
	}
	paths, _, err := helper.GetFilePaths(ctx)
	if err != nil || len(paths) == 0 {
		return nil, err
	}
	return paths, nil
}
