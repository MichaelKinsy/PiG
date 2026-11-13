package subprocess

// view_kit_grid.go: the grid kind (D614, the cell form). The host owns the cursor, keyed by cell value so it survives
// the updates that keep its cell, and draws the grid in equal columns whatever the width: label line, chip lines, then
// a "+K more" line, truncated by terminal cells. A grid is a focusable widget: it takes the keys it can act on and no
// other, so the keys a mount binds reach the client's unhandled-key path.

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func init() {
	registerViewWidget(ViewKindGrid, &viewWidget{check: checkGrid, focusable: true})
}

// gridColor checks a colour by the rule a drawing's fill follows: a theme token, "#rrggbb" or "#rrggbbaa".
func (c *viewChecker) gridColor(what, color string) {
	switch {
	case color == "":
	case !uiview.ValidColor(color):
		c.fail("grid %s %q is not a theme token, #rrggbb or #rrggbbaa", what, color)
	case color[0] != '#':
		c.fg(color)
	}
}

// checkGrid is the one validator of a grid: validateView calls it for a D107 view and the bridge for a mount.
func checkGrid(c *viewChecker, n *ViewNode) {
	if n.ID == "" {
		c.fail("grid needs an id")
	}
	if n.Columns == nil || *n.Columns < 1 || *n.Columns > ViewGridMaxColumns {
		c.fail("grid needs columns from 1 to %d", ViewGridMaxColumns)
		return
	}
	columns := *n.Columns
	if len(n.Head) != 0 && len(n.Head) != columns {
		c.fail("grid has %d head labels for %d columns", len(n.Head), columns)
	}
	for _, h := range n.Head {
		if len(h) > ViewGridMaxHeadBytes {
			c.fail("grid head label is more than %d bytes", ViewGridMaxHeadBytes)
		}
	}
	if n.ChipRows != nil && (*n.ChipRows < 1 || *n.ChipRows > ViewGridMaxChipRows) {
		c.fail("grid chipRows must be from 1 to %d", ViewGridMaxChipRows)
	}
	if len(n.Cells) > ViewGridMaxCells {
		c.fail("grid has more than %d cells", ViewGridMaxCells)
		return
	}
	if rows := (len(n.Cells) + columns - 1) / columns; rows > ViewGridMaxRows {
		c.fail("grid has %d rows, at most %d", rows, ViewGridMaxRows)
	}
	values := map[string]bool{}
	for i := range n.Cells {
		cell := &n.Cells[i]
		switch {
		case len(cell.Value) > ViewGridMaxValueBytes:
			c.fail("grid cell value is more than %d bytes", ViewGridMaxValueBytes)
		case len(cell.Label) > ViewGridMaxLabelBytes:
			c.fail("grid cell label is more than %d bytes", ViewGridMaxLabelBytes)
		case cell.Mark != "" && cell.Mark != ViewGridMarkCurrent:
			c.fail("unknown grid cell mark %q", cell.Mark)
		case cell.More < 0 || cell.More > ViewGridMaxMore:
			c.fail("grid cell more must be from 0 to %d", ViewGridMaxMore)
		case len(cell.Chips) > ViewGridMaxChips:
			c.fail("grid cell has more than %d chips", ViewGridMaxChips)
		case cell.Value != "" && values[cell.Value]:
			c.fail("duplicate grid cell value %q", cell.Value)
		}
		values[cell.Value] = true
		c.gridColor("cell color", cell.Color)
		for _, chip := range cell.Chips {
			if len(chip.Text) > ViewGridMaxChipBytes {
				c.fail("grid chip text is more than %d bytes", ViewGridMaxChipBytes)
			}
			c.gridColor("chip color", chip.Color)
		}
	}
	if n.Selected != "" && !values[n.Selected] {
		c.fail("grid selects %q, which is not a cell value", n.Selected)
	}
}

// viewGrid is the grid's component and widget state.
type viewGrid struct {
	b    *viewBuilder
	inst *viewInstance

	theme    *tui.Theme
	budget   func() int
	columns  int
	chipRows int
	head     []string
	cells    []ViewGridCell
	// cursor is the value of the cell the cursor is on, "" for none.
	cursor  string
	focused bool
	lay     viewGridLayout
}

// viewGridLayout is where the last Render put the cells, for the pointer.
type viewGridLayout struct {
	sep, cellW int
	rowTop     []int
	rowHeight  []int
}

func buildGrid(b *viewBuilder, n *ViewNode, out *frontend.ViewNode) (tui.Component, error) {
	inst, fresh := b.widgetInstance(n, widgetCtorKey(n, func(c *ViewNode) { c.ViewGridFields = ViewGridFields{} }))
	inst.node = out
	if fresh {
		inst.widget = &viewGrid{inst: inst}
	}
	g := inst.widget.(*viewGrid)
	g.b, g.theme, g.budget = b, b.theme, b.s.rowBudget
	g.columns, g.chipRows, g.head, g.cells = *n.Columns, ViewGridDefaultChipRows, n.Head, n.Cells
	if n.ChipRows != nil {
		g.chipRows = *n.ChipRows
	}
	switch {
	case n.Selected != "" && (fresh || inst.sent == nil || inst.sent.Selected != n.Selected):
		g.cursor = n.Selected
	case g.indexOf(g.cursor) < 0:
		g.cursor = n.Selected
	}
	inst.remember(n)
	g.refresh(out)
	return g, nil
}

func (g *viewGrid) refresh(node *frontend.ViewNode) {
	grid := &frontend.ViewGrid{Columns: g.columns, ChipRows: g.chipRows, Head: slices.Clone(g.head), Selected: g.cursor}
	for _, c := range g.cells {
		cell := frontend.ViewGridCell{Value: c.Value, Label: c.Label, Mark: c.Mark, Dim: c.Dim, Color: c.Color, More: c.More}
		for _, chip := range c.Chips {
			cell.Chips = append(cell.Chips, frontend.ViewGridChip{Text: chip.Text, Color: chip.Color})
		}
		grid.Cells = append(grid.Cells, cell)
	}
	node.Grid = grid
}

func (g *viewGrid) Invalidate() {}

// indexOf is the index of the cell with value, or -1 (an empty value names none).
func (g *viewGrid) indexOf(value string) int {
	if value == "" {
		return -1
	}
	return slices.IndexFunc(g.cells, func(c ViewGridCell) bool { return c.Value == value })
}

func (g *viewGrid) setFocused(focused bool) {
	g.focused = focused
	if focused && g.indexOf(g.cursor) < 0 {
		if i := slices.IndexFunc(g.cells, func(c ViewGridCell) bool { return c.Value != "" }); i >= 0 {
			g.cursor = g.cells[i].Value
		}
	}
}

// moveTo puts the cursor on cell i and queues the selection change.
func (g *viewGrid) moveTo(i int) {
	g.cursor = g.cells[i].Value
	g.b.queue(g.inst, ViewEventSelectionChange, func(ev *ViewEventPayload) {
		ev.Index, ev.Item = i, &ViewItem{Value: g.cells[i].Value, Label: g.cells[i].Label}
	})
}

// step finds the next chosen cell from i by delta (every step in the row-major order, or a whole row), skipping cells
// without a value.
func (g *viewGrid) step(i, delta int) int {
	for j := i + delta; j >= 0 && j < len(g.cells); j += delta {
		if g.cells[j].Value != "" {
			return j
		}
	}
	return -1
}

// handleInput takes the arrow keys, Home, End, Enter and Space when the grid can act on them, and reports false for
// every other key and for a move past an edge.
func (g *viewGrid) handleInput(data string) bool {
	i := g.indexOf(g.cursor)
	if i < 0 {
		return false
	}
	row := i / g.columns
	target := -1
	switch {
	case tui.MatchesKeyID(data, "left"):
		target = g.step(i, -1)
	case tui.MatchesKeyID(data, "right"):
		target = g.step(i, 1)
	case tui.MatchesKeyID(data, "up"):
		target = g.step(i, -g.columns)
	case tui.MatchesKeyID(data, "down"):
		target = g.step(i, g.columns)
	case tui.MatchesKeyID(data, "home"):
		for j := row * g.columns; j < i && target < 0; j++ {
			if g.cells[j].Value != "" {
				target = j
			}
		}
	case tui.MatchesKeyID(data, "end"):
		for j := min(row*g.columns+g.columns, len(g.cells)) - 1; j > i && target < 0; j-- {
			if g.cells[j].Value != "" {
				target = j
			}
		}
	case tui.MatchesKeyID(data, "enter"), tui.MatchesKeyID(data, "space"):
		g.choose(i)
		return true
	default:
		return false
	}
	if target < 0 {
		return false
	}
	g.moveTo(target)
	return true
}

// choose queues the choice of cell i, which raises the intent of the node with the cell's value.
func (g *viewGrid) choose(i int) {
	g.b.queue(g.inst, ViewEventSelect, func(ev *ViewEventPayload) {
		ev.Index, ev.Item = i, &ViewItem{Value: g.cells[i].Value, Label: g.cells[i].Label}
	})
}

// cellAt is the index of the chosen cell under a pointer event, or -1.
func (g *viewGrid) cellAt(event tui.TuiMouseEvent) int {
	if g.lay.cellW == 0 {
		return -1
	}
	col, within := event.X/(g.lay.cellW+g.lay.sep), event.X%(g.lay.cellW+g.lay.sep)
	if within >= g.lay.cellW || col >= g.columns {
		return -1
	}
	for r, top := range g.lay.rowTop {
		if event.Y >= top && event.Y < top+g.lay.rowHeight[r] {
			if i := r*g.columns + col; i < len(g.cells) && g.cells[i].Value != "" {
				return i
			}
		}
	}
	return -1
}

// HandleMouse moves the cursor to the cell a press lands on and asks for the focus; the click that follows chooses the cell.
func (g *viewGrid) HandleMouse(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
	if event.Button != tui.MouseButtonLeft || event.Type != tui.MousePress && event.Type != tui.MouseClick {
		return nil
	}
	i := g.cellAt(event)
	if i < 0 {
		return nil
	}
	if g.cells[i].Value != g.cursor {
		g.moveTo(i)
	}
	if event.Type == tui.MouseClick {
		g.choose(i)
	}
	return &tui.TuiMouseEventResult{Handled: true, Focus: true}
}

// chipsOf is the chips a cell draws with k chip lines and the count its "+K more" line states: every chip when they fit
// and nothing was left out, else one line less for the count (K = the chips not drawn and the events left out).
func chipsOf(cell *ViewGridCell, k int) (shown []ViewGridChip, more int) {
	if k <= 0 {
		return nil, len(cell.Chips) + cell.More
	}
	if cell.More == 0 && len(cell.Chips) <= k {
		return cell.Chips, 0
	}
	drawn := min(len(cell.Chips), k-1)
	return cell.Chips[:drawn], len(cell.Chips) - drawn + cell.More
}

func chipLines(cell *ViewGridCell, k int) int {
	if k <= 0 {
		return 0
	}
	shown, more := chipsOf(cell, k)
	if more > 0 {
		return len(shown) + 1
	}
	return len(shown)
}

// fitChipRows is the most chip lines, up to the node's chipRows, a cell draws so the grid fits the rows its slot gives it;
// 0 when even the labels alone do not.
func (g *viewGrid) fitChipRows() int {
	budget := 0
	if g.budget != nil {
		budget = g.budget()
	}
	for k := g.chipRows; k >= 1; k-- {
		rows := 0
		if len(g.head) > 0 {
			rows++
		}
		for r := 0; r*g.columns < len(g.cells); r++ {
			rows += g.rowHeight(r, k)
		}
		if budget <= 0 || rows <= budget {
			return k
		}
	}
	return 0
}

func (g *viewGrid) rowHeight(r, k int) int {
	height := 1
	for i := r * g.columns; i < min((r+1)*g.columns, len(g.cells)); i++ {
		height = max(height, 1+chipLines(&g.cells[i], k))
	}
	return height
}

const (
	viewGridSep    = "│"
	viewGridCursor = "▸"
	viewGridChip   = "▪"
)

func gridClean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func gridPad(s string, width int) string {
	if w := widthx.VisibleWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

// Render draws the grid at width: equal columns of (width - separators) / columns cells, every row exactly width cells.
func (g *viewGrid) Render(width int) []string {
	n := g.columns
	g.lay = viewGridLayout{}
	if width < 1 {
		return nil
	}
	if width < n {
		labels := make([]string, len(g.cells))
		for i := range g.cells {
			labels[i] = gridClean(g.cells[i].Label)
		}
		return []string{gridPad(widthx.TruncateToWidth(strings.Join(labels, " "), width, "…", false), width)}
	}
	sep := 0
	if width >= 3*n-1 {
		sep = 1
	}
	w := (width - sep*(n-1)) / n
	k := g.fitChipRows()
	var lines []string
	join := func(cols []string) string {
		glue := ""
		if sep == 1 {
			glue = g.theme.Fg("muted", viewGridSep)
		}
		return gridPad(strings.Join(cols, glue), width)
	}
	if len(g.head) > 0 {
		cols := make([]string, n)
		for c := range cols {
			text := ""
			if c < len(g.head) {
				text = widthx.TruncateToWidth(" "+gridClean(g.head[c]), w, "…", false)
			}
			cols[c] = g.theme.Fg("muted", g.theme.Bold(text)) + strings.Repeat(" ", w-widthx.VisibleWidth(text))
		}
		lines = append(lines, join(cols))
	}
	g.lay.sep, g.lay.cellW = sep, w
	for r := 0; r*n < len(g.cells); r++ {
		height := g.rowHeight(r, k)
		g.lay.rowTop = append(g.lay.rowTop, len(lines))
		g.lay.rowHeight = append(g.lay.rowHeight, height)
		for line := range height {
			cols := make([]string, n)
			for c := range cols {
				i := r*n + c
				if i >= len(g.cells) {
					cols[c] = strings.Repeat(" ", w)
					continue
				}
				cols[c] = g.cellLine(&g.cells[i], line, k, w)
			}
			lines = append(lines, join(cols))
		}
	}
	return lines
}

// cellLine is line i of a cell, exactly w cells wide.
func (g *viewGrid) cellLine(cell *ViewGridCell, line, k, w int) string {
	if line == 0 {
		return g.label(cell, k, w)
	}
	shown, more := chipsOf(cell, k)
	switch {
	case k <= 0 || line > len(shown)+btoi(more > 0):
		return strings.Repeat(" ", w)
	case line <= len(shown):
		return g.chip(shown[line-1], cell.Dim, w)
	}
	text := widthx.TruncateToWidth(" +"+strconv.Itoa(more)+" more", w, "…", false)
	return g.theme.Fg("muted", text) + strings.Repeat(" ", w-widthx.VisibleWidth(text))
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// label is a cell's first line: the cursor glyph, the label and, when no chip line fits, the count of its events.
func (g *viewGrid) label(cell *ViewGridCell, k, w int) string {
	cursor := cell.Value != "" && cell.Value == g.cursor
	prefix := " "
	if cursor {
		prefix = viewGridCursor
	}
	text := prefix + gridClean(cell.Label)
	if _, hidden := chipsOf(cell, k); k <= 0 && hidden > 0 {
		text += " +" + strconv.Itoa(hidden)
	}
	text = widthx.TruncateToWidth(text, w, "…", false)
	pad := strings.Repeat(" ", w-widthx.VisibleWidth(text))
	switch {
	case cell.Dim:
		text = g.theme.Fg("muted", text)
	case cell.Color != "":
		text = g.fg(cell.Color, text)
	}
	if cell.Mark == ViewGridMarkCurrent {
		text = g.theme.Bold(g.theme.Inverse(text))
	}
	if cursor && g.focused {
		text = g.theme.Underline(text)
	}
	return text + pad
}

// color resolves a grid colour: a theme token through the theme, "#rrggbb" or "#rrggbbaa" (the alpha is dropped: a
// terminal has none) through tui.ParseColor.
func (g *viewGrid) color(color string) (tui.Color, bool) {
	if color == "" {
		return nil, false
	}
	if color[0] != '#' {
		c, ok := g.theme.Colors()[color]
		return c, ok
	}
	c, err := tui.ParseColor(color[:7])
	return c, err == nil
}

func (g *viewGrid) fg(color, text string) string {
	c, ok := g.color(color)
	if !ok {
		return text
	}
	return tui.ForegroundAnsi(c, g.theme.GetColorMode()) + text + tui.SGRFgReset
}

// chip is one chip line: the chip glyph and the text, truncated by cells, on the chip's colour with the foreground that
// contrasts it most (gridContrast); a dimmed cell or a chip without a colour is plain text, so a chip is told from a
// label by its glyph in every colour mode.
func (g *viewGrid) chip(chip ViewGridChip, dim bool, w int) string {
	text := widthx.TruncateToWidth(viewGridChip+gridClean(chip.Text), w, "…", false)
	pad := strings.Repeat(" ", w-widthx.VisibleWidth(text))
	if dim {
		return g.theme.Fg("muted", text) + pad
	}
	bg, ok := g.color(chip.Color)
	if !ok {
		return text + pad
	}
	mode := g.theme.GetColorMode()
	return tui.BackgroundAnsi(bg, mode) + tui.ForegroundAnsi(gridContrast(bg), mode) + text + pad + tui.SGRFgReset + tui.SGRBgReset
}

// gridContrast is the foreground of a chip on bg: black or white, whichever has the higher WCAG contrast with it (black
// when they are equal).
func gridContrast(bg tui.Color) tui.Color {
	rgb := tui.ColorToRgb(bg)
	black, white := tui.RgbColor{}, tui.RgbColor{R: 255, G: 255, B: 255}
	if tui.WcagContrast(rgb, white) > tui.WcagContrast(rgb, black) {
		return tui.RgbColorValue(white)
	}
	return tui.RgbColorValue(black)
}
