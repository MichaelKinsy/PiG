package kit

// Grid is the grid node of D614 (the cell form): a calendar-style grid of
// equal columns whose cells are data, not nodes, so a month of events is one
// node. The host is its only validator and answers a mount or an update that
// breaks a rule with code invalid: Columns is 1 to 16, Head is empty or one
// label a column (64 bytes each), ChipRows 1 to 8, at most 128 cells in at
// most 32 rows, cell values unique and Selected one of them. An intent whose
// NodeKey is the grid's ID is raised with {"value": <the cell's Value>} when
// the user chooses a cell.
type Grid struct {
	// ID names the grid; it is required and unique within the view.
	ID string
	// Columns is the count of equal columns.
	Columns int
	// Head is the column labels: none, or as many as Columns.
	Head []string
	// ChipRows is the most chip lines a cell draws; nil takes the host's 3.
	ChipRows *int
	// Selected is the Value of the cell the cursor starts on; empty for none.
	Selected string
	// Cells are the cells row by row; the last row may be short.
	Cells []GridCell
}

// GridCell is one cell of a [Grid]. Value names it in the intent a choice
// raises (at most 256 bytes; a cell without one cannot be chosen), Label is
// its first line (at most 64 bytes), Mark "current" draws it as today, Dim as
// outside the shown range, and Color tints the label (a theme token, #rrggbb
// or #rrggbbaa). Chips are the events it shows (at most 8) and More counts
// the events the sender left out (0 to 9999). A member left at its zero value
// is not sent.
type GridCell struct {
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
	Mark  string `json:"mark,omitempty"`
	Dim   bool   `json:"dim,omitempty"`
	Color string `json:"color,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty chip list mean the same: none
	Chips []GridChip `json:"chips,omitempty"`
	More  int        `json:"more,omitempty"`
}

// GridChip is one event of a cell: its text (at most 256 bytes) and its
// colour (a theme token, #rrggbb or #rrggbbaa; empty for none).
type GridChip struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// NewGrid returns a Grid named id of columns equal columns over cells.
func NewGrid(id string, columns int, cells []GridCell) *Grid {
	return &Grid{ID: id, Columns: columns, Cells: cells}
}

func (*Grid) isNode() {}

type gridFields struct {
	Columns  int  `json:"columns"`
	ChipRows *int `json:"chipRows,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty head mean the same: none
	Head []string `json:"head,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty cell list mean the same: none
	Cells    []GridCell `json:"cells,omitempty"`
	Selected string     `json:"selected,omitempty"`
}

// WireNode implements [Wired].
func (g *Grid) WireNode() WireForm {
	return WireForm{Kind: "grid", ID: g.ID, Fields: gridFields{
		Columns: g.Columns, ChipRows: g.ChipRows, Head: g.Head, Cells: g.Cells, Selected: g.Selected,
	}}
}
