package main

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestConfigSelectorDoesNotDoubleJumpOnKeyRelease asserts the cursor position
// after a complete key event, not that a release is recognizable.
//
// `pig config` calls tui.EnterRawMode, which pushes \x1b[>7u, so the terminal
// reports a release for every key. The loop dispatched each raw read straight
// to HandleInput with no filter, and ConfigSelectorComponent matches a release
// for Down as KBSelectDown, so one arrow press moved the cursor two rows in any
// Kitty-protocol terminal.
//
// The choke-point gate did not catch this: it fails a file that calls
// IsKeyRelease outside the allowlist, and this loop called nothing at all.
func TestConfigSelectorDoesNotDoubleJumpOnKeyRelease(t *testing.T) {
	// Kitty press/release pairs a terminal such as Ghostty actually emits.
	const (
		downPress   = "\x1b[B"
		downRelease = "\x1b[1;1:3B"
		ctrlC       = "\x03"
	)

	newSelector := func() *tui.ConfigSelectorComponent {
		items := []tui.ResourceItem{
			{Path: "/a/one.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
			{Path: "/a/two.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
			{Path: "/a/three.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
			{Path: "/a/four.md", ResourceType: tui.ResourceSkills, Scope: "user", Origin: "top-level"},
		}
		selector := tui.NewConfigSelector(tui.BuildResourceGroups(items), 0)
		selector.SetTerminalRows(40)
		return selector
	}

	// Cursor is unexported, so compare rendered frames: the row the selector
	// marks is what the user actually sees move.
	render := func(selector *tui.ConfigSelectorComponent) string {
		return strings.Join(selector.Render(80), "\n")
	}

	baseline := newSelector()
	ui := tui.New()
	ui.Add(baseline)
	if err := driveConfigSelector(ui, baseline, strings.NewReader(downPress+ctrlC)); err != nil {
		t.Fatalf("press-only run: %v", err)
	}
	wantOneDown := render(baseline)

	// Press and release in one read, which is how a fast keystroke arrives, and
	// split across reads, which is how a slower one does.
	for _, tc := range []struct {
		name   string
		source func() io.Reader
	}{
		{
			name:   "release in the same read",
			source: func() io.Reader { return strings.NewReader(downPress + downRelease + ctrlC) },
		},
		{
			name:   "release in a later read",
			source: func() io.Reader { return &chunkReader{chunks: []string{downPress, downRelease, ctrlC}} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector := newSelector()
			ui := tui.New()
			ui.Add(selector)
			if err := driveConfigSelector(ui, selector, tc.source()); err != nil {
				t.Fatalf("drive: %v", err)
			}
			if got := render(selector); got != wantOneDown {
				t.Errorf("one Down keystroke did not land where a press alone lands;\n"+
					"the release moved the cursor a second time.\n got:\n%s\nwant:\n%s", got, wantOneDown)
			}
		})
	}
}

// chunkReader hands back one chunk per Read so a press and its release arrive
// in separate reads, as they do when a key is held briefly.
type chunkReader struct {
	chunks []string
	i      int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if c.i >= len(c.chunks) {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[c.i])
	c.i++
	return n, nil
}
