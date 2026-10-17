// Ports packages/coding-agent/src/modes/interactive/tui-renderer.ts.
package codingagent

import (
	"io"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// InteractiveTuiOptions supplies the shared paint surface. The driver owns terminal input, dispatch and shutdown. Nil Output selects the process terminal; nil ShowHardwareCursor preserves the renderer's construction default.
type InteractiveTuiOptions struct {
	TuiMode            string
	ShowHardwareCursor *bool
	LogDirectory       string
	Output             io.Writer
	// Terminal is the terminal the renderer drives (createInteractiveTui's `terminal`, tui-renderer.ts); nil selects the process terminal. Output, when set, takes precedence: it is the fixed-size paint surface of tests.
	Terminal               tui.Terminal
	OnRightClickPaste      func()
	FullscreenCopyOnSelect *bool
	// FullscreenWheelScrollLines is the fullscreen renderer's wheel line count; nil is auto (tui-renderer.ts:38).
	FullscreenWheelScrollLines *tui.WheelScrollLines
	OpenURL                    func(string) error
	CopySelection              func(string) error
}

// CreateInteractiveTui creates a regular or fullscreen renderer with the shared theme, clipboard and hyperlink behavior. It does not start terminal input or activate a Session.
func CreateInteractiveTui(options InteractiveTuiOptions) tui.TUI {
	var renderer tui.TUI
	if options.TuiMode == "fullscreen" {
		opts := fullscreenTuiOptions()
		opts.CopyOnSelect = options.FullscreenCopyOnSelect
		opts.CopySelection = options.CopySelection
		opts.WheelScrollLines = tui.WheelScrollLines{Auto: true}
		if options.FullscreenWheelScrollLines != nil {
			opts.WheelScrollLines = *options.FullscreenWheelScrollLines
		}
		if opts.CopySelection == nil {
			opts.CopySelection = copyToClipboard
		}
		openURL := options.OpenURL
		if openURL == nil {
			openURL = openBrowser
		}
		opts.OpenURL = func(url string) { _ = openURL(url) }
		opts.OnRightClickPaste = options.OnRightClickPaste
		if options.Output == nil {
			renderer = tui.NewTuiAltScreen(options.Terminal, nil, options.LogDirectory, opts)
		} else {
			// upstream: packages/tui/src/terminal.ts:ProcessTerminal
			renderer = tui.NewTuiAltScreenWithOutput(options.Output, 80, 24, opts)
		}
	} else {
		var main *tui.TuiMainScreen
		switch {
		case options.Output == nil && options.Terminal != nil:
			main = tui.NewTuiMainScreen(options.Terminal, nil, options.LogDirectory)
		case options.Output == nil:
			main = tui.New()
		default:
			// upstream: packages/tui/src/terminal.ts:ProcessTerminal
			main = tui.NewWithOutput(options.Output, 80, 24)
		}
		main.SetLogDirectory(options.LogDirectory)
		renderer = main
	}
	if options.ShowHardwareCursor != nil {
		renderer.SetShowHardwareCursor(*options.ShowHardwareCursor)
	}
	return renderer
}

func themeBgText(token, text string) string {
	bg := tui.ActiveTheme().GetBgAnsi(token)
	if bg == "" {
		return text
	}
	return bg + text + tui.SGRBgReset
}

func styleSearchMatch(text string) string {
	return themeBgText("searchMatchBg", tui.ActiveTheme().Fg("searchMatchText", text))
}

func scrollToEndIndicatorLabel() string {
	label := " ↓ Jump to latest message"
	if keys := tui.GetKeybindings().GetKeys(tui.KBAltScreenBottom); len(keys) > 0 {
		label += " · " + tui.FormatKeyText(strings.Join(keys, "/"), true)
	}
	return themeBgText("selectedBg", tui.ActiveTheme().Fg("text", label+" "))
}

func fullscreenTuiOptions() tui.TuiAltScreenOptions {
	return tui.TuiAltScreenOptions{
		SearchMatchStyle: func(text string) string { return "\x1b[4m" + styleSearchMatch(text) + tui.SGRUnderlineReset },
		SearchCurrentMatchStyle: func(text string) string {
			return "\x1b[1m" + tui.ActiveTheme().Inverse(styleSearchMatch(text)) + tui.SGRBoldDimReset
		},
		SearchNavigationButtonStyle: func(text string, hovered bool) string {
			if hovered {
				return "\x1b[4m" + text + tui.SGRUnderlineReset
			}
			return text
		},
		ScrollToEndIndicator: scrollToEndIndicatorLabel,
	}
}
