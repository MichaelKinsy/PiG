// Package ghostty binds libghostty-vt (the terminals plugin's vendor/ghostty/ghostty-vt.wasm) through wazero: the
// terminal emulator behind the host's headless copy of every terminal. It mirrors apps/p2/plugins/terminals/ghostty.ts
// of Pi: plain WebAssembly, pointers and size_t are 32 bits, struct layouts come from ghostty_type_json(), and
// structs passed by value travel as pointers (the wasm32 C ABI).
//
// A *Ghostty and the terminals made from it share one wasm instance and are not safe for concurrent use: callers use
// them from one goroutine at a time. The callbacks (Options.OnReply, Options.ColorScheme) run synchronously inside
// Write and must not call back into the terminal.
package ghostty

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	success    = 0
	outOfSpace = -3
)

// GhosttyTerminalOption
const (
	optWritePty        = 1
	optColorScheme     = 7
	optColorForeground = 11
	optColorBackground = 12
	optColorCursor     = 13
)

// GhosttyTerminalData
const (
	dataActiveScreen  = 6
	dataMouseTracking = 11
	dataScrollbar     = 9
	dataTitle         = 12
)

// GhosttyRenderStateData
const (
	renderDirty           = 3
	renderRowIterator     = 4
	renderCursorStyle     = 10
	renderCursorVisible   = 11
	renderCursorBlinking  = 12
	renderCursorInView    = 14
	renderCursorX         = 15
	renderCursorY         = 16
	renderCursorWideTail  = 17
	rowDirty              = 1
	rowRaw                = 2
	rowCells              = 3
	rowDataWrap           = 1
	cellRaw               = 1
	cellStyle             = 2
	cellGraphemesLen      = 3
	cellGraphemesBuf      = 4
	cellBg                = 5
	cellFg                = 6
	cellHasStyling        = 8
	formatPlain           = 0
	formatVT              = 1
	cellDataWide          = 3
	cellWide              = 1
	maxGrapheme           = 32
	scratchSize           = 1024
	writeChunk            = 64 << 10
	defaultCellWidthPx    = 8
	defaultCellHeightPx   = 16
	mouseOptEnableMotion  = 4
	mouseOptSize          = 2
	mouseOptAnyMotion     = 3
	mouseOutputCapacity   = 256
	keyOutputCapacity     = 256
	keyTextOffsetInScrach = 512
)

// Cell flags in Cell.Flags.
const (
	BOLD          = 1
	ITALIC        = 2
	FAINT         = 4
	UNDERLINE     = 8
	STRIKETHROUGH = 16
	INVERSE       = 32
	INVISIBLE     = 64
	BLINK         = 128
	OVERLINE      = 256
)

// Cell is one cell of a rendered row; colors are 0xRRGGBB, or -1 for the terminal's default.
type Cell struct {
	Text string
	// Width is the columns it covers: 2 for a wide character, 0 for the column after one (draw nothing).
	Width int
	Fg    int
	Bg    int
	Flags int
}

// Row is a changed row of a frame; Wrapped: soft-wrapped, its line continues on the next row.
type Row struct {
	Y       int
	Cells   []Cell
	Wrapped bool
}

// Cursor is the cursor, when it is visible in the viewport. Style is "bar", "block", "underline" or "hollow".
type Cursor struct {
	X, Y     int
	Style    string
	Blinking bool
	Color    *int
}

// Frame is what to draw since the last frame. Dirty is "none" (nothing changed), "partial" (only Changed rows
// changed) or "full" (Changed holds every row).
type Frame struct {
	Dirty      string
	Cols, Rows int
	Foreground int
	Background int
	Cursor     *Cursor
	Changed    []Row
}

// KeyInput is a key press: Code is KeyboardEvent.code (the physical key), Key is KeyboardEvent.key (the character
// for the layout, before Ctrl/Alt change it).
type KeyInput struct {
	Code, Key                      string
	Shift, Ctrl, Alt, Meta, Repeat bool
}

// Colors are 0xRRGGBB.
type Colors struct {
	Foreground, Background int
	Cursor                 *int
}

// MouseInput is a mouse event at a position in pixels of the terminal's content area: a button press or release
// (the wheel is buttons that are only pressed), or motion with Button held, or none. Action is "press", "release"
// or "motion"; Button is "left", "middle", "right", "wheelUp", "wheelDown" or "" for none. Width, Height,
// CellWidth and CellHeight are the content area and its cells, in pixels.
type MouseInput struct {
	Action                string
	Button                string
	X, Y                  float64
	Width, Height         float64
	CellWidth, CellHeight float64
	Shift, Ctrl, Alt      bool
}

// Options configure a terminal.
type Options struct {
	Cols, Rows int
	// Scrollback is the lines of scrollback kept.
	Scrollback int
	Colors     *Colors
	// OnReply receives the answers the terminal sends to the program (device attributes, cursor position reports,
	// ...); the slice is the callee's.
	OnReply func([]byte)
	// ColorScheme says whether the terminal is "light" or "dark", for programs that ask (CSI ? 996 n); "" leaves
	// the query unanswered.
	ColorScheme func() string
}

// The exports of the module that are called.
const (
	fnTypeJSON = iota
	fnAlloc
	fnFreeBytes
	fnTerminalSet
	fnTerminalGet
	fnTerminalNew
	fnTerminalFree
	fnTerminalWrite
	fnTerminalResize
	fnTerminalModeGet
	fnTerminalScroll
	fnMouseEncoderNew
	fnMouseEncoderFree
	fnMouseEncoderSetopt
	fnMouseEncoderFromTerminal
	fnMouseEncoderEncode
	fnMouseEventNew
	fnMouseEventFree
	fnMouseEventSetAction
	fnMouseEventClearButton
	fnMouseEventSetButton
	fnMouseEventSetMods
	fnMouseEventSetPosition
	fnFormatterNew
	fnFormatterFormat
	fnFormatterFree
	fnFree
	fnRenderNew
	fnRenderFree
	fnRenderUpdate
	fnRenderGet
	fnRenderSet
	fnRenderColors
	fnRowIterNew
	fnRowIterFree
	fnRowIterNext
	fnRowGet
	fnRowSet
	fnRowCellsNew
	fnRowCellsFree
	fnRowCellsNext
	fnRowCellsGet
	fnRowGetRaw
	fnCellGet
	fnKeyEncoderNew
	fnKeyEncoderFree
	fnKeyEncoderFromTerminal
	fnKeyEncoderEncode
	fnKeyEventNew
	fnKeyEventFree
	fnKeyEventSetAction
	fnKeyEventSetKey
	fnKeyEventSetMods
	fnKeyEventSetUnshifted
	fnKeyEventSetUTF8
	fnPasteEncode
	fnCount
)

var fnNames = [fnCount]string{
	fnTypeJSON:                 "ghostty_type_json",
	fnAlloc:                    "ghostty_wasm_alloc_u8_array",
	fnFreeBytes:                "ghostty_wasm_free_u8_array",
	fnTerminalSet:              "ghostty_terminal_set",
	fnTerminalGet:              "ghostty_terminal_get",
	fnTerminalNew:              "ghostty_terminal_new",
	fnTerminalFree:             "ghostty_terminal_free",
	fnTerminalWrite:            "ghostty_terminal_vt_write",
	fnTerminalResize:           "ghostty_terminal_resize",
	fnTerminalModeGet:          "ghostty_terminal_mode_get",
	fnTerminalScroll:           "ghostty_terminal_scroll_viewport",
	fnMouseEncoderNew:          "ghostty_mouse_encoder_new",
	fnMouseEncoderFree:         "ghostty_mouse_encoder_free",
	fnMouseEncoderSetopt:       "ghostty_mouse_encoder_setopt",
	fnMouseEncoderFromTerminal: "ghostty_mouse_encoder_setopt_from_terminal",
	fnMouseEncoderEncode:       "ghostty_mouse_encoder_encode",
	fnMouseEventNew:            "ghostty_mouse_event_new",
	fnMouseEventFree:           "ghostty_mouse_event_free",
	fnMouseEventSetAction:      "ghostty_mouse_event_set_action",
	fnMouseEventClearButton:    "ghostty_mouse_event_clear_button",
	fnMouseEventSetButton:      "ghostty_mouse_event_set_button",
	fnMouseEventSetMods:        "ghostty_mouse_event_set_mods",
	fnMouseEventSetPosition:    "ghostty_mouse_event_set_position",
	fnFormatterNew:             "ghostty_formatter_terminal_new",
	fnFormatterFormat:          "ghostty_formatter_format_alloc",
	fnFormatterFree:            "ghostty_formatter_free",
	fnFree:                     "ghostty_free",
	fnRenderNew:                "ghostty_render_state_new",
	fnRenderFree:               "ghostty_render_state_free",
	fnRenderUpdate:             "ghostty_render_state_update",
	fnRenderGet:                "ghostty_render_state_get",
	fnRenderSet:                "ghostty_render_state_set",
	fnRenderColors:             "ghostty_render_state_colors_get",
	fnRowIterNew:               "ghostty_render_state_row_iterator_new",
	fnRowIterFree:              "ghostty_render_state_row_iterator_free",
	fnRowIterNext:              "ghostty_render_state_row_iterator_next",
	fnRowGet:                   "ghostty_render_state_row_get",
	fnRowSet:                   "ghostty_render_state_row_set",
	fnRowCellsNew:              "ghostty_render_state_row_cells_new",
	fnRowCellsFree:             "ghostty_render_state_row_cells_free",
	fnRowCellsNext:             "ghostty_render_state_row_cells_next",
	fnRowCellsGet:              "ghostty_render_state_row_cells_get",
	fnRowGetRaw:                "ghostty_row_get",
	fnCellGet:                  "ghostty_cell_get",
	fnKeyEncoderNew:            "ghostty_key_encoder_new",
	fnKeyEncoderFree:           "ghostty_key_encoder_free",
	fnKeyEncoderFromTerminal:   "ghostty_key_encoder_setopt_from_terminal",
	fnKeyEncoderEncode:         "ghostty_key_encoder_encode",
	fnKeyEventNew:              "ghostty_key_event_new",
	fnKeyEventFree:             "ghostty_key_event_free",
	fnKeyEventSetAction:        "ghostty_key_event_set_action",
	fnKeyEventSetKey:           "ghostty_key_event_set_key",
	fnKeyEventSetMods:          "ghostty_key_event_set_mods",
	fnKeyEventSetUnshifted:     "ghostty_key_event_set_unshifted_codepoint",
	fnKeyEventSetUTF8:          "ghostty_key_event_set_utf8",
	fnPasteEncode:              "ghostty_paste_encode",
}

type layoutType struct {
	Size   int `json:"size"`
	Fields map[string]struct {
		Offset int `json:"offset"`
		Size   int `json:"size"`
	} `json:"fields"`
}

// offsets are the struct layouts the bindings touch, resolved once at Load.
type offsets struct {
	terminalOptionsSize, termCols, termRows, termScrollback                           int
	mouseSizeSize, mouseSizeSize_, mouseScreenW, mouseScreenH, mouseCellW, mouseCellH int
	mousePosX, mousePosY                                                              int
	stringPtr, stringLen                                                              int
	scrollTotal, scrollOffset, scrollLen                                              int
	scrollViewportSize, scrollTag, scrollValue                                        int
	fmtSize, fmtOptSize, fmtEmit, fmtUnwrap, fmtTrim, fmtExtra                        int
	extraSize, extraTotal, extraScreen                                                int
	extraFlags                                                                        [4]int
	screenSize, screenTotal                                                           int
	screenFlags                                                                       [6]int
	colorsSize, colorsTotal, colorsBackground, colorsForeground, colorsCursor         int
	colorsCursorHas                                                                   int
	styleSize, styleSizeField, styleUnderline                                         int
	styleFlags                                                                        [8]styleFlag
}

type styleFlag struct{ offset, bit int }

// Ghostty is a loaded libghostty-vt module; terminals made from it share its memory.
type Ghostty struct {
	rt        wazero.Runtime
	mem       api.Memory
	fns       [fnCount]api.Function
	off       offsets
	terminals map[uint32]*VtTerminal
	writePty  uint32
	scheme    uint32
	stack     [12]uint64
	ctx       context.Context
}

// cache holds the compiled module for the whole process: every Load of the same bytes shares one compilation.
var (
	cacheOnce sync.Once
	cache     wazero.CompilationCache
)

func compilationCache() wazero.CompilationCache {
	cacheOnce.Do(func() { cache = wazero.NewCompilationCache() })
	return cache
}

// Load instantiates the module. The compiled code is cached for the process, so loading again is cheap.
func Load(ctx context.Context, wasm []byte) (*Ghostty, error) {
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(compilationCache()))
	g := &Ghostty{rt: rt, terminals: map[uint32]*VtTerminal{}, ctx: context.Background()}
	if err := g.load(ctx, wasm); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return g, nil
}

// Close releases the instance; its terminals are unusable afterwards.
func (g *Ghostty) Close(ctx context.Context) error {
	clear(g.terminals)
	return g.rt.Close(ctx)
}

func (g *Ghostty) load(ctx context.Context, wasm []byte) error {
	const i32 = api.ValueTypeI32
	if _, err := g.rt.NewHostModuleBuilder("env").NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(func(_ context.Context, m api.Module, stack []uint64) {
			if b, ok := m.Memory().Read(uint32(stack[0]), uint32(stack[1])); ok {
				slog.Debug("[ghostty] " + string(b))
			}
		}), []api.ValueType{i32, i32}, nil).Export("log").Instantiate(ctx); err != nil {
		return fmt.Errorf("ghostty: host log: %w", err)
	}
	if _, err := g.rt.NewHostModuleBuilder("host").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(g.onWritePty),
		[]api.ValueType{i32, i32, i32, i32}, nil).Export("wp").
		NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(g.onColorScheme),
		[]api.ValueType{i32, i32, i32}, []api.ValueType{i32}).Export("cs").Instantiate(ctx); err != nil {
		return fmt.Errorf("ghostty: host callbacks: %w", err)
	}
	compiled, err := g.rt.CompileModule(ctx, wasm)
	if err != nil {
		return fmt.Errorf("ghostty: compile: %w", err)
	}
	mod, err := g.rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName("ghostty").WithStartFunctions())
	if err != nil {
		return fmt.Errorf("ghostty: instantiate: %w", err)
	}
	g.mem = mod.Memory()
	if g.mem == nil {
		return errors.New("ghostty: module exports no memory")
	}
	for i, name := range fnNames {
		fn := mod.ExportedFunction(name)
		if fn == nil {
			if i == fnTypeJSON {
				return errors.New("Not libghostty-vt: no ghostty_type_json")
			}
			return fmt.Errorf("libghostty-vt has no %s", name)
		}
		g.fns[i] = fn
	}
	// Callbacks go through the function table: a glue module puts the host functions into it.
	glueCompiled, err := g.rt.CompileModule(ctx, glueModule("ghostty"))
	if err != nil {
		return fmt.Errorf("ghostty: compile callbacks: %w", err)
	}
	glue, err := g.rt.InstantiateModule(ctx, glueCompiled, wazero.NewModuleConfig().WithName("glue").WithStartFunctions())
	if err != nil {
		return fmt.Errorf("ghostty: instantiate callbacks: %w", err)
	}
	res, err := glue.ExportedFunction("install").Call(ctx)
	if err != nil {
		return fmt.Errorf("ghostty: install callbacks: %w", err)
	}
	if int32(res[0]) < 0 {
		return errors.New("ghostty: the function table cannot grow")
	}
	g.writePty = uint32(res[0])
	g.scheme = g.writePty + 1
	return g.resolveLayout()
}

// GhosttyTerminalWritePtyFn(terminal, userdata, data, len)
func (g *Ghostty) onWritePty(_ context.Context, _ api.Module, stack []uint64) {
	terminal := g.terminals[uint32(stack[0])]
	if terminal == nil || terminal.onReply == nil {
		return
	}
	data := g.bytes(uint32(stack[2]), uint32(stack[3]))
	terminal.onReply(append([]byte(nil), data...))
}

// GhosttyTerminalColorSchemeFn(terminal, userdata, out) -> bool; GhosttyColorScheme: 0 light, 1 dark.
func (g *Ghostty) onColorScheme(_ context.Context, _ api.Module, stack []uint64) {
	terminal := g.terminals[uint32(stack[0])]
	stack[0] = 0
	if terminal == nil || terminal.colorScheme == nil {
		return
	}
	scheme := terminal.colorScheme()
	if scheme == "" {
		return
	}
	var v uint32
	if scheme == "dark" {
		v = 1
	}
	g.putU32(uint32(stack[2]), v)
	stack[0] = 1
}

func (g *Ghostty) resolveLayout() error {
	ptr := g.call(fnTypeJSON)
	mem, _ := g.mem.Read(0, g.mem.Size())
	end := ptr
	for int(end) < len(mem) && mem[end] != 0 {
		end++
	}
	var layout map[string]layoutType
	if err := json.Unmarshal(mem[ptr:end], &layout); err != nil {
		return fmt.Errorf("ghostty: layout: %w", err)
	}
	var err error
	field := func(typ, name string) int {
		f, ok := layout[typ].Fields[name]
		if !ok && err == nil {
			err = fmt.Errorf("libghostty-vt has no %s.%s", typ, name)
		}
		return f.Offset
	}
	size := func(typ string) int {
		l, ok := layout[typ]
		if !ok && err == nil {
			err = fmt.Errorf("libghostty-vt has no %s", typ)
		}
		return l.Size
	}
	o := &g.off
	const (
		tOpts   = "GhosttyTerminalOptions"
		tMouse  = "GhosttyMouseEncoderSize"
		tFmt    = "GhosttyFormatterTerminalOptions"
		tExtra  = "GhosttyFormatterTerminalExtra"
		tScreen = "GhosttyFormatterScreenExtra"
		tColors = "GhosttyRenderStateColors"
		tStyle  = "GhosttyStyle"
		tScroll = "GhosttyTerminalScrollViewport"
	)
	o.terminalOptionsSize = size(tOpts)
	o.termCols, o.termRows, o.termScrollback = field(tOpts, "cols"), field(tOpts, "rows"), field(tOpts, "max_scrollback")
	o.mouseSizeSize = size(tMouse)
	o.mouseSizeSize_, o.mouseScreenW, o.mouseScreenH = field(tMouse, "size"), field(tMouse, "screen_width"), field(tMouse, "screen_height")
	o.mouseCellW, o.mouseCellH = field(tMouse, "cell_width"), field(tMouse, "cell_height")
	o.mousePosX, o.mousePosY = field("GhosttyMousePosition", "x"), field("GhosttyMousePosition", "y")
	o.stringPtr, o.stringLen = field("GhosttyString", "ptr"), field("GhosttyString", "len")
	o.scrollTotal, o.scrollOffset, o.scrollLen = field("GhosttyTerminalScrollbar", "total"), field("GhosttyTerminalScrollbar", "offset"), field("GhosttyTerminalScrollbar", "len")
	o.scrollViewportSize = size(tScroll)
	o.scrollTag, o.scrollValue = field(tScroll, "tag"), field(tScroll, "value")
	o.fmtOptSize = size(tFmt)
	o.fmtSize, o.fmtEmit, o.fmtUnwrap, o.fmtTrim, o.fmtExtra = field(tFmt, "size"), field(tFmt, "emit"), field(tFmt, "unwrap"), field(tFmt, "trim"), field(tFmt, "extra")
	o.extraTotal = size(tExtra)
	o.extraSize, o.extraScreen = field(tExtra, "size"), field(tExtra, "screen")
	for i, name := range []string{"modes", "scrolling_region", "pwd", "keyboard"} {
		o.extraFlags[i] = field(tExtra, name)
	}
	o.screenTotal = size(tScreen)
	o.screenSize = field(tScreen, "size")
	for i, name := range []string{"cursor", "style", "hyperlink", "protection", "kitty_keyboard", "charsets"} {
		o.screenFlags[i] = field(tScreen, name)
	}
	o.colorsTotal = size(tColors)
	o.colorsSize, o.colorsBackground, o.colorsForeground = field(tColors, "size"), field(tColors, "background"), field(tColors, "foreground")
	o.colorsCursor, o.colorsCursorHas = field(tColors, "cursor"), field(tColors, "cursor_has_value")
	o.styleSize = size(tStyle)
	o.styleUnderline = field(tStyle, "underline")
	for i, f := range []struct {
		name string
		bit  int
	}{{"bold", BOLD}, {"italic", ITALIC}, {"faint", FAINT}, {"blink", BLINK}, {"inverse", INVERSE}, {"invisible", INVISIBLE}, {"strikethrough", STRIKETHROUGH}, {"overline", OVERLINE}} {
		o.styleFlags[i] = styleFlag{field(tStyle, f.name), f.bit}
	}
	o.styleSizeField = field(tStyle, "size")
	return err
}

// call invokes an export; the result is its i32 result (0 for none).
func (g *Ghostty) call(fn int, args ...uint64) int32 {
	copy(g.stack[:], args)
	if err := g.fns[fn].CallWithStack(g.ctx, g.stack[:max(len(args), 1)]); err != nil {
		panic(fmt.Errorf("libghostty-vt %s: %w", fnNames[fn], err))
	}
	return int32(uint32(g.stack[0]))
}

func (g *Ghostty) bytes(ptr, n uint32) []byte {
	b, ok := g.mem.Read(ptr, n)
	if !ok {
		panic(fmt.Errorf("libghostty-vt memory access out of range: %d+%d", ptr, n))
	}
	return b
}

func (g *Ghostty) u8(p int) uint8       { return g.bytes(uint32(p), 1)[0] }
func (g *Ghostty) u16(p int) uint16     { return binary.LittleEndian.Uint16(g.bytes(uint32(p), 2)) }
func (g *Ghostty) u32(p int) uint32     { return binary.LittleEndian.Uint32(g.bytes(uint32(p), 4)) }
func (g *Ghostty) u64(p int) uint64     { return binary.LittleEndian.Uint64(g.bytes(uint32(p), 8)) }
func (g *Ghostty) putU8(p int, v uint8) { g.bytes(uint32(p), 1)[0] = v }
func (g *Ghostty) putU16(p int, v int) {
	binary.LittleEndian.PutUint16(g.bytes(uint32(p), 2), uint16(v))
}
func (g *Ghostty) putU32(p uint32, v uint32) {
	binary.LittleEndian.PutUint32(g.bytes(p, 4), v)
}
func (g *Ghostty) setU32(p int, v uint32) { g.putU32(uint32(p), v) }

// rgb reads three bytes at p as 0xRRGGBB.
func (g *Ghostty) rgb(p int) int {
	b := g.bytes(uint32(p), 3)
	return int(b[0])<<16 | int(b[1])<<8 | int(b[2])
}

// alloc allocates length zeroed bytes.
func (g *Ghostty) alloc(length int) int {
	ptr := g.call(fnAlloc, uint64(length))
	if ptr == 0 {
		panic(errors.New("libghostty-vt is out of memory"))
	}
	clear(g.bytes(uint32(ptr), uint32(length)))
	return int(ptr)
}

func (g *Ghostty) free(ptr, length int) { g.call(fnFreeBytes, uint64(ptr), uint64(length)) }

// create makes an object through new(allocator, out, ...args) and returns its handle.
func (g *Ghostty) create(fn int, args ...uint64) (uint32, error) {
	out := g.alloc(4)
	defer g.free(out, 4)
	var a [6]uint64
	a[1] = uint64(out)
	n := copy(a[2:], args)
	if result := g.call(fn, a[:n+2]...); result != success {
		return 0, fmt.Errorf("%s failed (%d)", fnNames[fn], result)
	}
	return g.u32(out), nil
}

// Terminal makes a terminal emulator.
func (g *Ghostty) Terminal(options Options) (t *VtTerminal, err error) {
	t = &VtTerminal{g: g, onReply: options.OnReply, colorScheme: options.ColorScheme, cols: options.Cols, rows: options.Rows}
	o := &g.off
	size := o.terminalOptionsSize
	ptr := g.alloc(size)
	g.putU16(ptr+o.termCols, options.Cols)
	g.putU16(ptr+o.termRows, options.Rows)
	g.setU32(ptr+o.termScrollback, uint32(options.Scrollback))
	t.handle, err = g.create(fnTerminalNew, uint64(ptr))
	g.free(ptr, size)
	if err != nil {
		return nil, err
	}
	t.scratch = g.alloc(scratchSize)
	t.graphemes = g.alloc(maxGrapheme * 4)
	if options.Colors != nil {
		t.SetColors(*options.Colors)
	}
	g.terminals[t.handle] = t
	if options.OnReply != nil {
		g.call(fnTerminalSet, uint64(t.handle), optWritePty, uint64(g.writePty))
	}
	if options.ColorScheme != nil {
		g.call(fnTerminalSet, uint64(t.handle), optColorScheme, uint64(g.scheme))
	}
	return t, nil
}

// VtTerminal is one terminal emulator: VT state, screen and scrollback, rendering and input encoding.
type VtTerminal struct {
	g           *Ghostty
	handle      uint32
	onReply     func([]byte)
	colorScheme func() string
	scratch     int
	graphemes   int
	wbuf, wcap  int
	render      uint32
	rowIter     uint32
	cells       uint32
	encoder     uint32
	event       uint32
	mouseEnc    uint32
	mouseEvent  uint32
	freed       bool
	cols, rows  int
}

// Write processes output of the program.
func (t *VtTerminal) Write(b []byte) {
	g := t.g
	for len(b) > 0 {
		n := min(len(b), writeChunk)
		if n > t.wcap {
			if t.wbuf != 0 {
				g.free(t.wbuf, t.wcap)
			}
			t.wcap = min(max(n, 4096), writeChunk)
			t.wbuf = g.alloc(t.wcap)
		}
		copy(g.bytes(uint32(t.wbuf), uint32(n)), b[:n])
		g.call(fnTerminalWrite, uint64(t.handle), uint64(t.wbuf), uint64(n))
		b = b[n:]
	}
}

// Resize changes the grid; the cell size the program is told is 8×16 pixels.
func (t *VtTerminal) Resize(cols, rows int) {
	t.cols, t.rows = cols, rows
	t.g.call(fnTerminalResize, uint64(t.handle), uint64(cols), uint64(rows), defaultCellWidthPx, defaultCellHeightPx)
}

// SetColors sets the default colors; a nil Cursor clears the cursor color.
func (t *VtTerminal) SetColors(c Colors) {
	g := t.g
	set := func(option int, color *int) {
		if color == nil {
			g.call(fnTerminalSet, uint64(t.handle), uint64(option), 0)
			return
		}
		b := g.bytes(uint32(t.scratch), 3)
		b[0], b[1], b[2] = byte(*color>>16), byte(*color>>8), byte(*color)
		g.call(fnTerminalSet, uint64(t.handle), uint64(option), uint64(t.scratch))
	}
	set(optColorForeground, &c.Foreground)
	set(optColorBackground, &c.Background)
	set(optColorCursor, c.Cursor)
}

// Mode reports whether a mode is set; DEC private modes unless ansi.
func (t *VtTerminal) Mode(value int, ansi bool) bool {
	mode := value & 0x7fff
	if ansi {
		mode |= 0x8000
	}
	t.g.putU8(t.scratch, 0)
	t.g.call(fnTerminalModeGet, uint64(t.handle), uint64(mode), uint64(t.scratch))
	return t.g.u8(t.scratch) != 0
}

// AlternateScreen reports whether the alternate screen (full-screen programs) is active.
func (t *VtTerminal) AlternateScreen() bool {
	t.g.call(fnTerminalGet, uint64(t.handle), dataActiveScreen, uint64(t.scratch))
	return t.g.u32(t.scratch) == 1
}

// MouseTracking reports whether the program asked for mouse events (any tracking mode).
func (t *VtTerminal) MouseTracking() bool {
	t.g.putU8(t.scratch, 0)
	t.g.call(fnTerminalGet, uint64(t.handle), dataMouseTracking, uint64(t.scratch))
	return t.g.u8(t.scratch) != 0
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// round is JavaScript's Math.round clamped below at zero, as an unsigned 32-bit value.
func pixels(v float64) uint32 {
	r := math.Floor(v + 0.5)
	if !(r > 0) {
		return 0
	}
	if r > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(r)
}

// EncodeMouse returns the bytes a mouse event sends in the program's mouse mode and format; false when the
// program does not want it (no tracking, or motion it did not ask for).
func (t *VtTerminal) EncodeMouse(in MouseInput) ([]byte, bool) {
	g := t.g
	o := &g.off
	if t.mouseEnc == 0 {
		var err error
		if t.mouseEnc, err = g.create(fnMouseEncoderNew); err != nil {
			panic(err)
		}
		if t.mouseEvent, err = g.create(fnMouseEventNew); err != nil {
			panic(err)
		}
		// Motion within the cell of the last report is not reported again.
		g.putU8(t.scratch, 1)
		g.call(fnMouseEncoderSetopt, uint64(t.mouseEnc), mouseOptEnableMotion, uint64(t.scratch))
	}
	g.call(fnMouseEncoderFromTerminal, uint64(t.mouseEnc), uint64(t.handle))
	scratch := t.scratch
	clear(g.bytes(uint32(scratch), uint32(o.mouseSizeSize)))
	g.setU32(scratch+o.mouseSizeSize_, uint32(o.mouseSizeSize))
	g.setU32(scratch+o.mouseScreenW, pixels(in.Width))
	g.setU32(scratch+o.mouseScreenH, pixels(in.Height))
	g.setU32(scratch+o.mouseCellW, pixels(in.CellWidth))
	g.setU32(scratch+o.mouseCellH, pixels(in.CellHeight))
	g.call(fnMouseEncoderSetopt, uint64(t.mouseEnc), mouseOptSize, uint64(scratch))
	// Motion with a button held is reported in button-event mode (1002) too.
	g.putU8(scratch, uint8(b2u(in.Action == "motion" && in.Button != "")))
	g.call(fnMouseEncoderSetopt, uint64(t.mouseEnc), mouseOptAnyMotion, uint64(scratch))
	// GhosttyMouseAction: press, release, motion. GhosttyMouseButton: left 1, right 2, middle 3, wheel 4 / 5.
	action := uint64(2)
	switch in.Action {
	case "press":
		action = 0
	case "release":
		action = 1
	}
	g.call(fnMouseEventSetAction, uint64(t.mouseEvent), action)
	button := 0
	switch in.Button {
	case "left":
		button = 1
	case "right":
		button = 2
	case "middle":
		button = 3
	case "wheelUp":
		button = 4
	case "wheelDown":
		button = 5
	}
	if button == 0 {
		g.call(fnMouseEventClearButton, uint64(t.mouseEvent))
	} else {
		g.call(fnMouseEventSetButton, uint64(t.mouseEvent), uint64(button))
	}
	g.call(fnMouseEventSetMods, uint64(t.mouseEvent), b2u(in.Shift)|b2u(in.Ctrl)<<1|b2u(in.Alt)<<2)
	binary.LittleEndian.PutUint32(g.bytes(uint32(scratch+o.mousePosX), 4), math.Float32bits(float32(in.X)))
	binary.LittleEndian.PutUint32(g.bytes(uint32(scratch+o.mousePosY), 4), math.Float32bits(float32(in.Y)))
	g.call(fnMouseEventSetPosition, uint64(t.mouseEvent), uint64(scratch))
	out := scratch + 16
	if g.call(fnMouseEncoderEncode, uint64(t.mouseEnc), uint64(t.mouseEvent), uint64(out), mouseOutputCapacity, uint64(scratch)) != success {
		return nil, false
	}
	n := g.u32(scratch)
	if n == 0 {
		return nil, false
	}
	return append([]byte(nil), g.bytes(uint32(out), n)...), true
}

// Title is the title programs set with OSC 0/2.
func (t *VtTerminal) Title() string {
	g := t.g
	if g.call(fnTerminalGet, uint64(t.handle), dataTitle, uint64(t.scratch)) != success {
		return ""
	}
	ptr, n := g.u32(t.scratch+g.off.stringPtr), g.u32(t.scratch+g.off.stringLen)
	if n == 0 {
		return ""
	}
	return strings.ToValidUTF8(string(g.bytes(ptr, n)), "\uFFFD")
}

// Scrollbar is the scrollback position: rows in total, the first row shown, and rows shown.
func (t *VtTerminal) Scrollbar() (total, offset, length int) {
	g := t.g
	g.call(fnTerminalGet, uint64(t.handle), dataScrollbar, uint64(t.scratch))
	return int(g.u64(t.scratch + g.off.scrollTotal)), int(g.u64(t.scratch + g.off.scrollOffset)), int(g.u64(t.scratch + g.off.scrollLen))
}

func (t *VtTerminal) scroll(tag uint32, delta int32) {
	g := t.g
	clear(g.bytes(uint32(t.scratch), uint32(g.off.scrollViewportSize)))
	g.setU32(t.scratch+g.off.scrollTag, tag)
	if tag == 2 {
		g.setU32(t.scratch+g.off.scrollValue, uint32(delta))
	}
	g.call(fnTerminalScroll, uint64(t.handle), uint64(t.scratch))
}

// Scroll moves the viewport by delta rows (up is negative).
func (t *VtTerminal) Scroll(delta int) { t.scroll(2, int32(delta)) }

// ScrollBottom moves the viewport to the bottom.
func (t *VtTerminal) ScrollBottom() { t.scroll(1, 0) }

// Snapshot is the screen with its scrollback, styles and modes as VT sequences: writing it into a fresh terminal of
// the same size reproduces this one, for a browser that attaches later.
func (t *VtTerminal) Snapshot() []byte { return t.format(formatVT) }

// Text is the active screen as plain text, with the scrollback before it (the alternate screen of a full-screen
// program has none): soft-wrapped lines joined, trailing spaces trimmed.
func (t *VtTerminal) Text() string {
	return strings.ToValidUTF8(string(t.format(formatPlain)), "\uFFFD")
}

func (t *VtTerminal) format(emit uint32) []byte {
	g := t.g
	o := &g.off
	size := o.fmtOptSize
	options := g.alloc(size)
	var formatter uint32
	defer func() {
		if formatter != 0 {
			g.call(fnFormatterFree, uint64(formatter))
		}
		g.free(options, size)
	}()
	extra := options + o.fmtExtra
	screen := extra + o.extraScreen
	g.setU32(options+o.fmtSize, uint32(size))
	g.setU32(options+o.fmtEmit, emit)
	g.putU8(options+o.fmtUnwrap, 1)
	g.putU8(options+o.fmtTrim, 1)
	g.setU32(extra+o.extraSize, uint32(o.extraTotal))
	g.setU32(screen+o.screenSize, uint32(o.screenTotal))
	if emit == formatVT {
		// Not `tabstops`: the formatter emits them after the cursor position, moving the cursor (programs rarely
		// change the default stops anyway).
		for _, f := range o.extraFlags {
			g.putU8(extra+f, 1)
		}
		for _, f := range o.screenFlags {
			g.putU8(screen+f, 1)
		}
	}
	var err error
	formatter, err = g.create(fnFormatterNew, uint64(t.handle), uint64(options))
	if err != nil {
		panic(err)
	}
	if result := g.call(fnFormatterFormat, uint64(formatter), 0, uint64(t.scratch), uint64(t.scratch+4)); result != success {
		panic(fmt.Errorf("Formatting the terminal failed (%d)", result))
	}
	ptr, n := g.u32(t.scratch), g.u32(t.scratch+4)
	out := append([]byte(nil), g.bytes(ptr, n)...)
	g.call(fnFree, 0, uint64(ptr), uint64(n))
	return out
}

// ascii holds the one-byte strings, so a cell of plain text does not allocate.
var ascii = func() (a [128]string) {
	for i := range a {
		a[i] = string(rune(i))
	}
	return
}()

// Frame is what to draw since the last frame; each frame clears the dirty state. all returns every row.
func (t *VtTerminal) Frame(all bool) Frame {
	g := t.g
	o := &g.off
	if t.render == 0 {
		var err error
		if t.render, err = g.create(fnRenderNew); err != nil {
			panic(err)
		}
		if t.rowIter, err = g.create(fnRowIterNew); err != nil {
			panic(err)
		}
		if t.cells, err = g.create(fnRowCellsNew); err != nil {
			panic(err)
		}
	}
	state := uint64(t.render)
	scratch := t.scratch
	g.call(fnRenderUpdate, state, uint64(t.handle))
	get := func(data int) bool {
		g.setU32(scratch, 0)
		return g.call(fnRenderGet, state, uint64(data), uint64(scratch)) == success
	}
	dirtyValue := uint32(2)
	if get(renderDirty) {
		dirtyValue = g.u32(scratch)
	}
	dirty := "full"
	switch dirtyValue {
	case 0:
		dirty = "none"
	case 1:
		dirty = "partial"
	}
	clear(g.bytes(uint32(scratch), uint32(o.colorsTotal)))
	g.setU32(scratch+o.colorsSize, uint32(o.colorsTotal))
	g.call(fnRenderColors, state, uint64(scratch))
	background := g.rgb(scratch + o.colorsBackground)
	foreground := g.rgb(scratch + o.colorsForeground)
	var cursorColor *int
	if g.u8(scratch+o.colorsCursorHas) != 0 {
		c := g.rgb(scratch + o.colorsCursor)
		cursorColor = &c
	}
	var cursor *Cursor
	if get(renderCursorVisible) && g.u8(scratch) != 0 && get(renderCursorInView) && g.u8(scratch) != 0 {
		var x, y int
		if get(renderCursorX) {
			x = int(g.u16(scratch))
		}
		if get(renderCursorY) {
			y = int(g.u16(scratch))
		}
		wideTail := get(renderCursorWideTail) && g.u8(scratch) != 0
		styleIndex := uint32(1)
		if get(renderCursorStyle) {
			styleIndex = g.u32(scratch)
		}
		style := "block"
		if styleIndex < 4 {
			style = [...]string{"bar", "block", "underline", "hollow"}[styleIndex]
		}
		if wideTail {
			x = max(0, x-1)
		}
		cursor = &Cursor{X: x, Y: y, Style: style, Blinking: get(renderCursorBlinking) && g.u8(scratch) != 0, Color: cursorColor}
	}
	var changed []Row
	if dirty != "none" || all {
		g.setU32(scratch, t.rowIter)
		g.call(fnRenderGet, state, renderRowIterator, uint64(scratch))
		for y := 0; g.call(fnRowIterNext, uint64(t.rowIter)) != 0; y++ {
			g.putU8(scratch, 0)
			g.call(fnRowGet, uint64(t.rowIter), rowDirty, uint64(scratch))
			rowIsDirty := g.u8(scratch) != 0
			if !(all || dirty == "full" || rowIsDirty) {
				continue
			}
			g.call(fnRowGet, uint64(t.rowIter), rowRaw, uint64(scratch))
			g.call(fnRowGetRaw, g.u64(scratch), rowDataWrap, uint64(scratch))
			wrapped := g.u8(scratch) != 0
			g.setU32(scratch, t.cells)
			g.call(fnRowGet, uint64(t.rowIter), rowCells, uint64(scratch))
			cells := make([]Cell, 0, t.cols)
			afterWide := false
			for g.call(fnRowCellsNext, uint64(t.cells)) != 0 {
				text := ""
				width := 1
				if afterWide {
					width = 0
				}
				afterWide = false
				g.call(fnRowCellsGet, uint64(t.cells), cellGraphemesLen, uint64(scratch))
				length := int(min(g.u32(scratch), maxGrapheme))
				if length > 0 {
					g.call(fnRowCellsGet, uint64(t.cells), cellGraphemesBuf, uint64(t.graphemes))
					codes := g.bytes(uint32(t.graphemes), uint32(length*4))
					if length == 1 && binary.LittleEndian.Uint32(codes) < 128 {
						text = ascii[binary.LittleEndian.Uint32(codes)]
					} else {
						buf := make([]byte, 0, length*4)
						for i := range length {
							buf = utf8.AppendRune(buf, rune(binary.LittleEndian.Uint32(codes[i*4:])))
						}
						text = string(buf)
					}
					g.call(fnRowCellsGet, uint64(t.cells), cellRaw, uint64(scratch))
					g.call(fnCellGet, g.u64(scratch), cellDataWide, uint64(scratch))
					if g.u32(scratch) == cellWide {
						width = 2
						afterWide = true
					}
				}
				flags, fg := 0, -1
				g.putU8(scratch, 0)
				g.call(fnRowCellsGet, uint64(t.cells), cellHasStyling, uint64(scratch))
				if g.u8(scratch) != 0 {
					clear(g.bytes(uint32(scratch), uint32(o.styleSize)))
					g.setU32(scratch+o.styleSizeField, uint32(o.styleSize))
					g.call(fnRowCellsGet, uint64(t.cells), cellStyle, uint64(scratch))
					for _, f := range o.styleFlags {
						if g.u8(scratch+f.offset) != 0 {
							flags |= f.bit
						}
					}
					if g.u32(scratch+o.styleUnderline) != 0 {
						flags |= UNDERLINE
					}
					if g.call(fnRowCellsGet, uint64(t.cells), cellFg, uint64(scratch)) == success {
						fg = g.rgb(scratch)
					}
				}
				bg := -1
				if g.call(fnRowCellsGet, uint64(t.cells), cellBg, uint64(scratch)) == success {
					bg = g.rgb(scratch)
				}
				cells = append(cells, Cell{Text: text, Width: width, Fg: fg, Bg: bg, Flags: flags})
			}
			changed = append(changed, Row{Y: y, Cells: cells, Wrapped: wrapped})
			g.putU8(scratch, 0)
			g.call(fnRowSet, uint64(t.rowIter), 0, uint64(scratch))
		}
		g.setU32(scratch, 0)
		g.call(fnRenderSet, state, 0, uint64(scratch))
	}
	return Frame{Dirty: dirty, Cols: t.cols, Rows: t.rows, Foreground: foreground, Background: background, Cursor: cursor, Changed: changed}
}

// EncodeKey returns the bytes a key press sends in the terminal's current modes; false when it sends nothing.
func (t *VtTerminal) EncodeKey(in KeyInput) ([]byte, bool) {
	g := t.g
	if t.encoder == 0 {
		var err error
		if t.encoder, err = g.create(fnKeyEncoderNew); err != nil {
			panic(err)
		}
		if t.event, err = g.create(fnKeyEventNew); err != nil {
			panic(err)
		}
	}
	key := keyCodes[in.Code]
	var character rune
	single := false
	if n := utf8.RuneCountInString(in.Key); n == 1 {
		character, _ = utf8.DecodeRuneInString(in.Key)
		single = true
	}
	printable := single && character >= 0x20 && character != 0x7f
	if key == 0 && !printable {
		return nil, false
	}
	g.call(fnKeyEncoderFromTerminal, uint64(t.encoder), uint64(t.handle))
	action := uint64(1)
	if in.Repeat {
		action = 2
	}
	g.call(fnKeyEventSetAction, uint64(t.event), action)
	g.call(fnKeyEventSetKey, uint64(t.event), uint64(key))
	g.call(fnKeyEventSetMods, uint64(t.event), b2u(in.Shift)|b2u(in.Ctrl)<<1|b2u(in.Alt)<<2|b2u(in.Meta)<<3)
	var unshifted string
	switch {
	case strings.HasPrefix(in.Code, "Key"):
		unshifted = strings.ToLower(in.Code[3:])
	case strings.HasPrefix(in.Code, "Digit"):
		unshifted = in.Code[5:]
	case printable:
		unshifted = strings.ToLower(string(character))
	}
	var cp rune
	if unshifted != "" {
		cp, _ = utf8.DecodeRuneInString(unshifted)
	}
	g.call(fnKeyEventSetUnshifted, uint64(t.event), uint64(cp))
	// The text lives in scratch space past the encoder's output.
	text := t.scratch + keyTextOffsetInScrach
	var n int
	if printable {
		n = utf8.EncodeRune(g.bytes(uint32(text), utf8.UTFMax), character)
	}
	if n > 0 {
		g.call(fnKeyEventSetUTF8, uint64(t.event), uint64(text), uint64(n))
	} else {
		g.call(fnKeyEventSetUTF8, uint64(t.event), 0, 0)
	}
	out := t.scratch + 8
	result := g.call(fnKeyEncoderEncode, uint64(t.encoder), uint64(t.event), uint64(out), keyOutputCapacity, uint64(t.scratch))
	g.call(fnKeyEventSetUTF8, uint64(t.event), 0, 0)
	if result != success {
		return nil, false
	}
	length := g.u32(t.scratch)
	if length == 0 {
		return nil, false
	}
	return append([]byte(nil), g.bytes(uint32(out), length)...), true
}

// EncodePaste is pasted text as the program should receive it: control bytes made safe, bracketed when the
// program asked.
func (t *VtTerminal) EncodePaste(text string) []byte {
	g := t.g
	n := max(1, len(text))
	input := g.alloc(n)
	defer func() { g.free(input, n) }()
	capacity := len(text) + 16
	copy(g.bytes(uint32(input), uint32(len(text))), text)
	bracketed := b2u(t.Mode(2004, false))
	for {
		size := capacity
		output := g.alloc(size)
		result := g.call(fnPasteEncode, uint64(input), uint64(len(text)), bracketed, uint64(output), uint64(size), uint64(t.scratch))
		written := int(g.u32(t.scratch))
		if result == outOfSpace && written > size {
			g.free(output, size)
			capacity = written
			continue
		}
		if result != success {
			g.free(output, size)
			panic(fmt.Errorf("Encoding the paste failed (%d)", result))
		}
		out := append([]byte(nil), g.bytes(uint32(output), uint32(written))...)
		g.free(output, size)
		return out
	}
}

// Free releases the terminal's resources.
func (t *VtTerminal) Free() {
	if t.freed {
		return
	}
	t.freed = true
	g := t.g
	delete(g.terminals, t.handle)
	if t.render != 0 {
		g.call(fnRowCellsFree, uint64(t.cells))
		g.call(fnRowIterFree, uint64(t.rowIter))
		g.call(fnRenderFree, uint64(t.render))
	}
	if t.encoder != 0 {
		g.call(fnKeyEventFree, uint64(t.event))
		g.call(fnKeyEncoderFree, uint64(t.encoder))
	}
	if t.mouseEnc != 0 {
		g.call(fnMouseEventFree, uint64(t.mouseEvent))
		g.call(fnMouseEncoderFree, uint64(t.mouseEnc))
	}
	if t.wbuf != 0 {
		g.free(t.wbuf, t.wcap)
	}
	g.free(t.scratch, scratchSize)
	g.free(t.graphemes, maxGrapheme*4)
	g.call(fnTerminalFree, uint64(t.handle))
}
