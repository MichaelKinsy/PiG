// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.
package codingagent

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// headerContainer keeps host spacing outside the replaceable header component, including when a custom header renders no rows.
func (m *InteractiveMode) headerContainer() *tui.Container {
	if !m.opts.LoginVisible {
		return tui.NewContainer(m.extHeader)
	}
	return tui.NewContainer(tui.NewSpacer(1), m.extHeader, tui.NewSpacer(1))
}

// supportsHalfBlockMark reports whether the terminal draws half blocks cell-aligned. Apple Terminal leaves gaps between rows and
// misaligns them, so the pixel art would break apart there; it gets the text mark. A variable so tests can select either.
var supportsHalfBlockMark = func() bool { return !tui.IsAppleTerminalSession() }

// builtInHeaderKey holds every input that changes the built-in header's lines. A theme carries its color mode, and the theme and the merged keybindings are replaced, never changed in place (tui.SetTheme and RefreshActiveThemeColorMode store a new theme, KeybindingsManager.rebuild a new manager), so their pointers stand for their content. Nil bindings draw the host platform's defaults, which do not change while PiG runs; the version is a constant. The sprite's ID with piglogin.Revision identifies what the sprite draws.
type builtInHeaderKey struct {
	width                 int
	theme                 *tui.Theme
	bindings              *tui.TUIKeybindingsManager
	sprite                string
	spriteRevision        uint64
	halfBlock             bool
	expanded, showDetails bool
}

// builtInHeaderRender is the last built-in header drawn: its lines and logo area for its key.
type builtInHeaderRender struct {
	key   builtInHeaderKey
	lines []string
	logo  *builtInHeaderLogoArea
}

// renderBuiltInHeader renders the current startup help expansion. Verbose seeds this state only at initialization; tool toggles and header restoration subsequently select it.
// The lines are kept until an input in builtInHeaderKey changes, as Pi's BuiltInHeader keeps its text until it is invalidated (themed-text.ts) and its Text keeps its lines per width (text.ts render); the frame loop calls this on every frame, and drawing the pig head and wrapping the hints beside it is most of a frame's allocations.
func (m *InteractiveMode) renderBuiltInHeader(width int) []string {
	theme := tui.ActiveTheme()
	var bindings *tui.TUIKeybindingsManager
	if m.keybindings != nil {
		bindings = m.keybindings.merged
	}
	m.toolMu.Lock()
	expanded, showDetails := m.builtInHeaderExpanded, m.builtInHeaderShowDetails
	m.toolMu.Unlock()
	// The revision is read before the sprite, so a sprite registered in between is drawn again on the next frame.
	spriteRevision := piglogin.Revision()
	variant := piglogin.Active()
	key := builtInHeaderKey{
		width: width, theme: theme, bindings: bindings, sprite: variant.ID, spriteRevision: spriteRevision,
		halfBlock: supportsHalfBlockMark(), expanded: expanded, showDetails: showDetails,
	}
	if last := m.builtInHeaderLast.Load(); last != nil && last.key == key {
		m.builtInHeaderLogo.Store(last.logo)
		return last.lines
	}
	if bindings == nil {
		bindings = tui.NewKeybindingsManager(keybindingDefinitionsFor(tui.HostKeybindingPlatform()), nil)
	}
	lines, logo := drawBuiltInHeader(key, bindings, variant)
	m.builtInHeaderLogo.Store(logo)
	m.builtInHeaderLast.Store(&builtInHeaderRender{key: key, lines: lines, logo: logo})
	return lines
}

// drawBuiltInHeader draws the built-in header for in with bindings and the sprite variant, and returns its lines and the logo's clickable area.
func drawBuiltInHeader(in builtInHeaderKey, bindings *tui.TUIKeybindingsManager, variant piglogin.Variant) ([]string, *builtInHeaderLogoArea) {
	width, theme, expanded, showDetails := in.width, in.theme, in.expanded, in.showDetails
	mode := theme.GetColorMode()
	key := func(action string) string {
		return tui.FormatKeyText(strings.Join(bindings.GetKeys(action), "/"), false)
	}
	rawHint := func(key, description string) string {
		return themeFg(theme.Dim, key) + themeFg(theme.Muted, " "+description)
	}
	hint := func(action, description string) string { return rawHint(key(action), description) }
	// pig divergence (D2): PiG draws its pig head in Pi's logo slot (pi-logo.ts, interactive-mode.ts:998-1006), HeadRows lines
	// tall where Pi's logo is 2. The head's first line carries the version and its other lines the next lines of the header,
	// as Pi's logo carries the version and the first hint line. Where the head cannot be drawn but Pi draws its logo (no
	// truecolor, or too narrow for the version beside the head) the one-line text mark takes the logo's 4-cell slot: its
	// line carries the version and the slot's second line the first line of key hints, so the hints wrap where Pi's wrap. In
	// Apple Terminal, where Pi draws its wordmark instead of the logo (supportsPiLogo), the text mark takes the wordmark's
	// place.
	// pig divergence (D63): the startup version is the composite PiG+Pi release identity.
	version := themeFg(theme.Dim, "v"+pigversion.Version)
	drawHead := in.halfBlock && mode == tui.TerminalColorModeTrueColor &&
		width-2 >= piglogin.HeadCells+1+widthx.VisibleWidth(version)
	withLogo := func(hints string) string {
		switch {
		case drawHead:
			return version + "\n" + hints
		case !in.halfBlock:
			// interactive-mode.ts:1003: a terminal that cannot render the logo gets the wordmark and the version, with the hints below.
			return piWordmark(mode) + " " + version + "\n" + hints
		}
		return piglogin.TextMark(variant, mode) + " " + version + "\n" + strings.Repeat(" ", piglogin.TextMarkWidth) + " " + hints
	}
	// pig additive (D92): a hint of a stripped built-in (an action of a stripped command, or `!` of a stripped bash tool) is
	// left out. Stock PiG strips nothing and joins Pi's hints unchanged.
	bashStripped := pigstrip.Has(pigstrip.ListTools, "bash")
	unless := func(stripped bool, hint string) string {
		if stripped {
			return ""
		}
		return hint
	}
	join := func(hints []string, separator string) string {
		return strings.Join(slices.DeleteFunc(hints, func(hint string) bool { return hint == "" }), separator)
	}
	var instructions string
	if expanded {
		instructions = withLogo(join([]string{
			hint("app.interrupt", "to interrupt"),
			hint("app.clear", "to clear"),
			rawHint(key("app.clear")+" twice", "to exit"),
			hint("app.exit", "to exit (empty)"),
			hint("app.suspend", "to suspend"),
			hint("tui.editor.deleteToLineEnd", "to delete to end"),
			unless(appActionStripped("app.thinking.cycle"), hint("app.thinking.cycle", "to cycle thinking level")),
			unless(appActionStripped("app.model.cycleForward"), rawHint(key("app.model.cycleForward")+"/"+key("app.model.cycleBackward"), "to cycle models")),
			unless(appActionStripped("app.model.select"), hint("app.model.select", "to select model")),
			hint("app.tools.expand", "to expand tools"),
			hint("app.thinking.toggle", "to expand thinking"),
			hint("app.editor.external", "for external editor"),
			rawHint("/", "for commands"),
			unless(bashStripped, rawHint("!", "to run bash")),
			unless(bashStripped, rawHint("!!", "to run bash (no context)")),
			hint("app.message.followUp", "to queue follow-up"),
			hint("app.message.dequeue", "to edit all queued messages"),
			hint("app.clipboard.pasteImage", "to paste files on macOS, images, or text"),
			rawHint("drop files", "to attach"),
		}, "\n"))
	} else {
		instructions = join([]string{
			hint("app.interrupt", "interrupt"),
			rawHint(key("app.clear")+"/"+key("app.exit"), "clear/exit"),
			rawHint("/", "commands"),
			unless(bashStripped, rawHint("!", "bash")),
			hint("app.tools.expand", "more"),
		}, themeFg(theme.Muted, " · "))
		// interactive-mode.ts:997, 1044-1048: the loaded resources are mentioned only when the startup details showed as the header was built.
		resources := ""
		if showDetails {
			resources = " and loaded resources"
		}
		instructions = withLogo(instructions) + "\n" + themeFg(theme.Dim, "Press "+key("app.tools.expand")+" to show full startup help"+resources+".")
	}
	// pig divergence (D2): self-help names PiG rather than the separate Pi executable.
	// pig additive (D92): without the docs bundle there are no docs to look up.
	onboarding := themeFg(theme.Dim, "PiG can explain its own features and look up its docs. Ask it how to use or extend PiG.")
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		onboarding = themeFg(theme.Dim, "PiG can explain its own features. Ask it how to use or extend PiG.")
	}
	text := instructions + "\n\n" + onboarding
	if !drawHead {
		// Pi's logo is clickable wherever Pi draws it, which is everywhere but Apple Terminal (supportsPiLogo); the text mark
		// stands in for it there.
		area := builtInHeaderLogoArea{}
		if in.halfBlock {
			area = builtInHeaderLogoArea{visible: true, column: 1, columns: piglogin.TextMarkWidth, rows: 1}
		}
		return tui.NewPaddedText(text, 1, 0, nil).Render(width), &area
	}
	area := builtInHeaderLogoArea{visible: true, column: 1, columns: piglogin.HeadCells, rows: piglogin.HeadRows}
	return headBesideText(piglogin.HeadLines(variant, mode), text, width), &area
}

// piWordmark is the text fallback for the logo in a terminal color mode (pi-logo.ts piWordmark).
// pig divergence (D2): it is the bold "PiG." text mark of the active sprite, its letters in the sprite's wordmark ramp (as Pi colors "Pi" in coral and yellow) and its period in the accent, not Pi's "Pi".
func piWordmark(mode tui.TerminalColorMode) string {
	return piglogin.TextMark(piglogin.Active(), mode)
}

// headBesideText lays the header's text beside the pig head after the one-cell padding, as Pi lays its first two lines beside
// its logo: Pi prefixes the logo's lines to the first two lines of the header text and renders it all as one Text (paddingX
// 1). Here each logical line that starts beside the head is wrapped first in the cells right of the head, each wrapped line
// gets the next head line (or, past the head, the head's width of spaces), a head line with no text left stands alone, and
// the result goes through the same Text, so styles and padding carry from line to line as in Pi.
func headBesideText(head []string, text string, width int) []string {
	logical := strings.Split(text, "\n")
	besideWidth := max(1, width-2-piglogin.HeadCells-1)
	indent := strings.Repeat(" ", piglogin.HeadCells+1)
	composed := make([]string, 0, len(head)+len(logical))
	row, next := 0, 0
	for ; next < len(logical) && row < len(head); next++ {
		for _, segment := range widthx.WrapTextWithAnsi(logical[next], besideWidth) {
			prefix := indent
			if row < len(head) {
				prefix = head[row] + " "
				row++
			}
			composed = append(composed, prefix+segment)
		}
	}
	for ; row < len(head); row++ {
		composed = append(composed, head[row])
	}
	composed = append(composed, logical[next:]...)
	return tui.NewPaddedText(strings.Join(composed, "\n"), 1, 0, nil).Render(width)
}

// builtInHeaderLogoArea is the clickable logo of the built-in header as last drawn: the cells of the pig head, or of the text
// mark where it stands in for Pi's logo, relative to the header's first line.
type builtInHeaderLogoArea struct {
	visible       bool
	column, row   int
	columns, rows int
}

// handleBuiltInHeaderMouse plays the logo easter egg when the header's logo is clicked. Mirrors interactive-mode.ts
// BuiltInHeader.handleMouse, which takes a click on the logo's cells (x 1 to 4 of its two lines, after one column of
// padding) and passes the logo's top-left screen cell; Pi sets onLogoClick only when it draws its logo (interactive-mode.ts:1058).
// pig divergence (D87): the logo is PiG's pig head (D2), piglogin.HeadCells cells by piglogin.HeadRows lines, or the 4-cell
// text mark where it stands in for Pi's logo.
func (m *InteractiveMode) handleBuiltInHeaderMouse(event tui.TuiMouseEvent) *tui.TuiMouseDispatchResult {
	area := m.builtInHeaderLogo.Load()
	if event.Type != tui.MouseClick || area == nil || !area.visible ||
		event.X < area.column || event.X >= area.column+area.columns || event.Y < area.row || event.Y >= area.row+area.rows {
		return nil
	}
	left, top := event.ScreenX-event.X+area.column, event.ScreenY-event.Y+area.row
	m.playPigLogoAnimation(left, top, area.columns, area.rows)
	return &tui.TuiMouseDispatchResult{TuiMouseEventResult: tui.TuiMouseEventResult{Handled: true}}
}

// pig additive (D91): builtInHeaderClickArea is the header's logo as a frontend session reports a click on it.
func (m *InteractiveMode) builtInHeaderClickArea() (frontend.Area, bool) {
	area := m.builtInHeaderLogo.Load()
	if area == nil || !area.visible {
		return frontend.Area{}, false
	}
	return frontend.Area{Row: area.row, Column: area.column, Rows: area.rows, Columns: area.columns}, true
}
