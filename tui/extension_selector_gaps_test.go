package tui

import (
	"strings"
	"testing"
	"time"
)

// Pi: packages/coding-agent/src/modes/interactive/components/extension-selector.ts handleInput / constructor.
// The highlighted row moves with up/down and k/j and stops at both ends; the arrow marks it in the rendered rows; selection reports the highlighted option, a cancelled selector reports none, and a finished selector ignores further input.
func TestExtensionSelectorComponentNavigationSelectionAndCompletion(t *testing.T) {
	var picked []string
	s := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, func(o string) { picked = append(picked, o) }, nil)
	if s.SelectedIndex() != 0 || s.SelectedValue() != "a" || s.Done() || s.Cancelled() {
		t.Fatalf("initial state index %d value %q done %v cancelled %v", s.SelectedIndex(), s.SelectedValue(), s.Done(), s.Cancelled())
	}
	marked := func() string {
		for line := range strings.SplitSeq(stripANSI(strings.Join(s.Render(60), "\n")), "\n") {
			if strings.Contains(line, "→ ") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	if marked() != "→ a" {
		t.Fatalf("marked row = %q", marked())
	}
	s.HandleInput("\x1b[A")
	s.HandleInput("k")
	if s.SelectedIndex() != 0 {
		t.Fatalf("up past the first option moved to %d", s.SelectedIndex())
	}
	s.HandleInput("\x1b[B")
	if s.SelectedIndex() != 1 || marked() != "→ b" {
		t.Fatalf("down: index %d marked %q", s.SelectedIndex(), marked())
	}
	s.HandleInput("j")
	s.HandleInput("j")
	s.HandleInput("\x1b[B")
	if s.SelectedIndex() != 2 || marked() != "→ c" {
		t.Fatalf("down past the last option: index %d marked %q", s.SelectedIndex(), marked())
	}
	s.HandleInput("k")
	if s.SelectedIndex() != 1 || marked() != "→ b" {
		t.Fatalf("k moved to %d, marked %q", s.SelectedIndex(), marked())
	}
	s.HandleInput("\r")
	if !s.Done() || s.Cancelled() || len(picked) != 1 || picked[0] != "b" || s.SelectedIndex() != 1 || s.SelectedValue() != "b" {
		t.Fatalf("after confirm: done %v cancelled %v picked %v index %d", s.Done(), s.Cancelled(), picked, s.SelectedIndex())
	}
	s.HandleInput("\x1b[A")
	s.HandleInput("\r")
	if s.SelectedIndex() != 1 || len(picked) != 1 {
		t.Fatalf("a finished selector reacted to input: index %d picked %v", s.SelectedIndex(), picked)
	}
	s.Cancel()
	if s.Cancelled() {
		t.Fatal("Cancel changed an already finished selector")
	}

	c := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, nil, nil)
	c.HandleInput("j")
	c.Cancel()
	if !c.Done() || !c.Cancelled() || c.SelectedIndex() != -1 || c.SelectedValue() != "" {
		t.Fatalf("cancelled: done %v cancelled %v index %d value %q", c.Done(), c.Cancelled(), c.SelectedIndex(), c.SelectedValue())
	}
	c.HandleInput("\r")
	if c.SelectedValue() != "" {
		t.Fatal("a cancelled selector selected after Cancel")
	}

	pg := make([]string, 20)
	for i := range pg {
		pg[i] = string(rune('a' + i))
	}
	p := NewExtensionSelectorComponent("Pick", pg, nil, nil)
	p.HandleInput("\x1b[6~")
	pageMarked := func() string {
		for line := range strings.SplitSeq(stripANSI(strings.Join(p.Render(60), "\n")), "\n") {
			if strings.Contains(line, "→ ") {
				return strings.TrimSpace(line)
			}
		}
		return ""
	}
	if p.SelectedIndex() != 8 || pageMarked() != "→ i" {
		t.Fatalf("page down moved to %d, marked %q; want 8 (i)", p.SelectedIndex(), pageMarked())
	}
	p.HandleInput("\x1b[5~")
	if p.SelectedIndex() != 0 || pageMarked() != "→ a" {
		t.Fatalf("page up moved to %d, marked %q", p.SelectedIndex(), pageMarked())
	}
}

// extension-selector.ts: the countdown shows `${title} (${s}s)`; a description is a text row between the title and the rows and can be replaced or removed; dispose stops the timer and a replaced countdown is disposed.
func TestExtensionSelectorComponentCountdownTitleDescriptionAndReplacement(t *testing.T) {
	s := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
	s.SetCountdown(7)
	if out := stripANSI(strings.Join(s.Render(60), "\n")); !strings.Contains(out, "Pick (7s)") {
		t.Fatalf("countdown title missing:\n%s", out)
	}
	s.SetCountdown(3)
	if out := stripANSI(strings.Join(s.Render(60), "\n")); !strings.Contains(out, "Pick (3s)") || strings.Contains(out, "(7s)") {
		t.Fatalf("a second countdown value must replace the first, not append to it:\n%s", out)
	}
	s.SetCountdown(7)
	s.SetDescription("because")
	lines := strings.Split(stripANSI(strings.Join(s.Render(60), "\n")), "\n")
	title, desc, row := -1, -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, "Pick (7s)"):
			title = i
		case strings.Contains(l, "because"):
			desc = i
		case strings.Contains(l, "→ a"):
			row = i
		}
	}
	if title < 0 || desc <= title || row <= desc {
		t.Fatalf("title %d, description %d, row %d:\n%s", title, desc, row, strings.Join(lines, "\n"))
	}
	s.SetDescription("")
	if out := stripANSI(strings.Join(s.Render(60), "\n")); strings.Contains(out, "because") {
		t.Fatalf("description not removed:\n%s", out)
	}

	loop := make(chan func(), 4)
	ticks, expired := 0, 0
	titleAtTick := ""
	s.StartCountdown(2*time.Second, func(f func()) { loop <- f }, func() { ticks++; titleAtTick = s.title }, func() { expired++ })
	first := s.countdown
	if out := stripANSI(strings.Join(s.Render(60), "\n")); !strings.Contains(out, "Pick (2s)") || ticks != 1 || titleAtTick != "Pick (2s)" {
		t.Fatalf("StartCountdown: ticks %d, title at tick %q\n%s", ticks, titleAtTick, out)
	}
	s.StartCountdown(time.Hour, func(f func()) { loop <- f }, func() {}, func() {})
	defer s.Dispose()
	if !first.stopped || s.countdown == first {
		t.Fatal("a replaced countdown was left running")
	}
	if expired != 0 || s.Done() {
		t.Fatal("the replaced countdown expired")
	}

	e := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
	defer e.Dispose()
	expiry := 0
	expLoop := make(chan func(), 4)
	e.StartCountdown(time.Second, func(f func()) { expLoop <- f }, func() {}, func() { expiry++ })
	select {
	case tick := <-expLoop:
		tick()
	case <-time.After(10 * time.Second):
		t.Fatal("countdown did not tick")
	}
	if expiry != 1 || !e.Done() || !e.Cancelled() {
		t.Fatalf("expiry: callback %d done %v cancelled %v", expiry, e.Done(), e.Cancelled())
	}
}
