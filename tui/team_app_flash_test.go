package tui

import (
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Exact pinned-source bytes: node parity/testdata/next-evidence-app-b/oracle.mjs.
// In particular, truncation adds SGR 0 before the reverse-video closing SGR 27.
func TestFlashExactRender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		message string
		want    []string
	}{
		{"", []string{"", " \x1b[0m", "  ", "  ", "  "}},
		{"abcdef", []string{"", " \x1b[0m", " abc\x1b[0m", " abcd\x1b[0m", " abcdef "}},
		{"界🙂e\u0301", []string{"", " \x1b[0m", " 界\x1b[0m", " 界🙂\x1b[0m", " 界🙂e\u0301 "}},
		{"\x1b[31mred\x1b[39m", []string{"", " \x1b[0m", " \x1b[31mred\x1b[0m", " \x1b[31mred\x1b[39m ", " \x1b[31mred\x1b[39m "}},
	} {
		t.Run(tc.message, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				c := NewAltScreenFlashContainer(func() {})
				defer c.Dispose()
				c.Flash(tc.message, altScreenFlashDefaultDurationMS)
				for i, width := range []int{0, 1, 4, 5, 80} {
					want := []string{"\x1b[7m" + tc.want[i] + "\x1b[27m"}
					if got := c.Render(width); !slices.Equal(got, want) {
						t.Errorf("Render(%d) = %q, want %q", width, got, want)
					}
				}
			})
		})
	}
}

// Upstream removes by entry identity, not by position, and requests a render
// for each add/expiry. Advancing fake time must not expire the default early.
func TestFlashOrderedExpiryAndDispose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var renders atomic.Int32
		c := NewAltScreenFlashContainer(func() { renders.Add(1) })
		defer c.Dispose()
		check := func(wantRenders int32, messages ...string) {
			t.Helper()
			var want []string
			for _, message := range messages {
				want = append(want, "\x1b[7m "+message+" \x1b[27m")
			}
			if got := c.Render(80); !slices.Equal(got, want) {
				t.Errorf("render = %q, want %q", got, want)
			}
			if got := renders.Load(); got != wantRenders {
				t.Errorf("render requests = %d, want %d", got, wantRenders)
			}
		}
		advance := func(d time.Duration) {
			<-time.After(d)
			synctest.Wait()
		}
		check(0)
		c.Flash("first", altScreenFlashDefaultDurationMS)
		c.Flash("second", 250)
		c.Flash("third", 1500)
		check(3, "first", "second", "third")
		advance(249 * time.Millisecond)
		check(3, "first", "second", "third")
		advance(time.Millisecond)
		check(4, "first", "third")
		advance(749 * time.Millisecond)
		check(4, "first", "third")
		advance(time.Millisecond)
		check(5, "third")
		c.Dispose()
		check(5)
		advance(time.Second)
		check(5)
		// Disposal clears pending work; upstream also allows subsequent reuse.
		c.Flash("new", 10)
		check(6, "new")
		advance(10 * time.Millisecond)
		check(7)
	})
}

func TestFlashNonPositiveExpiryRequestsRender(t *testing.T) {
	t.Parallel()
	for _, duration := range []int{0, -25} {
		t.Run(fmt.Sprint(duration), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var renders atomic.Int32
				c := NewAltScreenFlashContainer(func() { renders.Add(1) })
				defer c.Dispose()
				c.Flash("brief", duration)
				// Wait for the zero-deadline timer to finish inside the fake-time bubble.
				synctest.Wait()
				if got := c.Render(80); len(got) != 0 {
					t.Errorf("explicit duration %d left flashes %q", duration, got)
				}
				if got := renders.Load(); got != 2 {
					t.Errorf("add + expiry requests = %d, want 2", got)
				}
			})
		})
	}
}
