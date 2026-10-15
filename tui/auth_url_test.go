package tui

// pi: packages/coding-agent/src/modes/interactive/components/auth-url.ts

import (
	"errors"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// packages/coding-agent/src/modes/interactive/components/auth-url.ts:12-39: the URL is an OSC 8 hyperlink to itself, the hint line carries
// the platform's click hint linked to the URL and the copy key, and copy() replaces the copy hint with the outcome and asks for a render.
func TestAuthURLRendersAndReportsTheCopyOutcome(t *testing.T) {
	authCopyKeybindings(t)
	const url = "https://auth.example.invalid/authorize?state=1"
	var renders atomic.Int32
	var copied atomic.Value
	component := NewAuthURL(url, func(text string) error { copied.Store(text); return nil }, func() { renders.Add(1) })
	if component.URL() != url {
		t.Fatalf("URL = %q", component.URL())
	}
	raw := strings.Join(component.Render(80), "\n")
	osc := "\x1b]8;;" + url + "\x1b\\"
	if strings.Count(raw, osc) != 2 {
		t.Fatalf("want the URL and the click hint both linked to the URL:\n%q", raw)
	}
	plain := widthx.StripAnsi(raw)
	clickHint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		clickHint = "Cmd+click to open"
	}
	lines := strings.Split(plain, "\n")
	if len(lines) != 2 || lines[0] != " "+url+strings.Repeat(" ", 80-1-len(url)) || !strings.HasPrefix(lines[1], " "+clickHint+" • ctrl+x to copy") {
		t.Fatalf("rendered lines = %q", lines)
	}

	select {
	case <-component.Copy():
	case <-time.After(5 * time.Second):
		t.Fatal("copy never finished")
	}
	if copied.Load() != url || renders.Load() != 1 {
		t.Fatalf("copied %v, renders %d", copied.Load(), renders.Load())
	}
	if got := widthx.StripAnsi(strings.Join(component.Render(80), "\n")); !strings.Contains(got, clickHint+" • Copied URL to clipboard") || strings.Contains(got, "to copy") {
		t.Fatalf("hint after copy:\n%s", got)
	}
	// :19, :27, :34 and :36 theme roles: the URL is accent, the click hint and bullet dim, the outcome success or error.
	theme := ActiveTheme()
	if theme.Fg("success", "x") == theme.Fg("error", "x") || theme.Fg("accent", "x") == theme.Fg("dim", "x") {
		t.Fatal("the active theme does not distinguish the roles this test checks")
	}
	styled := strings.Join(component.Render(80), "\n")
	for _, want := range []string{theme.Fg("accent", Hyperlink(url, url)), theme.Fg("dim", Hyperlink(clickHint, url)) + " " + theme.Fg("dim", "•") + " ", theme.Fg("success", "Copied URL to clipboard")} {
		if !strings.Contains(styled, want) {
			t.Fatalf("rendered hint lacks %q:\n%q", want, styled)
		}
	}

	failing := NewAuthURL(url, func(string) error { return errors.New("Clipboard unavailable") }, nil)
	<-failing.Copy()
	if got := widthx.StripAnsi(strings.Join(failing.Render(80), "\n")); !strings.Contains(got, clickHint+" • Clipboard unavailable") || strings.Contains(got, "Copied") {
		t.Fatalf("hint after a failed copy:\n%s", got)
	}
	if styled := strings.Join(failing.Render(80), "\n"); !strings.Contains(styled, theme.Fg("error", "Clipboard unavailable")) {
		t.Fatalf("failed copy is not shown in the error color:\n%q", styled)
	}
}

// AuthUrlComponent extends Container (auth-url.ts:12): the hyperlinked URL Text and the hint child.
func TestAuthURLIsAContainerOfURLAndHint(t *testing.T) {
	authCopyKeybindings(t)
	component := NewAuthURL("https://auth.example.invalid/x", func(string) error { return nil }, nil)
	if got := len(component.Children()); got != 2 {
		t.Fatalf("children = %d, want 2", got)
	}
	<-component.Copy()
	if got := widthx.StripAnsi(strings.Join(component.Render(80), "\n")); !strings.Contains(got, "Copied URL to clipboard") {
		t.Fatalf("copy outcome missing from the container render:\n%s", got)
	}
}

// The MCP sign-in screen and the login dialog hold the URL in a parent Container, which reuses a child's lines until the child is invalidated; the copy outcome arrives later from the copy goroutine.
func TestAuthURLCopyOutcomeReachesAParentContainer(t *testing.T) {
	authCopyKeybindings(t)
	component := NewAuthURL("https://auth.example.invalid/x", func(string) error { return nil }, nil)
	parent := NewContainer(component)
	_ = parent.Render(80)
	<-component.Copy()
	if got := widthx.StripAnsi(strings.Join(parent.Render(80), "\n")); !strings.Contains(got, "Copied URL to clipboard") {
		t.Fatalf("the parent kept the copy hint:\n%s", got)
	}
}
