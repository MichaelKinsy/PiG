package tui

import "testing"

// Pi's calculateImageRows(dims, targetWidthCells, cellDimensions = 9x18) is calculateImageCellSize(...).rows with no
// height limit: the image scales to the target width and rows round up.
func TestCalculateImageRowsScalesToTheTargetWidth(t *testing.T) {
	cell := CellDimensions{WidthPx: 9, HeightPx: 18}
	cases := []struct {
		name  string
		image ImageDimensions
		width int
		want  int
	}{
		{"square at ten cells", ImageDimensions{WidthPx: 100, HeightPx: 100}, 10, 5},
		{"wide image", ImageDimensions{WidthPx: 200, HeightPx: 50}, 20, 3},
		{"tall image rounds up", ImageDimensions{WidthPx: 90, HeightPx: 200}, 10, 12},
		{"zero target width clamps to one cell", ImageDimensions{WidthPx: 9, HeightPx: 18}, 0, 1},
	}
	for _, tc := range cases {
		got := CalculateImageRows(tc.image, tc.width, cell)
		if got != tc.want {
			t.Errorf("%s: rows = %d, want %d", tc.name, got, tc.want)
		}
		if size := CalculateImageCellSize(tc.image, tc.width, 0, cell); got != size.Rows {
			t.Errorf("%s: rows = %d, want calculateImageCellSize rows %d", tc.name, got, size.Rows)
		}
	}
}
