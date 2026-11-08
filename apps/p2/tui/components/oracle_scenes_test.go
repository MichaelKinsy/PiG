package components

import (
	"fmt"
	"strings"
	"testing"
)

// The scenes of the oracle comparison; testdata/oracle_probe.mjs builds the same ones from the same data.

const (
	keyDownArrow = "\x1b[B"
	keyUpArrow   = "\x1b[A"
	keyPageDown  = "\x1b[6~"
	keyPageUp    = "\x1b[5~"
)

func noRun(*ListRow) {}

func manyRows() []ListRow {
	colors := []string{"accent", "muted", "warning", "error", "success"}
	rows := make([]ListRow, 30)
	for i := range rows {
		row := ListRow{Key: fmt.Sprintf("k%d", i), Label: fmt.Sprintf("row %d alpha", i)}
		if i%7 == 3 {
			row.Label = fmt.Sprintf("row %d with a very long label that has to be truncated to fit the narrow viewport at hand", i)
		}
		if i%3 == 0 {
			row.Detail = fmt.Sprintf("d%d", i)
		}
		if i%5 == 0 {
			row.Description = fmt.Sprintf("Description of row %d that is long enough to be cut off at narrow widths, with \x1b]52;c;evil\x07 control text", i)
		}
		if i%4 == 1 {
			row.Color = colors[i%5]
		}
		rows[i] = row
	}
	return rows
}

func wideRows() []ListRow {
	return []ListRow{
		{Key: "w1", Label: "日本語のラベル和中文混合", Detail: "詳細"},
		{Key: "w2", Label: "emoji 👨‍👩‍👧‍👦 row\x1b[31mred\x1b[0m\ttab", Detail: "x\ny"},
		{Key: "w3", Label: ""},
	}
}

func viewerLines(width int) []string {
	lines := make([]string, 60)
	for i := range lines {
		if i%9 == 0 {
			lines[i] = fmt.Sprintf("line %d %s", i, strings.Repeat("x", width))
		} else {
			lines[i] = fmt.Sprintf("line %d \x1b[32mgreen\x1b[0m 日本語", i)
		}
	}
	return lines
}

func init() {
	noop := func() {}
	registerScene("list-basic", func(t testing.TB, ctx oracleContext) oracleScene {
		rows := manyRows()
		return oracleScene{
			component: NewListOverlay(ctx, ListOverlayOptions{
				Title: func() string { return "Pick a thing" }, Rows: func() []ListRow { return rows },
				OnSelect: func(ListRow) {}, OnClose: noop,
				Actions: []ListAction{{ID: "app.session.delete", Label: "delete", Run: noRun}, {Key: "ctrl+t", Label: "toggle", Run: noRun}},
				Footer: func() []string {
					return []string{"A footer note that explains the status of things and wraps over several rows at narrow widths for sure ok", "second"}
				},
			}),
			focus:  new(true),
			inputs: []string{keyDownArrow, keyDownArrow, keyPageDown, keyPageDown, keyUpArrow, keyPageUp, "a", "l", keyDownArrow, "\x7f", keyEsc},
		}
	})
	registerScene("list-short", func(t testing.TB, ctx oracleContext) oracleScene {
		rows := wideRows()
		return oracleScene{
			component: NewListOverlay(ctx, ListOverlayOptions{Title: func() string { return "Wide 日本語 title" }, Rows: func() []ListRow { return rows }, OnClose: noop, Height: 5}),
			focus:     new(true),
			inputs:    []string{keyDownArrow, keyDownArrow, keyDownArrow, "zzz"},
		}
	})
	registerScene("list-empty", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewListOverlay(ctx, ListOverlayOptions{Title: func() string { return "Empty" }, Rows: func() []ListRow { return nil }, OnClose: noop, Selected: new("none"), Footer: func() []string { return nil }}),
			focus:     new(false),
			inputs:    []string{keyDownArrow, keyEnter},
		}
	})
	registerScene("list-follow", func(t testing.TB, ctx oracleContext) oracleScene {
		rows := manyRows()
		return oracleScene{
			component: NewListOverlay(ctx, ListOverlayOptions{Title: func() string { return "Log" }, Rows: func() []ListRow { return rows }, OnClose: noop, Follow: true, Selected: new("k3"), Height: 6}),
			focus:     new(true),
			inputs:    []string{keyUpArrow, keyUpArrow, keyDownArrow, keyDownArrow, keyDownArrow},
		}
	})
	registerScene("list-preselect", func(t testing.TB, ctx oracleContext) oracleScene {
		rows := manyRows()
		return oracleScene{
			component: NewListOverlay(ctx, ListOverlayOptions{Title: func() string { return "Pre" }, Rows: func() []ListRow { return rows }, OnClose: noop, Selected: new("k20"), Height: 7, OnSelect: func(ListRow) {}}),
			focus:     new(true),
			inputs:    []string{keyDownArrow},
		}
	})
	registerScene("confirm", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{component: NewConfirmOverlay(ctx, []string{"Delete the thing?", "It cannot be undone and this line is long enough to be truncated at narrow widths", "日本語"}, func(bool) {})}
	})
	registerScene("text-viewer", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewTextViewer(ctx, TextViewerOptions{Title: "File\x1b]0;t\x07 view 日本語", Lines: viewerLines, OnClose: noop, Height: 8, Line: 30}),
			inputs:    []string{keyDownArrow, keyDownArrow, keyPageDown, keyPageDown, keyPageDown, keyPageDown, keyPageDown, keyPageUp, keyUpArrow, keyEsc},
		}
	})
	registerScene("text-viewer-short", func(t testing.TB, ctx oracleContext) oracleScene {
		return oracleScene{
			component: NewTextViewer(ctx, TextViewerOptions{Title: "Short", Lines: func(int) []string { return []string{"only", "two"} }, OnClose: noop}),
			inputs:    []string{keyDownArrow, keyPageDown},
		}
	})
}
