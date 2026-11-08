package native

import (
	_ "embed"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/egoist/mygo/ui"
)

//go:embed logo.png
var logoPNG []byte

var logo, _ = ui.DecodeBitmap(logoPNG)

// pig additive (D481): the native client is a surface host (internal/surfacehost); these are its views.

// App is the window's content: a view that is a function of the surface host's state. Make it with New (before the host,
// whose Changed is App.Invalidate), give it the host with Start, give it to the window (shell.Content, Build).
type App struct {
	mu     sync.Mutex
	redraw func()
	host   *surfacehost.Host

	scope    *tui.TuiScope
	sessions *screens.Sessions

	// Theme, rebuilt when the host's active theme changes.
	theme    *ui.Theme
	themeKey string
	// mode is the colour mode the host was last told.
	mode string

	// The sessions screen's own state.
	query    string
	archived bool
	selected string
	list     ui.ListState
	renaming string
	draft    string
	creating bool
	newCwd   string
	newTitle string
	shownErr string
}

// New is an App with no host yet: nothing is drawn but the connecting page until Start.
func New() *App { return &App{} }

// Attach gives the content its window's redraw (from any goroutine); it implements shell.Content.
func (a *App) Attach(redraw func()) {
	a.mu.Lock()
	a.redraw = redraw
	a.mu.Unlock()
}

// Detach is called when the window closed; it implements shell.Content.
func (a *App) Detach() {
	a.mu.Lock()
	a.redraw = nil
	a.mu.Unlock()
	a.stop()
}

// Invalidate asks for a new frame: any change of state calls it, from any goroutine.
func (a *App) Invalidate() {
	a.mu.Lock()
	redraw := a.redraw
	a.mu.Unlock()
	if redraw != nil {
		redraw()
	}
}

// Start gives the app its surface host and opens the sessions screen on it.
func (a *App) Start(h *surfacehost.Host) error {
	scope := h.Scope(a.Invalidate)
	sessions, err := screens.OpenSessions(h, scope)
	if err != nil {
		scope.Dispose()
		return err
	}
	a.mu.Lock()
	a.host, a.scope, a.sessions = h, scope, sessions
	a.mu.Unlock()
	a.Invalidate()
	return nil
}

func (a *App) stop() {
	a.mu.Lock()
	scope := a.scope
	a.scope, a.sessions, a.host = nil, nil, nil
	a.mu.Unlock()
	if scope != nil {
		scope.Dispose()
	}
}

// themeOf is the MyGo theme of the host's active theme, kept until it changes.
func (a *App) themeOf(h *surfacehost.Host) *ui.Theme {
	active := h.Theme()
	key := active.ID + "/" + active.Mode
	for role, value := range active.Colors {
		key += "|" + role + value
	}
	if a.theme == nil || key != a.themeKey {
		a.theme, a.themeKey = Theme(surfacehost.Palette{Theme: active}), key
	}
	return a.theme
}

// Build is the window's view, called for every frame: f(state).
func (a *App) Build(c *ui.Context) {
	a.mu.Lock()
	h, sessions := a.host, a.sessions
	a.mu.Unlock()
	if h == nil || sessions == nil {
		c.SetTheme(Theme(surfacehost.Palette{Theme: fallbackTheme()}))
		ui.Column(c).Fill().Center().Gap(12).Children(func() {
			ui.Spinner(c)
			ui.Text(c, "Starting PiG…").TextColor(c.Theme().TextMuted)
		})
		return
	}
	// The system's colour mode (the theme of a frame before SetTheme) follows into the host: the active theme is the user's
	// pick, else the first of that mode.
	if mode := modeOf(c); mode != a.mode {
		a.mode = mode
		h.SetMode(mode)
	}
	c.SetTheme(a.themeOf(h))
	t := c.Theme()
	ui.Row(c).Fill().Children(func() {
		ui.Column(c).Width(340).Background(t.Surface).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
			a.sidebar(c, h, sessions)
		})
		ui.Column(c).Grow(1).Padding(24).Gap(16).Children(func() {
			a.detail(c, sessions)
		})
	})
	if err := sessions.LastError(); err != nil {
		c.Toast(err.Error())
		sessions.ClearError()
	}
	c.After(30 * time.Second)
}

func modeOf(c *ui.Context) string {
	if c.Theme().Dark {
		return "dark"
	}
	return "light"
}
