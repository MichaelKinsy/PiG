// Package mygoshell implements shell.Platform with MyGo (github.com/egoist/mygo). It is the only package of the p2 desktop
// app that imports MyGo: pin, upgrade and replace the framework here.
//
// MyGo shows the window in the system's web view (WKWebView, WebKitGTK, WebView2) and needs no cgo: it calls the
// platform's libraries through purego (docs: apps/p2desktop and docs/additive-features.md, the p2 desktop record).
package mygoshell

import (
	"context"
	"errors"
	"net/url"
	"runtime"
	"sync/atomic"

	"github.com/egoist/mygo"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell"
)

// Platform is the MyGo implementation of shell.Platform.
type Platform struct {
	// Name is the application's name (the menu bar title on macOS, window titles, notifications).
	Name string
	// ver is the version the app reports.
	ver string

	handlers shell.Handlers
}

// New is a Platform for the app called name, at version (empty: MyGo's own).
func New(name, version string) *Platform {
	if name != "" {
		mygo.App.SetName(name)
	}
	if version != "" {
		mygo.App.SetVersion(version)
	}
	return &Platform{Name: name, ver: version}
}

var _ shell.Platform = (*Platform)(nil)

func (p *Platform) Debug() bool { return mygo.IsDev() }

func (p *Platform) Version() string { return mygo.App.Version() }

func (p *Platform) SingleInstance() bool { return mygo.App.RequestSingleInstanceLock() }

func (p *Platform) Run(h shell.Handlers, ready func()) error {
	p.handlers = h
	// Registered before Run: the URL or the notification click that launched the app is delivered to them.
	if h.OpenURL != nil {
		mygo.App.OnOpenURL(h.OpenURL)
	}
	if h.SecondInstance != nil {
		mygo.App.OnSecondInstance(func(args []string, _ string) { h.SecondInstance(args) })
	}
	if h.NotificationClick != nil {
		mygo.App.OnNotificationClick(h.NotificationClick)
	}
	if h.Activate != nil {
		mygo.App.OnActivate(func(hasVisibleWindows bool) {
			if !hasVisibleWindows {
				h.Activate()
			}
		})
	}
	if runtime.GOOS == "darwin" {
		// macOS apps keep running without a window; the Dock icon opens one again (Activate).
		mygo.App.OnWindowAllClosed(func() {})
	}
	if h.Quit != nil {
		mygo.App.OnQuit(h.Quit)
	}
	mygo.App.WhenReady(ready)
	return mygo.App.Run()
}

func (p *Platform) Quit() { mygo.App.Quit() }

func (p *Platform) SetMenu(m shell.Menu) { mygo.App.SetMenu(p.menu(m)) }

func (p *Platform) menu(m shell.Menu) *mygo.Menu { return mygo.NewMenu(p.items(m)) }

func (p *Platform) items(m shell.Menu) []*mygo.MenuItem {
	out := make([]*mygo.MenuItem, 0, len(m))
	for _, item := range m {
		out = append(out, p.item(item))
	}
	return out
}

func (p *Platform) item(item shell.MenuItem) *mygo.MenuItem {
	switch {
	case item.Separator:
		return mygo.Separator()
	case item.Role != shell.RoleNone:
		role := map[shell.Role]mygo.MenuRole{
			shell.RoleApp:    mygo.RoleAppMenu,
			shell.RoleEdit:   mygo.RoleEditMenu,
			shell.RoleWindow: mygo.RoleWindowMenu,
		}[item.Role]
		return &mygo.MenuItem{Role: role}
	case len(item.Submenu) > 0:
		return &mygo.MenuItem{Label: item.Label, Submenu: p.items(item.Submenu)}
	}
	role, ok := commandRoles[item.Command]
	if ok {
		return &mygo.MenuItem{Role: role, Label: item.Label, Accelerator: item.Accelerator}
	}
	cmd := item.Command
	return &mygo.MenuItem{Label: item.Label, Accelerator: item.Accelerator, Click: func(*mygo.MenuItem, *mygo.Window) {
		if p.handlers.Command != nil {
			p.handlers.Command(cmd)
		}
	}}
}

// commandRoles are the commands MyGo has a role for. The role supplies the platform's label and accelerator unless the
// item gives its own.
var commandRoles = map[shell.Command]mygo.MenuRole{
	shell.CmdQuit:             mygo.RoleQuit,
	shell.CmdClose:            mygo.RoleClose,
	shell.CmdReload:           mygo.RoleReload,
	shell.CmdForceReload:      mygo.RoleForceReload,
	shell.CmdToggleDevTools:   mygo.RoleToggleDevTools,
	shell.CmdToggleFullScreen: mygo.RoleToggleFullScreen,
	shell.CmdMinimize:         mygo.RoleMinimize,
	shell.CmdZoomIn:           mygo.RoleZoomIn,
	shell.CmdZoomOut:          mygo.RoleZoomOut,
	shell.CmdResetZoom:        mygo.RoleResetZoom,
	shell.CmdAbout:            mygo.RoleAbout,
}

func (p *Platform) NewTray(spec shell.TraySpec) error {
	opts := mygo.TrayOptions{
		ID:             "main",
		Icon:           spec.Icon,
		IconIsTemplate: spec.Template,
		ToolTip:        spec.ToolTip,
	}
	if len(spec.Menu) > 0 {
		opts.Menu = p.menu(spec.Menu)
	}
	_, err := mygo.NewTray(opts)
	return err
}

func (p *Platform) Notify(ctx context.Context, n shell.Notification) error {
	if !mygo.NotificationsSupported() {
		return shell.ErrNotificationsDenied
	}
	err := mygo.NewNotification(mygo.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body}).ShowContext(ctx)
	if errors.Is(err, mygo.ErrNotificationsDenied) {
		return shell.ErrNotificationsDenied
	}
	return err
}

func (p *Platform) OpenExternal(rawURL string) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return
	}
	_ = mygo.Shell.OpenExternal(rawURL)
}

func (p *Platform) ShowError(title, message string) {
	_, _ = mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageError, Title: p.Name, Message: title, Detail: message})
}

// window is the main window. origin is the one origin its page may navigate to and download from, set by Load.
type window struct {
	w      *mygo.Window
	origin *atomic.Value
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (p *Platform) NewWindow(spec shell.WindowSpec) (shell.Window, error) {
	if spec.Content != nil {
		return p.newNativeWindow(spec)
	}
	devtools := mygo.DevToolsDisabled
	if spec.DevTools {
		devtools = mygo.DevToolsEnabled
	}
	start := spec.URL
	if start == "" {
		start = "about:blank"
	}
	// the window shows p2's page with no Go bridge and no navigation beyond the server's origin.
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:           spec.Title,
		URL:             start,
		Width:           spec.Width,
		Height:          spec.Height,
		MinWidth:        spec.MinWidth,
		MinHeight:       spec.MinHeight,
		StateKey:        spec.StateKey,
		BackgroundColor: spec.Background,
		Page: mygo.PageOptions{
			// The page is p2's own web UI served on loopback, not an app page of MyGo's: no Go method is bound, and
			// nothing the page asks of the window (navigation, new windows, downloads, permissions) is allowed unless
			// the handlers below say so. Without Untrusted, development builds would trust every loopback page.
			Untrusted: true,
			DevTools:  devtools,
		},
	})
	page := w.Page()
	origin := &atomic.Value{}
	origin.Store(spec.Origin)
	allowed := func() string { return origin.Load().(string) }
	sameOrigin := func(raw string) bool { return allowed() != "" && originOf(raw) == allowed() }
	// The window says "PiG", not the page's title (the web UI's own title is for browser tabs).
	page.OnPageTitleUpdated(func(e *mygo.TitleEvent) { e.PreventDefault() })
	page.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if e.URL == "about:blank" || sameOrigin(e.URL) {
			return
		}
		e.PreventDefault()
		if e.UserInitiated {
			p.OpenExternal(e.URL)
		}
	})
	page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if req.UserInitiated {
			p.OpenExternal(req.URL)
		}
		return nil
	})
	page.OnWillDownload(func(e *mygo.DownloadEvent) {
		if !sameOrigin(e.URL) {
			e.PreventDefault()
		}
	})
	page.SetPermissionHandler(func(req mygo.PermissionRequest) bool {
		if !spec.Microphone || allowed() == "" || req.Origin != allowed() {
			return false
		}
		for _, permission := range req.Permissions {
			if permission != mygo.PermissionMicrophone {
				return false
			}
		}
		return true
	})
	return window{w, origin}, nil
}

func (w window) Load(rawURL string) {
	w.origin.Store(originOf(rawURL))
	_ = w.w.Page().LoadURL(rawURL)
}
func (w window) Show()              { w.w.Show() }
func (w window) Focus()             { w.w.Show(); w.w.Focus() }
func (w window) Focused() bool      { return !w.w.IsDestroyed() && w.w.IsFocused() }
func (w window) Close()             { w.w.Close() }
func (w window) OnClosed(fn func()) { w.w.OnClosed(fn) }
