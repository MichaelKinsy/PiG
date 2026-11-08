package native

import (
	"context"
	_ "embed"
	"os"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/activity"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/sprite"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
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
	// The `/tree` branches: the sessions whose branches the drawer lists, the branch being renamed, and the actions.
	expanded map[string]bool
	branch   branchEdit
	tree     *screens.Tree
	// pickSprite is the sprite picker's dialog; modeChoice the theme toggle (system, light, dark); collapsed the sidebar.
	pickSprite bool
	// The providers sheet: shown, its search, the answer being typed, and the screen's references.
	pickProvider bool
	// toasts are the errors of work off the frame, shown by the next one (guarded by mu).
	toasts        []string
	providerQuery string
	// providerSel is the provider whose sheet is open ("": the list); providerResult and providerBusy are its catalog refresh (guarded by mu).
	providerSel, providerResult string
	providerBusy                bool
	loginAnswer                 string
	providers                   *screens.Providers
	modeChoice                  string
	collapsed                   bool
	showDrawer                  bool
	stopSprite                  func()
	draft                       string
	creating                    bool
	newCwd                      string
	newTitle                    string
	shownErr                    string
	announced                   bool

	// The chat screen: the open conversation and its scope, its list, the prompt's text, the activity sheet, the markdown
	// options, and the first message of a session started from the empty state (sent once its chat opens).
	chat      *screens.Chat
	chatScope *tui.TuiScope
	chatErr   error
	chatList  ui.ListState
	draftText string
	// pendingDraft is a user message branched from, to go back into the prompt at the next frame (guarded by mu).
	pendingDraft string
	sheetKey     string // the activity row whose sheet is open, and the call opened in it
	sheetStep    string
	actItems     []activity.Item
	actFor       *transcript.Item
	actLen       int
	actWorking   bool
	// attached are the files the composer's + uploaded to the open session, to go with the next message (guarded by mu);
	// uploading counts the uploads under way and uploadErr is why the last one failed.
	attached     []screens.Upload
	uploading    int
	uploadErr    string
	md           markdown.Options
	firstText    string
	firstFor     string
	shownChatErr string
}

// New is an App with no host yet: nothing is drawn but the connecting page until Start.
func New() *App {
	// P2_DESKTOP_THEME=light|dark pins the colour mode (the visual gate's captures); the footer's toggle changes it.
	mode := os.Getenv("P2_DESKTOP_THEME")
	if mode != "light" && mode != "dark" {
		mode = ""
	}
	// The web's running text: 16 px on a 26.4 px line.
	return &App{modeChoice: mode, md: markdown.Options{Theme: &markdown.Theme{FontSize: 16, LineHeight: 1.65}}}
}

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
	// The sign-in belongs to servers that ship the login plugin; without it the sheet and the empty state's button stay away.
	providers, _ := screens.OpenProviders(h, scope)
	ctx, cancel := context.WithCancel(h.Context())
	sprite.Watch(ctx, time.Second, a.Invalidate)
	a.mu.Lock()
	a.host, a.scope, a.sessions, a.stopSprite, a.providers = h, scope, sessions, cancel, providers
	a.mu.Unlock()
	a.Invalidate()
	return nil
}

func (a *App) stop() {
	a.mu.Lock()
	scope, stopSprite, chatScope := a.scope, a.stopSprite, a.chatScope
	a.scope, a.sessions, a.host, a.stopSprite, a.providers = nil, nil, nil, nil, nil
	a.chat, a.chatScope = nil, nil
	a.mu.Unlock()
	if stopSprite != nil {
		stopSprite()
	}
	if chatScope != nil {
		chatScope.Dispose()
	}
	if scope != nil {
		scope.Dispose()
	}
}

// announceFirstFrame writes the file P2_DESKTOP_READY_FILE names (the visual gate's hook; unset in normal runs) the first time the
// sessions screen is built from loaded data, so a capture never shows the start page.
func (a *App) announceFirstFrame(sessions *screens.Sessions) {
	if a.announced || !sessions.Ready() {
		return
	}
	a.announced = true
	if path := os.Getenv("P2_DESKTOP_READY_FILE"); path != "" {
		_ = os.WriteFile(path, []byte("ready\n"), 0o600)
	}
}

// palette is the active theme's colours.
func (a *App) palette(h *surfacehost.Host) surfacehost.Palette {
	return surfacehost.Palette{Theme: h.Theme()}
}

// cycleTheme is the footer's toggle: system, light, dark, as the web UI's.
func (a *App) cycleTheme() {
	switch a.modeChoice {
	case "":
		a.modeChoice = "light"
	case "light":
		a.modeChoice = "dark"
	default:
		a.modeChoice = ""
	}
	a.mode = "" // the next frame tells the host the mode again
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
	mode := modeOf(c)
	if a.modeChoice != "" {
		mode = a.modeChoice
	}
	if mode != a.mode {
		a.mode = mode
		h.SetMode(mode)
	}
	c.SetTheme(a.themeOf(h))
	a.announceFirstFrame(sessions)
	t := c.Theme()
	// Below the web UI's breakpoint the sidebar collapses to a button, as the drawer does on a phone.
	width, _ := c.Size()
	narrow := width < 720
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		if narrow && a.showDrawer {
			ui.Column(c).Grow(1).Children(func() {
				a.sidebar(c, h, sessions, func() { a.showDrawer = false })
			})
			return
		}
		if !narrow && !a.collapsed {
			ui.Column(c).Width(sidebarWidth).BorderWidth(0, 1, 0, 0).BorderColor(t.Border).Children(func() {
				a.sidebar(c, h, sessions, func() { a.collapsed = true })
			})
		}
		ui.Column(c).Grow(1).Children(func() {
			a.pane(c, h, sessions, narrow || a.collapsed)
		})
	})
	a.spritePicker(c)
	a.providersSheet(c)
	a.branchSheet(c)
	a.activitySheet(c)
	a.showToasts(c)
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
