// Package parity provides the test harness for TUI byte-level and visible-state
// parity against upstream pi-tui.
//
// Two test styles:
//
//  1. Headless byte parity (`AssertByteParity`): runs a renderer function
//     against an in-process Grid, captures the output stream, and asserts the
//     resulting grid state matches a golden file. Used for fast unit-level
//     parity checks of individual components.
//
//  2. Live tmux parity (`AssertLiveParity`): drives both `~/.local/bin/pig`
//     and the installed upstream pi binary through a tmux scenario, captures
//     panes, and asserts the visible state matches.
//
// TUI parity is enforced by parity/scenarios/*.tmux scenarios.
package parity

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/termsim"
)

// Capture runs render into an in-process Grid of the given dimensions and
// returns the final grid plus the raw byte stream.
func Capture(rows, cols int, render func(w *bytes.Buffer)) (*termsim.Grid, []byte) {
	var buf bytes.Buffer
	render(&buf)
	g := termsim.New(rows, cols)
	g.Write(buf.Bytes())
	return g, buf.Bytes()
}

// AssertByteParity runs `pigRender` and `upstreamRender` into independent
// grids and asserts the resulting visible state is identical line-for-line.
// Both functions receive a fresh buffer they should write their full render
// output into.
//
// On failure, prints a side-by-side diff. Style differences are NOT compared
// here (rendering colour is a separate axis verified by AssertStyleParity).
func AssertByteParity(t *testing.T, rows, cols int, pigRender, upstreamRender func(*bytes.Buffer)) {
	t.Helper()
	gGrid, gBytes := Capture(rows, cols, pigRender)
	uGrid, uBytes := Capture(rows, cols, upstreamRender)
	if gGrid.String() != uGrid.String() {
		t.Errorf("byte parity failed.\n--- pig (visible) ---\n%s\n--- upstream (visible) ---\n%s",
			withRowMarkers(gGrid.String()), withRowMarkers(uGrid.String()))
		t.Logf("pig raw bytes: %d, upstream raw bytes: %d", len(gBytes), len(uBytes))
	}
}

// AssertGolden compares the visible state of `render` against a golden file
// in testdata/<name>.txt. Update goldens via UPDATE_GOLDEN=1.
func AssertGolden(t *testing.T, name string, rows, cols int, render func(*bytes.Buffer)) {
	t.Helper()
	g, _ := Capture(rows, cols, render)
	got := g.String()
	path := filepath.Join("testdata", name+".txt")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden read %s: %v (run with UPDATE_GOLDEN=1 to create)", path, err)
	}
	if got != string(want) {
		t.Errorf("golden mismatch.\n--- got ---\n%s\n--- want ---\n%s",
			withRowMarkers(got), withRowMarkers(string(want)))
	}
}

func withRowMarkers(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		b.WriteString(formatRow(i, line))
		b.WriteByte('\n')
	}
	return b.String()
}

func formatRow(i int, line string) string {
	return "  " + padInt(i, 3) + "│" + line + "│"
}

func padInt(n, width int) string {
	s := ""
	for n > 0 || len(s) < width {
		if n == 0 {
			s = " " + s
			continue
		}
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
