package subprocess

// view_grid_wire.go: the wire of the grid node kind (D614, the cell form). A grid is one node whose cells are data, so a
// month of coloured chips costs three nodes where nested stacks cost hundreds. The span form of a week or a day extends
// the same kind later with `spans`, `rowLabels` and `marker`; it must not use the member `items`, which a ViewNode already
// has for lists.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ViewKindGrid is the grid node kind.
const ViewKindGrid = "grid"

// pig additive (D614): the bounds of a grid. One past any of them is rejected with code invalid.
const (
	ViewGridMaxColumns      = 16
	ViewGridMaxRows         = 32
	ViewGridMaxCells        = 128
	ViewGridMaxChips        = 8
	ViewGridMaxChipRows     = 8
	ViewGridDefaultChipRows = 3
	ViewGridMaxHeadBytes    = 64
	ViewGridMaxLabelBytes   = 64
	ViewGridMaxValueBytes   = 256
	ViewGridMaxChipBytes    = 256
	ViewGridMaxMore         = 9999
)

// ViewGridMarkCurrent is the one cell mark: today.
const ViewGridMarkCurrent = "current"

// ViewGridFields are the ViewNode fields of a grid. Columns is the count of equal columns, Head the optional column
// labels (as many as columns), ChipRows the most chip lines a cell draws (default 3), Cells the cells row by row (the
// last row may be short) and Selected the value of the cell the cursor starts on.
type ViewGridFields struct {
	Columns  *int `json:"columns,omitempty"`
	ChipRows *int `json:"chipRows,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty head mean the same: none
	Head []string `json:"head,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty cell list mean the same: none
	Cells    []ViewGridCell `json:"cells,omitempty"`
	Selected string         `json:"selected,omitempty"`
}

// ViewGridCell is one cell: Value names it in the intent a choice raises (a cell without one cannot be chosen), Label is
// its first line, Mark "current" draws it as today, Dim as outside the shown range, Color tints the label (a theme
// token, #rrggbb or #rrggbbaa). Chips are the events it shows, and More counts the events the sender left out.
type ViewGridCell struct {
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
	Mark  string `json:"mark,omitempty"`
	Dim   bool   `json:"dim,omitempty"`
	Color string `json:"color,omitempty"`
	//portlint:allow emptydrop the D107 view wire is PiG-owned with no Pi counterpart, and an absent and an empty chip list mean the same: none
	Chips []ViewGridChip `json:"chips,omitempty"`
	More  int            `json:"more,omitempty"`
}

// ViewGridChip is one event of a cell: its text and its colour (a theme token, #rrggbb or #rrggbbaa).
type ViewGridChip struct {
	Text  string `json:"text"`
	Color string `json:"color,omitempty"`
}

// viewGridWire is a grid node as it is read: every member of the node, and no other.
type viewGridWire struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id,omitempty"`
	Stack *ViewStackEntry `json:"stack,omitempty"`
	ViewGridFields
}

// decodeGridNode reads a grid node and rejects a member the record does not have, in the node, a cell or a chip.
func decodeGridNode(data []byte) error {
	var wire viewGridWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return fmt.Errorf("grid: %w", err)
	}
	return nil
}
