package tui

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/tui/src/tui-main-screen.ts doRender, packages/tui/src/tui.ts applyLineResets

// loneSurrogateRows are rows of exactly 20 cells by Pi's visibleWidth (an unpaired surrogate is zero cells), as UTF-16 units.
func loneSurrogateRows() [][]uint16 {
	twenty := func(extra ...uint16) []uint16 {
		row := make([]uint16, 0, 20+len(extra))
		for range 20 {
			row = append(row, 'x')
		}
		return append(row, extra...)
	}
	return [][]uint16{
		twenty(0xd83d),
		twenty(0xde00),
		append([]uint16{0xd83d}, twenty()...),
		append(twenty(0xd83d), 0xde00, 0xde00),
		append(twenty()[:10], append([]uint16{0xd83d, 0x0301}, twenty()[:10]...)...),
		append(twenty(0xd83d), 0x1b, '[', '7', 'm', 0xde00),
	}
}

// A row that fills the terminal and holds an unpaired surrogate is 20 cells wide to Pi, which writes the surrogate to Node's stdout (encoded as U+FFFD)
// after the check, so the differential renderer neither crashes nor changes the row.
func TestMainScreenLoneSurrogateRowMatchesPi(t *testing.T) {
	rows := loneSurrogateRows()
	type result struct {
		Out   []byte `json:"out"`
		Error string `json:"error"`
	}
	var want []result
	pioracle.Run(t, `
const { TuiMainScreen } = await load("pi-tui/tui-main-screen.js");
const results = [];
for (const codes of input) {
	let out = "";
	const term = {
		columns: 20, rows: 10, kittyProtocolActive: false,
		start() {}, stop() {}, async drainInput() {},
		write(d) { out += d; },
		moveBy(n) { if (n > 0) out += "\x1b[" + n + "B"; else if (n < 0) out += "\x1b[" + -n + "A"; },
		hideCursor() { out += "\x1b[?25l"; }, showCursor() { out += "\x1b[?25h"; },
		clearLine() {}, clearFromCursor() {}, clearScreen() {}, setTitle() {}, setProgress() {},
	};
	const ui = new TuiMainScreen(term);
	let lines = ["fits", "ok"];
	ui.addChild({ invalidate() {}, render() { return lines; } });
	ui.renderNow(false);
	out = "";
	lines = ["fits", String.fromCharCode(...codes)];
	let error = "";
	try { ui.renderNow(false); } catch (e) { error = String(e && e.message).split("\n")[0]; }
	// Node encodes the frame as UTF-8 when it writes it to stdout.
	results.push({ out: Buffer.from(out, "utf8").toString("base64"), error });
}
emit(results);`, rows, &want)
	for i, codes := range rows {
		var out bytes.Buffer
		lines := []string{"fits", "ok"}
		ui, _ := newOverflowTUI(t, &out, 20, 10, &lines)
		ui.Render()
		out.Reset()
		lines = []string{"fits", jsstring.FromUTF16(codes)}
		value := renderRecover(ui)
		if (want[i].Error != "") != (value != nil) {
			t.Errorf("row %x: PiG panic %v, Pi error %q", codes, value, want[i].Error)
			continue
		}
		if !bytes.Equal(out.Bytes(), want[i].Out) {
			t.Errorf("row %x: PiG wrote %q, Pi %q", codes, out.Bytes(), want[i].Out)
		}
	}
}

// The process terminal encodes the frame like Node's stdout, so the unpaired surrogate reaches the screen as U+FFFD.
func TestMainScreenLoneSurrogateRowIsEncodedByProcessTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "20")
	t.Setenv("LINES", "10")
	var out bytes.Buffer
	lines := []string{"fits", "ok"}
	ui := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &out), nil, t.TempDir())
	ui.Add(renderFuncComponent(func(int) []string { return lines }))
	ui.Render()
	out.Reset()
	lines = []string{"fits", strings.Repeat("x", 20) + jsstring.FromUTF16([]uint16{0xd83d})}
	if value := renderRecover(ui); value != nil {
		t.Fatalf("render terminated: %v", value)
	}
	if got := out.String(); !strings.Contains(got, strings.Repeat("x", 20)+"\uFFFD") || !utf8.ValidString(got) {
		t.Fatalf("bytes = %q, want the row ending in U+FFFD as valid UTF-8", got)
	}
}
