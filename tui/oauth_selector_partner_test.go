package tui

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// Port of oauth-selector.test.ts "shows the brand style of a preferred partner only on its selected row".
func TestOAuthSelectorShowsTheBrandStyleOnlyOnTheSelectedPartnerRow(t *testing.T) {
	th := ActiveTheme()
	providers := []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
	}
	selector := NewOAuthSelectorComponentAnimated("login", providers, func(string, string) {}, func() {}, "", renderFuncTUI{f: func() {}})
	defer selector.Dispose()

	output := strings.Join(selector.Render(120), "\n")
	if strings.Contains(output, th.Fg("accent", "Radius")) {
		t.Errorf("selected Radius row kept the accent style: %q", output)
	}
	if !strings.Contains(output, th.Fg("text", "OpenRouter")) {
		t.Errorf("unselected OpenRouter row lost the text style: %q", output)
	}
	if !strings.Contains(stripANSI(output), "→ Radius") {
		t.Errorf("selected row missing: %q", stripANSI(output))
	}

	selector.HandleInput("\x1b[B")
	moved := strings.Join(selector.Render(120), "\n")
	if !strings.Contains(moved, th.Fg("text", "Radius")) {
		t.Errorf("unselected Radius row lost the text style: %q", moved)
	}
	if strings.Contains(moved, th.Fg("accent", "OpenRouter")) {
		t.Errorf("selected OpenRouter row kept the accent style: %q", moved)
	}
	if !strings.Contains(stripANSI(moved), "→ OpenRouter") {
		t.Errorf("selected row missing: %q", stripANSI(moved))
	}
}

// Port of oauth-selector.test.ts "keeps the accent style without a TUI to animate in".
func TestOAuthSelectorKeepsTheAccentStyleWithoutATUI(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"}}, func(string, string) {}, func() {})
	if output := strings.Join(selector.Render(120), "\n"); !strings.Contains(output, ActiveTheme().Fg("accent", "OpenRouter")) {
		t.Fatalf("render = %q", output)
	}
}

// Port of oauth-selector.test.ts "marks preferred partners with a star".
func TestOAuthSelectorMarksPreferredPartnersWithAStar(t *testing.T) {
	selector := NewOAuthSelectorComponent("login", []OAuthProvider{
		{ID: "radius", Name: "Radius", AuthType: "oauth"},
		{ID: "openrouter", Name: "OpenRouter", AuthType: "api_key"},
		{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
	}, func(string, string) {}, func() {})
	output := stripANSI(strings.Join(selector.Render(120), "\n"))
	for _, want := range []string{"→ Radius ✦ [subscription]", "  OpenRouter ✦ [API key]", "  Anthropic [API key]"} {
		if !strings.Contains(output, want) {
			t.Errorf("render missing %q:\n%s", want, output)
		}
	}
}

// brandAnimationGoroutines counts the goroutines of the calling synctest bubble that run StartBrandAnimation's loop.
func brandAnimationGoroutines() int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	count := 0
	for block := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(block, "synctest bubble") && strings.Contains(block, "tui.StartBrandAnimation.func1") {
			count++
		}
	}
	return count
}

// oauth-selector.ts constructor: with a tui it starts the brand animation, which requests a render each frame while a
// partner row is selected, and dispose stops it (startBrandAnimation): no frame is requested once dispose has returned
// and the animation goroutine is gone.
func TestOAuthSelectorAnimatesAPartnerRowUntilDisposed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var disposed atomic.Bool
		var requests atomic.Int64
		selector := NewOAuthSelectorComponentAnimated("login", []OAuthProvider{
			{ID: "radius", Name: "Radius", AuthType: "oauth"},
			{ID: "anthropic", Name: "Anthropic", AuthType: "api_key"},
		}, func(string, string) {}, func() {}, "", renderFuncTUI{f: func() {
			requests.Add(1)
			if disposed.Load() {
				t.Error("a render was requested after Dispose returned")
			}
		}})
		selector.Render(120)
		time.Sleep(3 * BrandAnimationFrame)
		synctest.Wait()
		if requests.Load() == 0 {
			t.Fatal("no render was requested while the Radius row was selected")
		}
		if got := brandAnimationGoroutines(); got != 1 {
			t.Fatalf("%d animation goroutines while the selector is open, want 1", got)
		}
		selector.Dispose()
		disposed.Store(true)
		if got := brandAnimationGoroutines(); got != 0 {
			t.Errorf("%d animation goroutines after Dispose returned, want 0", got)
		}
		time.Sleep(3 * BrandAnimationFrame)
		selector.Dispose()
	})
}

// provider-brand.ts startBrandAnimation: the returned stop waits for the loop, so a render request that is in flight when
// stop is called finishes before stop returns and none follows it, even when the animating check stays true.
func TestStartBrandAnimationStopWaitsForAnInFlightRequestAndEndsTheLoop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		var requests atomic.Int64
		var stopReturned atomic.Bool
		stop := StartBrandAnimation(func() {
			requests.Add(1)
			if stopReturned.Load() {
				t.Error("a render was requested after stop returned")
			}
			once.Do(func() { close(entered) })
			<-release
		}, func() bool { return true })
		<-entered
		var returned atomic.Bool
		go func() {
			stop()
			returned.Store(true)
		}()
		synctest.Wait()
		if returned.Load() {
			t.Fatal("stop returned while a render request was still in flight")
		}
		close(release)
		synctest.Wait()
		if !returned.Load() {
			t.Fatal("stop did not return once the in-flight request finished")
		}
		stopReturned.Store(true)
		after := requests.Load()
		time.Sleep(10 * BrandAnimationFrame)
		if got := requests.Load(); got != after {
			t.Errorf("%d render requests after stop returned", got-after)
		}
		if got := brandAnimationGoroutines(); got != 0 {
			t.Errorf("%d animation goroutines after stop returned, want 0", got)
		}
		stop()
	})
}

// provider-brand.ts startBrandAnimation requests a frame only while isAnimating() is true: nothing is requested for a
// selector with no animated row, and requests start once the row animates.
func TestStartBrandAnimationRequestsFramesOnlyWhileAnimating(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var requests atomic.Int64
		var animating atomic.Bool
		stop := StartBrandAnimation(func() { requests.Add(1) }, animating.Load)
		time.Sleep(10 * BrandAnimationFrame)
		synctest.Wait()
		if got := requests.Load(); got != 0 {
			t.Errorf("%d render requests while nothing animates, want 0", got)
		}
		animating.Store(true)
		time.Sleep(3 * BrandAnimationFrame)
		synctest.Wait()
		if requests.Load() == 0 {
			t.Error("no render was requested while a row animates")
		}
		stop()
	})
}

// renderFuncTUI is a TUI whose RequestRender calls f; the selector calls nothing else on it.
type renderFuncTUI struct {
	TUI
	f func()
}

func (r renderFuncTUI) RequestRender(...bool) { r.f() }
