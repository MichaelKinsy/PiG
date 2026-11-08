package ghostty

import (
	"context"
	"os"
	"testing"
)

// Creating and freeing terminals, and using every feature, leaves the module's memory where it was: no leaks of
// terminals, scratch space, encoders or formatters.
func TestFreeReturnsMemory(t *testing.T) {
	wasm, err := os.ReadFile("../vendor/ghostty/ghostty-vt.wasm")
	if err != nil {
		t.Fatal(err)
	}
	g, err := Load(context.Background(), wasm)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close(context.Background()) }()
	round := func() {
		vt, err := g.Terminal(Options{Cols: 80, Rows: 24, Scrollback: 100, OnReply: func([]byte) {}, ColorScheme: func() string { return "dark" }})
		if err != nil {
			t.Fatal(err)
		}
		vt.Write([]byte("\x1b[1mhello\x1b[0m\r\n\x1b[6n\x1b[?1000h\x1b[?2004h"))
		vt.Write(make([]byte, 100<<10))
		vt.Frame(true)
		vt.Snapshot()
		vt.Text()
		vt.EncodeKey(KeyInput{Code: "KeyA", Key: "a"})
		vt.EncodeMouse(MouseInput{Action: "press", Button: "left", X: 1, Y: 1, Width: 640, Height: 384, CellWidth: 8, CellHeight: 16})
		vt.EncodePaste("paste")
		vt.Free()
	}
	round()
	round() // steady state: the allocator's pools and the module's tables are warm
	before := g.mem.Size()
	for range 200 {
		round()
	}
	if after := g.mem.Size(); after != before {
		t.Fatalf("module memory grew from %d to %d bytes over 200 terminals", before, after)
	}
	if len(g.terminals) != 0 {
		t.Fatalf("%d terminals still registered", len(g.terminals))
	}
}
