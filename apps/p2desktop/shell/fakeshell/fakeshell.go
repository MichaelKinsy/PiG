// Package fakeshell is a shell.Platform with no framework and no display, for tests of the app's behavior: it records what
// the app asked for, and lets a test deliver what the platform would (a deep link, a click on a notification, a quit).
package fakeshell

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell"
)

// Platform is a recording shell.Platform. Run blocks until Quit, running ready first, as the real event loop does.
type Platform struct {
	DebugBuild bool
	// NotifyErr is what Notify answers.
	NotifyErr error
	// NotHolder makes SingleInstance answer false, as a second process does.
	NotHolder bool

	mu            sync.Mutex
	Handlers      shell.Handlers
	Menu          shell.Menu
	Windows       []*Window
	TrayIcons     []shell.TraySpec
	Notifications []shell.Notification
	External      []string
	Errors        []string
	quit          chan struct{}
	quitOnce      sync.Once
}

// New is a Platform whose event loop has not started.
func New() *Platform { return &Platform{quit: make(chan struct{})} }

var _ shell.Platform = (*Platform)(nil)

func (p *Platform) Debug() bool          { return p.DebugBuild }
func (p *Platform) Version() string      { return "test" }
func (p *Platform) SingleInstance() bool { return !p.NotHolder }

// Run runs ready, then waits for Quit, then runs the Quit handler.
func (p *Platform) Run(h shell.Handlers, ready func()) error {
	p.mu.Lock()
	p.Handlers = h
	p.mu.Unlock()
	ready()
	<-p.quit
	if h.Quit != nil {
		h.Quit()
	}
	return nil
}

func (p *Platform) Quit() { p.quitOnce.Do(func() { close(p.quit) }) }

func (p *Platform) SetMenu(m shell.Menu) {
	p.mu.Lock()
	p.Menu = m
	p.mu.Unlock()
}

func (p *Platform) NewWindow(spec shell.WindowSpec) (shell.Window, error) {
	w := &Window{Spec: spec, URL: spec.URL, focused: true}
	if spec.Content != nil {
		spec.Content.Attach(func() { w.redraws.Add(1) })
		w.onClose = append(w.onClose, spec.Content.Detach)
	}
	p.mu.Lock()
	p.Windows = append(p.Windows, w)
	p.mu.Unlock()
	return w, nil
}

func (p *Platform) NewTray(spec shell.TraySpec) error {
	p.mu.Lock()
	p.TrayIcons = append(p.TrayIcons, spec)
	p.mu.Unlock()
	return nil
}

func (p *Platform) Notify(_ context.Context, n shell.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.NotifyErr != nil {
		return p.NotifyErr
	}
	p.Notifications = append(p.Notifications, n)
	return nil
}

func (p *Platform) OpenExternal(rawURL string) {
	p.mu.Lock()
	p.External = append(p.External, rawURL)
	p.mu.Unlock()
}

func (p *Platform) ShowError(title, message string) {
	p.mu.Lock()
	p.Errors = append(p.Errors, title+": "+message)
	p.mu.Unlock()
}

// Snapshot returns copies of what was recorded, safe to read while the app runs.
func (p *Platform) Snapshot() (windows []*Window, notifications []shell.Notification, external, errs []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*Window(nil), p.Windows...), append([]shell.Notification(nil), p.Notifications...),
		append([]string(nil), p.External...), append([]string(nil), p.Errors...)
}

// Window is a recording shell.Window.
type Window struct {
	Spec shell.WindowSpec

	mu      sync.Mutex
	URL     string
	Loads   []string
	focused bool
	closed  bool
	onClose []func()
	// redraws counts the frames native content asked for.
	redraws atomic.Int64
}

// Redraws is how many frames the window's native content asked for.
func (w *Window) Redraws() int64 { return w.redraws.Load() }

func (w *Window) Load(url string) {
	w.mu.Lock()
	w.URL = url
	w.Loads = append(w.Loads, url)
	w.mu.Unlock()
}

func (w *Window) Show() {}

func (w *Window) Focus() { w.SetFocused(true) }

func (w *Window) Focused() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.focused && !w.closed
}

// SetFocused sets what Focused reports.
func (w *Window) SetFocused(focused bool) {
	w.mu.Lock()
	w.focused = focused
	w.mu.Unlock()
}

func (w *Window) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	fns := w.onClose
	w.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (w *Window) OnClosed(fn func()) {
	w.mu.Lock()
	w.onClose = append(w.onClose, fn)
	w.mu.Unlock()
}

// CurrentURL is the last URL loaded.
func (w *Window) CurrentURL() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.URL
}
