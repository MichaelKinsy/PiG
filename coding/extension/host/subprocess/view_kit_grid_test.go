package subprocess

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// gridMonthJSON is November 2026 as a grid: Monday first, six weeks of seven cells, the 12th today, the days of other
// months dim, and chips events on each day (every day with chips left out as "more" past three).
func gridMonthJSON(chips int) string {
	var cells []string
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for range 42 {
		var cs []string
		for c := range min(chips, 3) {
			cs = append(cs, fmt.Sprintf(`{"text":"Event %d of day %d","color":"%s"}`, c+1, day.Day(), []string{"#3b82f6", "accent", "#f59e0b"}[c]))
		}
		cell := fmt.Sprintf(`{"value":%q,"label":"%d","chips":[%s],"more":%d`, day.Format("2006-01-02"), day.Day(), strings.Join(cs, ","), max(chips-3, 0))
		if day.Month() != time.November {
			cell += `,"dim":true`
		}
		if day.Day() == 12 && day.Month() == time.November {
			cell += `,"mark":"current"`
		}
		cells = append(cells, cell+"}")
		day = day.AddDate(0, 0, 1)
	}
	return `{"kind":"grid","id":"cal","columns":7,"head":["Mon","Tue","Wed","Thu","Fri","Sat","Sun"],"selected":"2026-11-12","cells":[` + strings.Join(cells, ",") + `]}`
}

func mountContent(root string) uiview.Content {
	return uiview.Content{View: json.RawMessage(`{"root":` + root + `}`)}
}

func gridCheck(raw string) error {
	var n ViewNode
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		return err
	}
	c := viewChecker{ids: map[string]string{}, theme: tui.ActiveTheme()}
	c.node(&n, 1)
	return c.err
}

// pig additive (D614): every bound of the grid holds at its limit and is rejected one past it, and a member the record
// does not have is rejected in the node, a cell and a chip.
func TestGridValidatorBounds(t *testing.T) {
	cells := func(n int) string {
		return `"cells":[` + strings.TrimSuffix(strings.Repeat(`{"label":"x"},`, n), ",") + `]`
	}
	cases := []struct {
		name, ok, bad string
	}{
		{"columns", `{"kind":"grid","id":"g","columns":16}`, `{"kind":"grid","id":"g","columns":17}`},
		{"columns zero", `{"kind":"grid","id":"g","columns":1}`, `{"kind":"grid","id":"g","columns":0}`},
		{"no columns", `{"kind":"grid","id":"g","columns":2}`, `{"kind":"grid","id":"g"}`},
		{"id", `{"kind":"grid","id":"g","columns":2}`, `{"kind":"grid","columns":2}`},
		{"head", `{"kind":"grid","id":"g","columns":2,"head":["a","b"]}`, `{"kind":"grid","id":"g","columns":2,"head":["a","b","c"]}`},
		{"head bytes", `{"kind":"grid","id":"g","columns":1,"head":["` + strings.Repeat("h", 64) + `"]}`, `{"kind":"grid","id":"g","columns":1,"head":["` + strings.Repeat("h", 65) + `"]}`},
		{"chipRows", `{"kind":"grid","id":"g","columns":1,"chipRows":8}`, `{"kind":"grid","id":"g","columns":1,"chipRows":9}`},
		{"chipRows zero", `{"kind":"grid","id":"g","columns":1,"chipRows":1}`, `{"kind":"grid","id":"g","columns":1,"chipRows":0}`},
		{"cells", `{"kind":"grid","id":"g","columns":16,` + cells(128) + `}`, `{"kind":"grid","id":"g","columns":16,` + cells(129) + `}`},
		{"rows", `{"kind":"grid","id":"g","columns":4,` + cells(128) + `}`, `{"kind":"grid","id":"g","columns":3,` + cells(97) + `}`},
		{"value bytes", `{"kind":"grid","id":"g","columns":1,"cells":[{"value":"` + strings.Repeat("v", 256) + `"}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"value":"` + strings.Repeat("v", 257) + `"}]}`},
		{"label bytes", `{"kind":"grid","id":"g","columns":1,"cells":[{"label":"` + strings.Repeat("l", 64) + `"}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"label":"` + strings.Repeat("l", 65) + `"}]}`},
		{"chips", `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[` + strings.TrimSuffix(strings.Repeat(`{"text":"c"},`, 8), ",") + `]}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[` + strings.TrimSuffix(strings.Repeat(`{"text":"c"},`, 9), ",") + `]}]}`},
		{"chip bytes", `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"` + strings.Repeat("c", 256) + `"}]}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"` + strings.Repeat("c", 257) + `"}]}]}`},
		{"more", `{"kind":"grid","id":"g","columns":1,"cells":[{"more":9999}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"more":10000}]}`},
		{"more negative", `{"kind":"grid","id":"g","columns":1,"cells":[{"more":0}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"more":-1}]}`},
		{"mark", `{"kind":"grid","id":"g","columns":1,"cells":[{"mark":"current"}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"mark":"today"}]}`},
		{"colour token", `{"kind":"grid","id":"g","columns":1,"cells":[{"color":"accent","chips":[{"text":"c","color":"#3b82f6"}]}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"color":"nosuchtoken"}]}`},
		{"colour hex", `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"c","color":"#3b82f680"}]}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"c","color":"#3b82f"}]}]}`},
		{"duplicate value", `{"kind":"grid","id":"g","columns":2,"cells":[{"value":"a"},{"value":"b"}]}`, `{"kind":"grid","id":"g","columns":2,"cells":[{"value":"a"},{"value":"a"}]}`},
		{"selected", `{"kind":"grid","id":"g","columns":1,"selected":"a","cells":[{"value":"a"}]}`, `{"kind":"grid","id":"g","columns":1,"selected":"b","cells":[{"value":"a"}]}`},
		{"children", `{"kind":"grid","id":"g","columns":1}`, `{"kind":"grid","id":"g","columns":1,"children":[{"kind":"text","text":"x"}]}`},
		{"unknown node member", `{"kind":"grid","id":"g","columns":1}`, `{"kind":"grid","id":"g","columns":1,"rowLabels":["a"]}`},
		{"unknown cell member", `{"kind":"grid","id":"g","columns":1,"cells":[{"label":"a"}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"label":"a","kind":"text"}]}`},
		{"unknown chip member", `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"a"}]}]}`, `{"kind":"grid","id":"g","columns":1,"cells":[{"chips":[{"text":"a","bg":"x"}]}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := gridCheck(tc.ok); err != nil {
				t.Errorf("the grid at its limit was rejected: %v", err)
			}
			if err := gridCheck(tc.bad); err == nil {
				t.Errorf("the grid one past its limit was accepted: %.120s", tc.bad)
			}
			// The mount path runs the same function: the same grid is rejected there.
			content := mountContent(tc.bad)
			if err := checkMountGrids(&content); err == nil {
				t.Errorf("checkMountGrids accepted the grid one past its limit")
			}
		})
	}
}

// pig additive (D614): the bounds hold at their limit in one grid at once: the largest legal grid is valid.
func TestGridLargestLegalGridIsValid(t *testing.T) {
	var cells []string
	chip := `{"text":"` + strings.Repeat("c", 256) + `","color":"#3b82f6"}`
	for i := range 128 {
		cells = append(cells, fmt.Sprintf(`{"value":"v%d","label":"%d","chips":[%s]}`, i, i, strings.TrimSuffix(strings.Repeat(chip+",", 8), ",")))
	}
	raw := `{"kind":"grid","id":"g","columns":4,"cells":[` + strings.Join(cells, ",") + `]}`
	if err := gridCheck(raw); err != nil {
		t.Fatal(err)
	}
	t.Logf("the largest legal grid is %d bytes of view JSON", len(raw))
}

// pig additive (D614): a month with three chips a day and a week of 20 events are three nodes, against the 281, 407, 241
// and 477 of the nested stacks that drew them.
func TestGridMonthAndWeekCostThreeNodes(t *testing.T) {
	week := `{"kind":"grid","id":"week","columns":7,"head":["Mon 9","Tue 10","Wed 11","Thu 12","Fri 13","Sat 14","Sun 15"],"chipRows":8,"cells":[`
	for d := range 7 {
		var chips []string
		for e := range 3 {
			chips = append(chips, fmt.Sprintf(`{"text":"%02d:00 event %d","color":"#3b82f6"}`, 8+e, d*3+e))
		}
		if d > 0 {
			week += ","
		}
		week += fmt.Sprintf(`{"value":"d%d","label":"%d","chips":[%s],"more":%d}`, d, 9+d, strings.Join(chips, ","), 0)
	}
	week += `]}`
	for name, grid := range map[string]string{"month": gridMonthJSON(3), "week": week} {
		root := `{"root":{"kind":"vstack","children":[{"kind":"text","text":"title"},` + grid + `]}}`
		var payload ViewPayload
		if err := json.Unmarshal([]byte(root), &payload); err != nil {
			t.Fatal(err)
		}
		c := viewChecker{ids: map[string]string{}, theme: tui.ActiveTheme()}
		c.node(&payload.Root, 1)
		if c.err != nil || c.count != 3 {
			t.Fatalf("%s: nodes %d, err %v, want 3 nodes", name, c.count, c.err)
		}
		t.Logf("%s: %d nodes, %d bytes", name, c.count, len(root))
	}
}

// pig additive (D614): a chip's foreground is the one of black and white with the higher WCAG contrast with its colour,
// black when they are equal.
func TestGridContrast(t *testing.T) {
	for _, tc := range []struct {
		color string
		black bool
	}{
		{"#000000", false}, {"#ffffff", true}, {"#3b82f6", true}, {"#f59e0b", true}, {"#22c55e", true},
		{"#767676", true}, {"#757575", false}, {"#808080", true}, {"#777777", true}, {"#ef4444", true}, {"#7c3aed", false},
	} {
		color, err := tui.ParseColor(tc.color)
		if err != nil {
			t.Fatal(err)
		}
		got := tui.ColorToRgb(gridContrast(color))
		if black := got.R == 0; black != tc.black {
			t.Errorf("%s: foreground black=%v, want %v", tc.color, black, tc.black)
		}
	}
}

// gridSurface draws a view in a surface the way a terminal mount does.
func gridSurface(t *testing.T, root string, budget int) *MountSurface {
	t.Helper()
	m := NewMountSurface(MountSurfaceOptions{RowBudget: func() int { return budget }})
	t.Cleanup(m.Close)
	if err := m.Accept(json.RawMessage(`{"root":`+root+`}`), "cal"); err != nil {
		t.Fatal(err)
	}
	return m
}

// pig additive (D614): at 40, 80 and 120 columns a month's rows are exactly the width, its seven columns are equal, the
// today cell is bold inverse, the chips are cut by cells with an ellipsis, and the head names the days.
func TestGridRendersEqualColumnsAtEveryWidth(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			rows := gridSurface(t, gridMonthJSON(5), 0).Render(width)
			if len(rows) == 0 {
				t.Fatal("nothing drawn")
			}
			cellW := (width - 6) / 7
			for i, row := range rows {
				if w := widthx.VisibleWidth(row); w != width {
					t.Fatalf("row %d is %d cells, want %d: %q", i, w, width, widthx.StripAnsi(row))
				}
				plain := []rune(widthx.StripAnsi(row))
				var at []int
				for col, r := range plain {
					if r == '│' {
						at = append(at, col)
					}
				}
				if len(at) != 6 {
					t.Fatalf("row %d has %d separators, want 6: %q", i, len(at), string(plain))
				}
				for c, col := range at {
					if want := (c+1)*cellW + c; col != want {
						t.Fatalf("row %d: separator %d at cell %d, want %d (equal columns of %d)", i, c, col, want, cellW)
					}
				}
			}
			all := widthx.StripAnsi(strings.Join(rows, "\n"))
			for _, want := range []string{"Mon", "Sun", "+3", "…", "+3 more"} {
				if width >= 80 && want == "…" || width < 80 && want == "+3 more" {
					continue
				}
				if !strings.Contains(all, want) {
					t.Errorf("the grid at %d does not show %q:\n%s", width, want, all)
				}
			}
			if !strings.Contains(strings.Join(rows, "\n"), "\x1b[1m\x1b[7m") && !strings.Contains(strings.Join(rows, "\n"), "\x1b[7m") {
				t.Error("the today cell is not drawn inverse")
			}
			if !strings.Contains(strings.Join(rows, "\n"), "\x1b[48;2;59;130;246m") {
				t.Error("the blue chip is not drawn on its colour")
			}
		})
	}
}

// pig additive (D614): wide characters and emoji are cut by terminal cells and never overflow a column.
func TestGridCutsByTerminalCells(t *testing.T) {
	root := `{"kind":"grid","id":"cal","columns":3,"cells":[{"value":"a","label":"日本語のラベル","chips":[{"text":"会議 🎉🎉🎉 長いイベント名","color":"#22c55e"},{"text":"👩‍💻👩‍💻👩‍💻👩‍💻"}]},{"value":"b","label":"b"},{"value":"c","label":"c"}]}`
	for _, width := range []int{9, 17, 40} {
		for _, row := range gridSurface(t, root, 0).Render(width) {
			if w := widthx.VisibleWidth(row); w != width {
				t.Errorf("width %d: a row is %d cells: %q", width, w, widthx.StripAnsi(row))
			}
		}
	}
}

// pig additive (D614): a grid that cannot get a column per cell draws one truncated line of labels, and a slot that cuts
// the view makes the grid draw fewer chip lines, down to none, before the slot cuts it.
func TestGridFitsItsSlot(t *testing.T) {
	narrow := gridSurface(t, gridMonthJSON(3), 0).Render(5)
	if len(narrow) != 1 || widthx.VisibleWidth(narrow[0]) != 5 {
		t.Fatalf("a grid narrower than its columns drew %q", narrow)
	}
	full := gridSurface(t, gridMonthJSON(3), 0).Render(80)
	if len(full) != 1+6*4 {
		t.Fatalf("a month of three chips drew %d rows, want 25", len(full))
	}
	for budget, want := range map[int]int{25: 25, 24: 19, 19: 19, 18: 13, 13: 13, 12: 7, 7: 7, 3: 7} {
		rows := gridSurface(t, gridMonthJSON(3), budget).Render(80)
		if len(rows) != want {
			t.Errorf("budget %d: %d rows, want %d", budget, len(rows), want)
		}
	}
	labels := widthx.StripAnsi(strings.Join(gridSurface(t, gridMonthJSON(3), 7).Render(80), "\n"))
	if !strings.Contains(labels, " +3") || strings.Contains(labels, "more") {
		t.Errorf("with no chip line the cell states its count on the label line:\n%s", labels)
	}
}
