package ghostty_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
)

const wasmPath = "../vendor/ghostty/ghostty-vt.wasm"

// wasmSHA256 is the module Pi vendors (plugins/terminals/vendor/ghostty/README.md).
const wasmSHA256 = "ba4dfb2cdb5dfb1c9924552983e04af18d36a9b98de41a6aef74bed6d0202db2"

func wasm(t testing.TB) []byte {
	t.Helper()
	b, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != wasmSHA256 {
		t.Fatalf("vendored ghostty-vt.wasm is %x, want %s", sum, wasmSHA256)
	}
	return b
}

func load(t testing.TB) *ghostty.Ghostty {
	t.Helper()
	g, err := ghostty.Load(context.Background(), wasm(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close(context.Background()) })
	return g
}

func terminal(t testing.TB, g *ghostty.Ghostty, o ghostty.Options) *ghostty.VtTerminal {
	t.Helper()
	vt, err := g.Terminal(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vt.Free)
	return vt
}

// The corpus and Pi's answers to it: testdata/gen.mjs runs Pi's ghostty.ts over cases.json and writes golden.json.
type op struct {
	Op         string          `json:"op"`
	S          *string         `json:"s"`
	B          []int           `json:"b"`
	Repeat     int             `json:"repeat"`
	Cols       int             `json:"cols"`
	Rows       int             `json:"rows"`
	Scrollback int             `json:"scrollback"`
	Foreground int             `json:"foreground"`
	Background int             `json:"background"`
	Cursor     *int            `json:"cursor"`
	All        bool            `json:"all"`
	Value      int             `json:"value"`
	Ansi       bool            `json:"ansi"`
	Delta      json.RawMessage `json:"delta"`
	Code       string          `json:"code"`
	Key        string          `json:"key"`
	Shift      bool            `json:"shift"`
	Ctrl       bool            `json:"ctrl"`
	Alt        bool            `json:"alt"`
	Meta       bool            `json:"meta"`
	Rep        bool            `json:"rep"`
	Action     string          `json:"action"`
	Button     string          `json:"button"`
	X          float64         `json:"x"`
	Y          float64         `json:"y"`
	Width      float64         `json:"width"`
	Height     float64         `json:"height"`
	CellWidth  float64         `json:"cellWidth"`
	CellHeight float64         `json:"cellHeight"`
}

type testCase struct {
	Name       string          `json:"name"`
	Cols       int             `json:"cols"`
	Rows       int             `json:"rows"`
	Scrollback int             `json:"scrollback"`
	Colors     *ghostty.Colors `json:"-"`
	RawColors  *struct {
		Foreground int  `json:"foreground"`
		Background int  `json:"background"`
		Cursor     *int `json:"cursor"`
	} `json:"colors"`
	Replies bool            `json:"replies"`
	Scheme  json.RawMessage `json:"scheme"`
	Ops     []op            `json:"ops"`
}

// JSON shapes of the results, as Pi's objects serialize.
type cellJSON struct {
	Text  string `json:"text"`
	Width int    `json:"width"`
	Fg    int    `json:"fg"`
	Bg    int    `json:"bg"`
	Flags int    `json:"flags"`
}
type rowJSON struct {
	Y       int        `json:"y"`
	Cells   []cellJSON `json:"cells"`
	Wrapped bool       `json:"wrapped"`
}
type cursorJSON struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Style    string `json:"style"`
	Blinking bool   `json:"blinking"`
	Color    *int   `json:"color,omitempty"`
}
type frameJSON struct {
	Dirty      string      `json:"dirty"`
	Cols       int         `json:"cols"`
	Rows       int         `json:"rows"`
	Foreground int         `json:"foreground"`
	Background int         `json:"background"`
	Cursor     *cursorJSON `json:"cursor,omitempty"`
	Changed    []rowJSON   `json:"changed"`
}

func frameResult(f ghostty.Frame) frameJSON {
	out := frameJSON{Dirty: f.Dirty, Cols: f.Cols, Rows: f.Rows, Foreground: f.Foreground, Background: f.Background, Changed: []rowJSON{}}
	if f.Cursor != nil {
		out.Cursor = &cursorJSON{f.Cursor.X, f.Cursor.Y, f.Cursor.Style, f.Cursor.Blinking, f.Cursor.Color}
	}
	for _, r := range f.Changed {
		row := rowJSON{Y: r.Y, Wrapped: r.Wrapped, Cells: make([]cellJSON, len(r.Cells))}
		for i, c := range r.Cells {
			row.Cells[i] = cellJSON{c.Text, c.Width, c.Fg, c.Bg, c.Flags}
		}
		out.Changed = append(out.Changed, row)
	}
	return out
}

func b64(b []byte, ok bool) any {
	if !ok {
		return nil
	}
	return base64.StdEncoding.EncodeToString(b)
}

func digest(b []byte) map[string]any {
	sum := sha256.Sum256(b)
	d := map[string]any{"sha256": hex.EncodeToString(sum[:]), "length": len(b)}
	if len(b) <= 4096 {
		d["base64"] = base64.StdEncoding.EncodeToString(b)
	}
	return d
}

func (o op) input() []byte {
	if o.B != nil {
		b := make([]byte, len(o.B))
		for i, v := range o.B {
			b[i] = byte(v)
		}
		return b
	}
	return []byte(*o.S)
}

func TestMatchesPi(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	var raw []json.RawMessage
	cases, goldenRaw := mustRead(t, "testdata/cases.json"), mustRead(t, "testdata/golden.json")
	if err := json.Unmarshal(cases, &raw); err != nil {
		t.Fatal(err)
	}
	var golden []struct {
		Name    string            `json:"name"`
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatal(err)
	}
	g := load(t)
	for i, rc := range raw {
		var c testCase
		if err := json.Unmarshal(rc, &c); err != nil {
			t.Fatal(err)
		}
		want := golden[i]
		t.Run(c.Name, func(t *testing.T) {
			if want.Name != c.Name || len(want.Results) != len(c.Ops) {
				t.Fatalf("golden.json is stale: regenerate with testdata/gen.mjs")
			}
			var replies [][]byte
			options := ghostty.Options{Cols: c.Cols, Rows: c.Rows, Scrollback: c.Scrollback}
			if c.RawColors != nil {
				options.Colors = &ghostty.Colors{Foreground: c.RawColors.Foreground, Background: c.RawColors.Background, Cursor: c.RawColors.Cursor}
			}
			if c.Replies {
				options.OnReply = func(b []byte) { replies = append(replies, b) }
			}
			if c.Scheme != nil {
				var scheme *string
				if err := json.Unmarshal(c.Scheme, &scheme); err != nil {
					t.Fatal(err)
				}
				options.ColorScheme = func() string {
					if scheme == nil {
						return ""
					}
					return *scheme
				}
			}
			vt := terminal(t, g, options)
			for n, o := range c.Ops {
				got := run(t, g, vt, &replies, o)
				buf, err := json.Marshal(got)
				if err != nil {
					t.Fatal(err)
				}
				var gotAny, wantAny any
				if err := json.Unmarshal(buf, &gotAny); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(want.Results[n], &wantAny); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(gotAny, wantAny) {
					t.Fatalf("op %d (%s): got %s\nwant %s", n, o.Op, clip(buf), clip(want.Results[n]))
				}
			}
		})
	}
}

func clip(b []byte) string {
	if len(b) > 1500 {
		return string(b[:1500]) + "…"
	}
	return string(b)
}

func mustRead(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func run(t *testing.T, g *ghostty.Ghostty, vt *ghostty.VtTerminal, replies *[][]byte, o op) any {
	t.Helper()
	switch o.Op {
	case "write":
		in := o.input()
		n := max(o.Repeat, 1)
		for range n {
			vt.Write(in)
		}
		out := []any{}
		for _, r := range *replies {
			out = append(out, base64.StdEncoding.EncodeToString(r))
		}
		*replies = nil
		return map[string]any{"replies": out}
	case "resize":
		vt.Resize(o.Cols, o.Rows)
		return nil
	case "setColors":
		vt.SetColors(ghostty.Colors{Foreground: o.Foreground, Background: o.Background, Cursor: o.Cursor})
		return nil
	case "frame":
		return frameResult(vt.Frame(o.All))
	case "snapshot":
		return digest(vt.Snapshot())
	case "text":
		return vt.Text()
	case "title":
		return vt.Title()
	case "mode":
		return vt.Mode(o.Value, o.Ansi)
	case "alt":
		return vt.AlternateScreen()
	case "mouseTracking":
		return vt.MouseTracking()
	case "scrollbar":
		total, offset, length := vt.Scrollbar()
		return map[string]int{"total": total, "offset": offset, "length": length}
	case "scroll":
		if string(o.Delta) == `"bottom"` {
			vt.ScrollBottom()
		} else {
			var d int
			if err := json.Unmarshal(o.Delta, &d); err != nil {
				t.Fatal(err)
			}
			vt.Scroll(d)
		}
		return nil
	case "key":
		return b64(vt.EncodeKey(ghostty.KeyInput{Code: o.Code, Key: o.Key, Shift: o.Shift, Ctrl: o.Ctrl, Alt: o.Alt, Meta: o.Meta, Repeat: o.Rep}))
	case "mouse":
		return b64(vt.EncodeMouse(ghostty.MouseInput{Action: o.Action, Button: o.Button, X: o.X, Y: o.Y, Width: o.Width, Height: o.Height,
			CellWidth: o.CellWidth, CellHeight: o.CellHeight, Shift: o.Shift, Ctrl: o.Ctrl, Alt: o.Alt}))
	case "paste":
		return base64.StdEncoding.EncodeToString(vt.EncodePaste(*o.S))
	case "replay":
		fresh := terminal(t, g, ghostty.Options{Cols: o.Cols, Rows: o.Rows, Scrollback: o.Scrollback})
		fresh.Write(vt.Snapshot())
		result := map[string]any{"frame": frameResult(fresh.Frame(true)), "snapshot": digest(fresh.Snapshot())}
		fresh.Free()
		return result
	}
	t.Fatalf("unknown op %q", o.Op)
	return nil
}

// terminal-keys.test.ts: tmux key names (resolved by keys.ts to these presses) encode as a terminal sends them.
func TestTmuxKeys(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	vt := terminal(t, load(t), ghostty.Options{Cols: 20, Rows: 4, Scrollback: 10})
	encode := func(k ghostty.KeyInput) string {
		b, ok := vt.EncodeKey(k)
		if !ok {
			return "<none>"
		}
		return string(b)
	}
	key := func(code, key string, mods ...string) ghostty.KeyInput {
		k := ghostty.KeyInput{Code: code, Key: key}
		for _, m := range mods {
			switch m {
			case "shift":
				k.Shift = true
			case "ctrl":
				k.Ctrl = true
			case "alt":
				k.Alt = true
			}
		}
		return k
	}
	for _, c := range []struct {
		name string
		in   ghostty.KeyInput
		want string
	}{
		{"Enter", key("Enter", "Enter"), "\r"},
		{"Escape", key("Escape", "Escape"), "\x1b"},
		{"Tab", key("Tab", "Tab"), "\t"},
		{"BTab", key("Tab", "Tab", "shift"), "\x1b[Z"},
		{"BSpace", key("Backspace", "Backspace"), "\x7f"},
		{"Space", key("Space", " "), " "},
		{"C-c", key("KeyC", "c", "ctrl"), "\x03"},
		{"^D", key("KeyD", "d", "ctrl"), "\x04"},
		{"C-Space", key("Space", " ", "ctrl"), "\x00"},
		{"C-\\", key("Backslash", "\\", "ctrl"), "\x1c"},
		{"C-_", key("", "_", "ctrl"), "\x1f"},
		{"M-x", key("KeyX", "x", "alt"), "\x1bx"},
		{"M-X", key("KeyX", "X", "alt", "shift"), "\x1bX"},
		{"Up", key("ArrowUp", "ArrowUp"), "\x1b[A"},
		{"S-Up", key("ArrowUp", "ArrowUp", "shift"), "\x1b[1;2A"},
		{"Home", key("Home", "Home"), "\x1b[H"},
		{"PPage", key("PageUp", "PageUp"), "\x1b[5~"},
		{"DC", key("Delete", "Delete"), "\x1b[3~"},
		{"F1", key("F1", "F1"), "\x1bOP"},
		{"F5", key("F5", "F5"), "\x1b[15~"},
	} {
		if got := encode(c.in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	// Application cursor mode (a full-screen program asked for it) changes the arrows.
	vt.Write([]byte("\x1b[?1h"))
	if got := encode(key("ArrowUp", "ArrowUp")); got != "\x1bOA" {
		t.Errorf("Up in application cursor mode: %q", got)
	}
	// Keys that send nothing.
	if b, ok := vt.EncodeKey(ghostty.KeyInput{Code: "NoSuchKey", Key: "Dead"}); ok || b != nil {
		t.Errorf("an unknown key sent %q", b)
	}
}

// terminal-clipboard.test.ts: pasted text goes through Ghostty's encoder, plain or bracketed.
func TestPaste(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	vt := terminal(t, load(t), ghostty.Options{Cols: 80, Rows: 24, Scrollback: 100})
	if got := string(vt.EncodePaste("plain\nlines")); got != "plain\rlines" {
		t.Errorf("plain paste: %q", got)
	}
	vt.Write([]byte("\x1b[?2004h"))
	if got := string(vt.EncodePaste("first\nsecond\x1bthird")); got != "\x1b[200~first\nsecond third\x1b[201~" {
		t.Errorf("bracketed paste: %q", got)
	}
	if got := string(vt.EncodePaste("toolbar\npaste")); got != "\x1b[200~toolbar\npaste\x1b[201~" {
		t.Errorf("toolbar paste: %q", got)
	}
}

// terminals-host.test.ts: a snapshot written into a fresh terminal of the same size shows the screen.
func TestSnapshotReplaysTheScreen(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	g := load(t)
	vt := terminal(t, g, ghostty.Options{Cols: 40, Rows: 10, Scrollback: 100})
	vt.Write([]byte("$ printf 'in %s\\n' \"$PWD\"; echo $TERM\r\nin /tmp/p2-terminal-cwd-x\r\nxterm-256color\r\n$ "))
	snapshot := vt.Snapshot()
	fresh := terminal(t, g, ghostty.Options{Cols: 40, Rows: 10, Scrollback: 100})
	fresh.Write(snapshot)
	var lines []string
	for _, row := range fresh.Frame(true).Changed {
		var b strings.Builder
		for _, c := range row.Cells {
			if c.Text == "" {
				b.WriteByte(' ')
			} else {
				b.WriteString(c.Text)
			}
		}
		lines = append(lines, b.String())
	}
	screen := strings.Join(lines, "\n")
	for _, want := range []string{"\nin /tmp/p2-terminal-cwd-x", "\nxterm-256color"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the replayed screen lacks %q:\n%s", want, screen)
		}
	}
	if got := fresh.Text(); got != vt.Text() {
		t.Errorf("text %q differs from %q", got, vt.Text())
	}
}

func TestLoadRejectsOtherModules(t *testing.T) {
	// An empty module, and not a module at all.
	for name, b := range map[string][]byte{"empty module": {0, 'a', 's', 'm', 1, 0, 0, 0}, "garbage": []byte("not wasm")} {
		if g, err := ghostty.Load(context.Background(), b); err == nil {
			_ = g.Close(context.Background())
			t.Errorf("%s: loaded", name)
		}
	}
}

// Terminals of one module are independent, and every terminal can be freed in any order, twice.
func TestTerminalsAreIndependent(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	g := load(t)
	var a, b [][]byte
	one := terminal(t, g, ghostty.Options{Cols: 10, Rows: 3, Scrollback: 5, OnReply: func(r []byte) { a = append(a, r) }})
	two := terminal(t, g, ghostty.Options{Cols: 10, Rows: 3, Scrollback: 5, OnReply: func(r []byte) { b = append(b, r) }})
	one.Write([]byte("\x1b[6n"))
	two.Write([]byte("\x1b[2;2H\x1b[6n"))
	if len(a) != 1 || string(a[0]) != "\x1b[1;1R" || len(b) != 1 || string(b[0]) != "\x1b[2;2R" {
		t.Fatalf("replies: %q %q", a, b)
	}
	one.Free()
	one.Free()
	two.Write([]byte("x"))
	if got := two.Text(); got != "\nx" && !strings.Contains(got, "x") {
		t.Fatalf("text %q", got)
	}
}

// Many goroutines each with a module of their own share the process's compilation.
func TestConcurrentLoads(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	b := wasm(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := ghostty.Load(context.Background(), b)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = g.Close(context.Background()) }()
			vt, err := g.Terminal(ghostty.Options{Cols: 20, Rows: 5, Scrollback: 10})
			if err != nil {
				t.Error(err)
				return
			}
			defer vt.Free()
			vt.Write([]byte("hello"))
			if f := vt.Frame(true); f.Changed[0].Cells[0].Text != "h" {
				t.Errorf("frame: %+v", f.Changed[0].Cells[:2])
			}
		}()
	}
	wg.Wait()
}

// 1 MB of mixed output through Write and Frame, as the host feeds a busy terminal.
func stream() []byte {
	var b strings.Builder
	line := strings.Repeat("0123456789abcdefghij\x1b[31mred\x1b[0m ", 4) + "\r\n"
	for b.Len() < 1<<20 {
		b.WriteString(line)
	}
	return []byte(b.String())
}

func BenchmarkWriteFrame1MB(b *testing.B) {
	g := load(b)
	vt := terminal(b, g, ghostty.Options{Cols: 100, Rows: 30, Scrollback: 5000})
	data := stream()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		vt.Write(data)
		vt.Frame(false)
	}
}

func BenchmarkWrite1MB(b *testing.B) {
	g := load(b)
	vt := terminal(b, g, ghostty.Options{Cols: 100, Rows: 30, Scrollback: 5000})
	data := stream()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		vt.Write(data)
	}
}

func BenchmarkFrameAll(b *testing.B) {
	g := load(b)
	vt := terminal(b, g, ghostty.Options{Cols: 100, Rows: 30, Scrollback: 5000})
	vt.Write(stream())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		vt.Frame(true)
	}
}

// Load is cheap after the first: the compiled module is shared by the process.
func BenchmarkLoad(b *testing.B) {
	wasm := wasm(b)
	b.ReportAllocs()
	for range b.N {
		g, err := ghostty.Load(context.Background(), wasm)
		if err != nil {
			b.Fatal(err)
		}
		_ = g.Close(context.Background())
	}
}
