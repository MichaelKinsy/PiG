package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestAltScreenFlashRendersReverseVideo(t *testing.T) {
	var renders int
	c := NewAltScreenFlashContainer(func() { renders++ })
	t.Cleanup(c.Dispose)
	c.Flash("hi", altScreenFlashDefaultDurationMS)
	if renders != 1 {
		t.Errorf("requestRender called %d times on flash, want 1", renders)
	}
	lines := c.Render(20)
	if len(lines) != 1 {
		t.Fatalf("render lines = %d, want 1", len(lines))
	}
	// Reverse video on/off around the padded message.
	if !strings.HasPrefix(lines[0], "\x1b[7m") || !strings.HasSuffix(lines[0], "\x1b[27m") {
		t.Errorf("line = %q, want \\x1b[7m...\\x1b[27m", lines[0])
	}
	if !strings.Contains(lines[0], " hi ") {
		t.Errorf("line = %q, want to contain space-padded message", lines[0])
	}
}

func TestAltScreenFlashTruncatesToWidth(t *testing.T) {
	c := NewAltScreenFlashContainer(func() {})
	t.Cleanup(c.Dispose)
	c.Flash("abcdefghij", altScreenFlashDefaultDurationMS)
	line := c.Render(5)[0]
	// Strip the reverse-video wrapper; visible width must be <= 5.
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "\x1b[7m"), "\x1b[27m")
	if w := widthx.VisibleWidth(inner); w > 5 {
		t.Errorf("truncated visible width = %d, want <= 5 (line %q)", w, line)
	}
}

func TestAltScreenFlashExplicitNonPositiveDurationExpiresImmediately(t *testing.T) {
	for _, duration := range []int{0, -25} {
		t.Run(fmt.Sprintf("duration_%d", duration), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := NewAltScreenFlashContainer(func() {})
				t.Cleanup(c.Dispose)
				c.Flash("brief", duration)
				if got := len(c.Render(20)); got != 1 {
					t.Fatalf("flash should be present before the timer runs: %d lines", got)
				}
				time.Sleep(time.Millisecond)
				synctest.Wait()
				if got := len(c.Render(20)); got != 0 {
					t.Fatalf("flash remains after explicit duration %d: %d lines", duration, got)
				}
			})
		})
	}
}

func TestAltScreenFlashAutoRemovesAfterDuration(t *testing.T) {
	var mu sync.Mutex
	renders := 0
	c := NewAltScreenFlashContainer(func() { mu.Lock(); renders++; mu.Unlock() })
	t.Cleanup(c.Dispose)
	c.Flash("gone", 20)
	if len(c.Render(20)) != 1 {
		t.Fatal("flash should be visible immediately")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(c.Render(20)) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(c.Render(20)); got != 0 {
		t.Fatalf("flash still visible after duration: %d lines", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if renders < 2 {
		t.Errorf("requestRender called %d times, want >= 2 (flash + auto-remove)", renders)
	}
}

func TestAltScreenFlashDisposeStopsTimers(t *testing.T) {
	var mu sync.Mutex
	renders := 0
	c := NewAltScreenFlashContainer(func() { mu.Lock(); renders++; mu.Unlock() })
	c.Flash("a", 20)
	c.Flash("b", 20)
	c.Dispose()
	if len(c.Render(20)) != 0 {
		t.Error("dispose should clear entries")
	}
	mu.Lock()
	afterDispose := renders
	mu.Unlock()
	// Wait past the duration; disposed timers must not fire requestRender.
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if renders != afterDispose {
		t.Errorf("requestRender fired after dispose: %d -> %d", afterDispose, renders)
	}
}

func TestAltScreenFlashMultiplePreservesOrder(t *testing.T) {
	c := NewAltScreenFlashContainer(func() {})
	t.Cleanup(c.Dispose)
	c.Flash("first", altScreenFlashDefaultDurationMS)
	c.Flash("second", altScreenFlashDefaultDurationMS)
	lines := c.Render(40)
	if len(lines) != 2 || !strings.Contains(lines[0], "first") || !strings.Contains(lines[1], "second") {
		t.Errorf("lines = %v, want [first, second] in order", lines)
	}
}
