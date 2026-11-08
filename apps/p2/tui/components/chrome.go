package components

// The runtime's built-in header, status lines and footer fallback. The header follows coding-agent's startup header (interactive-mode.ts
// BuiltInHeader, pi-logo.ts): logo, then the server origin and role in place of the version, then key hints; Ctrl+O (expanded tools) shows the full
// list.
//
// pi: apps/p2/src/tui/components/chrome.ts

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

const reset = "\x1b[0m"

var (
	coral  = mustRGB(228, 138, 122)
	blue   = mustRGB(79, 142, 179)
	yellow = mustRGB(234, 182, 93)
)

func mustRGB(r, g, b float64) tui.Color {
	color, err := tui.NewRgbColor(r, g, b)
	if err != nil {
		panic(err)
	}
	return color
}

// PiLogoLines is piLogoLines (pi-logo.ts): 4 cells by 2 lines of half blocks in fixed brand colors, in the theme's color mode.
func PiLogoLines(ctx render.RenderContext) (top, bottom string) {
	mode := ctx.Theme().Current().GetColorMode()
	fg := func(color tui.Color) string { return tui.ForegroundAnsi(color, mode) }
	top = fg(coral) + tui.BackgroundAnsi(blue, mode) + "▀" + reset + fg(coral) + "▀█" + reset + " "
	bottom = fg(blue) + "█▀" + reset + " " + fg(yellow) + "█" + reset
	return top, bottom
}

// HeaderSource is what the built-in header reads each render (HeaderSource's getters).
type HeaderSource interface {
	// Ctx is the render context; HintKey and HintRaw are its `hints.key` and `hints.raw`.
	Ctx() render.RenderContext
	HintKey(id, description string) string
	HintRaw(key, description string) string
	Origin() string
	Role() string
	Connected() bool
	Expanded() bool
	// Hints are the hints for the compact line, already styled.
	Hints() []string
	// ExpandedHints is the full list shown when expanded.
	ExpandedHints() []string
}

// BuiltInHeader is the runtime's built-in header.
type BuiltInHeader struct {
	source HeaderSource
	text   *tui.Text
	key    string
}

// NewBuiltInHeader is the BuiltInHeader constructor.
func NewBuiltInHeader(source HeaderSource) *BuiltInHeader {
	return &BuiltInHeader{source: source, text: tui.NewPaddedText("", 1, 0, nil)}
}

// Invalidate drops the cached text.
func (h *BuiltInHeader) Invalidate() {
	h.key = ""
	h.text.Invalidate()
}

// Render renders the header.
func (h *BuiltInHeader) Render(width int) []string {
	source := h.source
	ctx := source.Ctx()
	theme := ctx.Theme().Current()
	hints := source.Hints()
	if source.Expanded() {
		hints = source.ExpandedHints()
	}
	key := fmt.Sprintf("%d\x00%q\x00%q\x00%t\x00%q", ctx.Theme().Version, source.Origin(), source.Role(), source.Connected(), hints)
	if key != h.key {
		h.key = key
		top, bottom := PiLogoLines(ctx)
		var where []string
		if origin := source.Origin(); origin != "" {
			where = append(where, render.LineText(origin))
		}
		if role := source.Role(); role != "" {
			where = append(where, role)
		}
		state := ""
		if !source.Connected() {
			state = " " + theme.Fg("warning", "reconnecting…")
		}
		separator := theme.Fg("muted", " · ")
		head := top + " " + theme.Fg("dim", strings.Join(where, " · ")) + state
		var lines []string
		if source.Expanded() {
			lines = append([]string{head, bottom}, hints...)
		} else {
			lines = []string{head, bottom + " " + strings.Join(hints, separator), theme.Fg("dim", "Press "+ctx.KeyText("app.tools.expand")+" to show full startup help.")}
		}
		h.text.SetText(strings.Join(lines, "\n"))
	}
	return append([]string{""}, h.text.Render(width)...)
}

// NoticeKind is a Notice's kind: "info", "warning" or "error".
type NoticeKind string

// The three NoticeKind values.
const (
	NoticeInfo    NoticeKind = "info"
	NoticeWarning NoticeKind = "warning"
	NoticeError   NoticeKind = "error"
)

// Notice is a status or error line.
type Notice struct {
	Kind  NoticeKind
	Text  string
	Count int
}

// StatusLines renders status and error lines (pi's showStatus/showError); a repeated message collapses into one line with a count.
type StatusLines struct {
	ctx     render.RenderContext
	notices func() []Notice
}

// NewStatusLines is the StatusLines constructor.
func NewStatusLines(ctx render.RenderContext, notices func() []Notice) *StatusLines {
	return &StatusLines{ctx: ctx, notices: notices}
}

// Invalidate does nothing: the lines are built on each render.
func (*StatusLines) Invalidate() {}

// Render renders the notices.
func (s *StatusLines) Render(width int) []string {
	theme := s.ctx.Theme().Current()
	notices := s.notices()
	lines := make([]string, len(notices))
	for i, notice := range notices {
		color := "dim"
		switch notice.Kind {
		case NoticeError:
			color = "error"
		case NoticeWarning:
			color = "warning"
		}
		prefix, suffix := "", ""
		if notice.Kind == NoticeError {
			prefix = "Error: "
		}
		if notice.Count > 1 {
			suffix = fmt.Sprintf(" (×%d)", notice.Count)
		}
		text := prefix + render.LineText(notice.Text) + suffix
		lines[i] = widthx.TruncateToWidth(" "+theme.Fg(color, text), width, "...", false)
	}
	return lines
}

// BuiltInFooter is the fallback footer when no plugin registers the `footer` slot: one dim line of hints.
type BuiltInFooter struct{ line func() string }

// NewBuiltInFooter is the BuiltInFooter constructor.
func NewBuiltInFooter(line func() string) *BuiltInFooter { return &BuiltInFooter{line: line} }

// Invalidate does nothing.
func (*BuiltInFooter) Invalidate() {}

// Render renders the footer line.
func (f *BuiltInFooter) Render(width int) []string {
	line := f.line()
	if line == "" {
		return nil
	}
	rendered := tui.NewTruncatedText(line, 1, 0).Render(width)
	if len(rendered) == 0 {
		return []string{""}
	}
	return []string{rendered[0]}
}

// Spread pads `left` and `right` onto one line (footer stats left, model right), truncating the right side first.
func Spread(left, right string, width, minPadding int) string {
	leftText := left
	leftWidth := widthx.VisibleWidth(leftText)
	if leftWidth > width {
		leftText = widthx.TruncateToWidth(leftText, width, "...", false)
		leftWidth = widthx.VisibleWidth(leftText)
	}
	rightWidth := widthx.VisibleWidth(right)
	if leftWidth+minPadding+rightWidth <= width {
		return leftText + strings.Repeat(" ", width-leftWidth-rightWidth) + right
	}
	available := width - leftWidth - minPadding
	if available <= 0 {
		return leftText
	}
	cut := widthx.TruncateToWidth(right, available, "", false)
	return leftText + strings.Repeat(" ", max(0, width-leftWidth-widthx.VisibleWidth(cut))) + cut
}
