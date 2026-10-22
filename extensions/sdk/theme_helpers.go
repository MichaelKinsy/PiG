package sdk

import (
	"fmt"
	"strings"
)

// Pi's coding-agent theme helpers (modes/interactive/theme/theme.ts): the component themes of pi-tui built from the process-global theme, highlightCode and
// initTheme. Pi's theme is global, so these read the global theme each time a function runs: the theme the host last published, or the one [InitTheme] installed since
// (see tui_state.go). The host has the highlight.js port, so [HighlightCode] asks it (the highlight.code host call) and a failed call takes the same lines Pi returns
// for a failed highlight.

// MarkdownTheme is pi-tui's MarkdownTheme as getMarkdownTheme returns it: every style is a function of the global theme.
type MarkdownTheme struct {
	Heading         func(text string) string
	Link            func(text string) string
	LinkUrl         func(text string) string
	Code            func(text string) string
	CodeBlock       func(text string) string
	CodeBlockBorder func(text string) string
	Quote           func(text string) string
	QuoteBorder     func(text string) string
	Hr              func(text string) string
	ListBullet      func(text string) string
	Bold            func(text string) string
	Italic          func(text string) string
	Underline       func(text string) string
	Strikethrough   func(text string) string
	// HighlightCode is getMarkdownTheme().highlightCode: [HighlightCode], with the mdCodeBlock color for a highlight that fails.
	HighlightCode func(code, lang string) []string
}

// SelectListTheme is pi-tui's SelectListTheme as getSelectListTheme returns it.
type SelectListTheme struct {
	SelectedPrefix func(text string) string
	SelectedText   func(text string) string
	Description    func(text string) string
	ScrollInfo     func(text string) string
	NoMatch        func(text string) string
}

// SettingsListTheme is pi-tui's SettingsListTheme as getSettingsListTheme returns it. Cursor is drawn when the theme is built.
type SettingsListTheme struct {
	Label       func(text string, selected bool) string
	Value       func(text string, selected bool) string
	Description func(text string) string
	Cursor      string
	Hint        func(text string) string
}

// EditorTheme is pi-tui's EditorTheme as getEditorTheme returns it.
type EditorTheme struct {
	BorderColor func(text string) string
	SelectList  SelectListTheme
}

func foregroundOf(token string) func(string) string {
	return func(text string) string { return currentHostTheme().Fg(token, text) }
}

// GetMarkdownTheme is pi-coding-agent's getMarkdownTheme.
func GetMarkdownTheme() MarkdownTheme {
	return MarkdownTheme{
		Heading:         foregroundOf("mdHeading"),
		Link:            foregroundOf("mdLink"),
		LinkUrl:         foregroundOf("mdLinkUrl"),
		Code:            foregroundOf("mdCode"),
		CodeBlock:       foregroundOf("mdCodeBlock"),
		CodeBlockBorder: foregroundOf("mdCodeBlockBorder"),
		Quote:           foregroundOf("mdQuote"),
		QuoteBorder:     foregroundOf("mdQuoteBorder"),
		Hr:              foregroundOf("mdHr"),
		ListBullet:      foregroundOf("mdListBullet"),
		Bold:            func(text string) string { return currentHostTheme().Bold(text) },
		Italic:          func(text string) string { return currentHostTheme().Italic(text) },
		Underline:       func(text string) string { return currentHostTheme().Underline(text) },
		Strikethrough:   func(text string) string { return currentHostTheme().Strikethrough(text) },
		HighlightCode:   func(code, lang string) []string { return highlightCode(code, lang, true) },
	}
}

// GetSelectListTheme is pi-coding-agent's getSelectListTheme.
func GetSelectListTheme() SelectListTheme {
	accent, muted := foregroundOf("accent"), foregroundOf("muted")
	return SelectListTheme{SelectedPrefix: accent, SelectedText: accent, Description: muted, ScrollInfo: muted, NoMatch: muted}
}

// GetEditorTheme is theme.ts getEditorTheme, which the interactive mode builds its editor with.
func GetEditorTheme() EditorTheme {
	return EditorTheme{BorderColor: foregroundOf("borderMuted"), SelectList: GetSelectListTheme()}
}

// GetSettingsListTheme is pi-coding-agent's getSettingsListTheme.
func GetSettingsListTheme() SettingsListTheme {
	accent, muted, dim := foregroundOf("accent"), foregroundOf("muted"), foregroundOf("dim")
	return SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return accent(text)
			}
			return text
		},
		Value: func(text string, selected bool) string {
			if selected {
				return accent(text)
			}
			return muted(text)
		},
		Description: dim,
		Cursor:      accent("→ "),
		Hint:        dim,
	}
}

// HighlightCode is pi-coding-agent's highlightCode: code highlighted for lang with the global theme, one string per line. Code in a language the host's highlight.js does
// not support, or with no language, takes the mdCodeBlock color; code highlight.js fails on stays unstyled. The host's highlighter runs it; a host that cannot be reached
// leaves the code unstyled, as a failed highlight does.
func HighlightCode(code, lang string) []string { return highlightCode(code, lang, false) }

func highlightCode(code, lang string, markdown bool) []string {
	if lang == "" {
		// No language: Pi highlights nothing, whatever the host would answer.
		return codeBlockLines(code)
	}
	if lines, err := highlightCodeLines(code, lang, markdown); err == nil {
		return lines
	}
	if markdown {
		return codeBlockLines(code)
	}
	return strings.Split(code, "\n")
}

// codeBlockLines is the code's lines in the mdCodeBlock color, which Pi gives code it does not highlight.
func codeBlockLines(code string) []string {
	theme := currentHostTheme()
	lines := strings.Split(code, "\n")
	for i, line := range lines {
		lines[i] = theme.Fg("mdCodeBlock", line)
	}
	return lines
}

// InitTheme is pi-coding-agent's initTheme: it makes the host's theme of that name, or the system theme for an empty name or one the host has no theme of, the global theme,
// which every helper above reads, until the host's own theme changes. The host answers over the theme.load call; an error means it could not be reached and the global theme
// is unchanged. Pi's enableWatcher has no counterpart: the host owns its theme files.
func InitTheme(name string) error {
	var request *string
	if name != "" {
		request = &name
	}
	raw, err := loadTheme(request)
	if err != nil {
		return err
	}
	theme, ok := decodeUITheme(raw)
	if !ok {
		return fmt.Errorf("initTheme: the host answered a theme that is not a palette")
	}
	installedTheme.Store(&globalTheme{theme: theme, raw: raw})
	return nil
}
