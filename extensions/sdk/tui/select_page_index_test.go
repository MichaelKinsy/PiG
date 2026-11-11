package tui

import "testing"

func TestGetSelectPageIndex(t *testing.T) {
	kb := NewKeybindingsManager(TUIKeybindings, nil)
	const pageUp, pageDown = "\x1b[5~", "\x1b[6~"
	cases := []struct {
		name         string
		data         string
		selected     int
		count, size  int
		want         int
		wantRecorded bool
	}{
		{"page down", pageDown, 2, 20, 5, 7, true},
		{"page up", pageUp, 7, 20, 5, 2, true},
		{"clamps at the end", pageDown, 18, 20, 5, 19, true},
		{"clamps at the start", pageUp, 1, 20, 5, 0, true},
		{"empty list", pageDown, 0, 0, 5, 0, true},
		{"other key", "a", 3, 20, 5, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := GetSelectPageIndex(kb, tc.data, tc.selected, tc.count, tc.size)
			if got != tc.want || ok != tc.wantRecorded {
				t.Fatalf("GetSelectPageIndex = %d, %v; want %d, %v", got, ok, tc.want, tc.wantRecorded)
			}
		})
	}
}
