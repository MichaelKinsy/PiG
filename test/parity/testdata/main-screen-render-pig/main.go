package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/tui"
)

type component struct {
	lines   []string
	renders int
}

func (c *component) Render(int) []string { c.renders++; return c.lines }
func (*component) Invalidate()           {}

type terminal struct{ writes []string }

// Pi's fixture implements hideCursor separately as a no-op. Omit only that whole terminal-operation write.
func (w *terminal) Write(p []byte) (int, error) {
	s := string(p)
	if s != "\x1b[?25l" {
		w.writes = append(w.writes, s)
	}
	return len(p), nil
}

type frame struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Writes []int  `json:"writes"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	tui.SetCapabilities(tui.TerminalCapabilities{})
	probe := func(name string, height int, before, after []string, force bool) error {
		w := &terminal{}
		ui := tui.NewWithOutput(w, 40, height)
		c := &component{lines: before}
		ui.Add(c)
		if before != nil {
			ui.Render()
			w.writes = nil
		}
		c.lines = after
		if force {
			ui.ForceFullRender()
		}
		ui.Render()
		ui.CancelPendingRender()
		lengths := make([]int, len(w.writes))
		for i, s := range w.writes {
			for _, r := range s {
				lengths[i]++
				if r > 0xffff {
					lengths[i]++
				}
			}
		}
		return enc.Encode(frame{name, strings.Join(w.writes, ""), lengths})
	}
	large := "\x1b_Ga=T,f=100;" + strings.Repeat("A", 1200000) + "\x1b\\"
	image := "\x1b_Ga=T,f=100,q=2,C=1,c=3,r=3,i=89;AAAA\x1b\\"
	cases := []struct {
		name          string
		height        int
		before, after []string
		force         bool
	}{
		{"full-large", 24, nil, []string{large, large}, false},
		{"differential-large", 24, []string{"before"}, []string{"before", large, large}, false},
		{"reserve-before-full-placement", 5, []string{"l0", "l1", "l2", "l3", "l4"}, []string{"l0", "l1", "l2", "l3", "l4", image, "", "", "after"}, false},
		{"image-reserved-growth", 5, []string{image, ""}, []string{image, "", ""}, false},
		{"cleanup-without-advertised-support", 10, []string{image}, []string{"plain"}, true},
		{"stable-deletion-order", 10, []string{"\x1b_Ga=T,i=9;A\x1b\\", "\x1b_Ga=T,i=2;B\x1b\\", "\x1b_Ga=T,i=9;A\x1b\\"}, []string{"plain"}, true},
	}
	for _, c := range cases {
		if err := probe(c.name, c.height, c.before, c.after, c.force); err != nil {
			return err
		}
	}
	w := &terminal{}
	ui := tui.NewWithOutput(w, 40, 10)
	c := &component{lines: []string{"initial"}}
	ui.Add(c)
	var mu sync.Mutex
	var queue []func()
	ui.SetRenderDispatcher(func(fn func()) { mu.Lock(); queue = append(queue, fn); mu.Unlock() })
	ui.Render()
	beforeCount := c.renders
	c.lines = []string{"pending"}
	ui.RequestRender()
	for _, s := range []string{"first", "second", "typed"} {
		c.lines = []string{s}
		ui.RequestImmediateRender()
	}
	mu.Lock()
	ready := queue
	queue = nil
	mu.Unlock()
	for _, fn := range ready {
		fn()
	}
	ui.RequestImmediateRender()
	ui.Stop()
	mu.Lock()
	ready = queue
	queue = nil
	mu.Unlock()
	for _, fn := range ready {
		fn()
	}
	ui.CancelPendingRender()
	if err := enc.Encode(struct {
		Name    string   `json:"name"`
		Renders int      `json:"renders"`
		Lines   []string `json:"lines"`
	}{"keyboard", c.renders - beforeCount, c.lines}); err != nil {
		return err
	}
	root, err := os.MkdirTemp(os.Args[1], "render-")
	if err != nil {
		return err
	}
	logDir := filepath.Join(root, "logs")
	if err := os.Setenv("PI_TUI_DEBUG_REDRAW", "1"); err != nil {
		return err
	}
	ui = tui.NewWithOutput(&terminal{}, 40, 10)
	ui.SetLogDirectory(logDir)
	ui.Add(&component{lines: []string{"test"}})
	ui.Render()
	log, err := os.ReadFile(filepath.Join(logDir, "pi-tui-debug.log"))
	if err != nil {
		return err
	}
	_, reason, _ := strings.Cut(string(log), "] ")
	if err := enc.Encode(struct{ Name, File, Reason string }{"debug", "pi-tui-debug.log", reason}); err != nil {
		return err
	}
	if err := os.Setenv("PI_TUI_DEBUG_REDRAW", ""); err != nil {
		return err
	}
	for _, key := range []string{"TMPDIR", "TEMP", "TMP"} {
		if err := os.Setenv(key, root); err != nil {
			return err
		}
	}
	ui = tui.NewWithOutput(&terminal{}, 40, 10)
	c = &component{lines: []string{"ok"}}
	ui.Add(c)
	ui.Render()
	c.lines = []string{"ok", strings.Repeat("x", 60)}
	var failure error
	func() {
		defer func() {
			if value := recover(); value != nil {
				failure, _ = value.(error)
			}
		}()
		ui.Render()
	}()
	if failure == nil {
		return fmt.Errorf("overflow did not fail")
	}
	path := filepath.Join(root, "pi-tui-crash.log")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return enc.Encode(struct {
		Name, File                string
		Referenced, WidthRecorded bool
	}{"crash", filepath.Base(path), strings.Contains(failure.Error(), path), strings.Contains(string(data), "Terminal width: 40")})
}
