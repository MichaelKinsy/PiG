package tui

// Ports packages/coding-agent/src/modes/interactive/components/auth-url.ts

import (
	"runtime"
	"sync"
)

// AuthURL is a sign-in URL with a click hint and a copy hint. Hosts call
// [AuthURL.Copy] when `app.message.copy` is pressed, since a long URL wraps
// and often cannot be selected or clicked as a whole (SSH, tmux).
type AuthURL struct {
	Container
	url           string
	copyText      func(text string) error
	requestRender func()

	mu     sync.Mutex
	suffix string
}

// NewAuthURL returns the component for url. copyText writes the system
// clipboard; requestRender repaints after a copy finishes. Either may be nil.
func NewAuthURL(url string, copyText func(text string) error, requestRender func()) *AuthURL {
	a := &AuthURL{url: url, copyText: copyText, requestRender: requestRender, suffix: loginKeyHint("app.message.copy", "to copy")}
	// upstream: auth-url.ts constructor children; the hint child reads the current suffix so a copy outcome from the copy goroutine is rendered under the lock.
	a.Add(NewPaddedText(a.texts()[0], 1, 0, nil))
	a.Add(&authURLHint{auth: a})
	return a
}

// authURLHint is the hint Text child: the click hint, a bullet and the copy hint or the copy outcome.
type authURLHint struct{ auth *AuthURL }

func (h *authURLHint) Render(width int) []string {
	return NewPaddedText(h.auth.texts()[1], 1, 0, nil).Render(width)
}
func (*authURLHint) Invalidate() {}

// URL returns the sign-in URL.
func (a *AuthURL) URL() string { return a.url }

// Lines returns the hyperlinked URL and the hint line, each indented by one column as upstream's Text(…, 1, 0).
func (a *AuthURL) Lines() []string {
	lines := a.texts()
	return []string{" " + lines[0], " " + lines[1]}
}

func (a *AuthURL) texts() [2]string {
	t := ActiveTheme()
	clickHint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		clickHint = "Cmd+click to open"
	}
	a.mu.Lock()
	suffix := a.suffix
	a.mu.Unlock()
	return [2]string{
		t.Fg("accent", Hyperlink(a.url, a.url)),
		t.Fg("dim", Hyperlink(clickHint, a.url)) + " " + t.Fg("dim", "•") + " " + suffix,
	}
}

// Copy copies the URL to the clipboard on an owned goroutine and shows the
// outcome in the hint, as upstream's unawaited `copy()`. The returned channel
// closes when the hint shows the outcome.
func (a *AuthURL) Copy() <-chan struct{} {
	done := make(chan struct{})
	if a.copyText == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		err := a.copyText(a.url)
		t := ActiveTheme()
		a.mu.Lock()
		if err != nil {
			a.suffix = t.Fg("error", err.Error())
		} else {
			a.suffix = t.Fg("success", "Copied URL to clipboard")
		}
		a.mu.Unlock()
		// A parent Container reuses this component's lines until it is invalidated.
		a.Invalidate()
		if a.requestRender != nil {
			a.requestRender()
		}
	}()
	return done
}
