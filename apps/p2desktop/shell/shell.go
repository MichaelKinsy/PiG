// Package shell is the seam between the p2 desktop app and the desktop framework that draws its window. The app code
// (internal/desktop) depends only on these types; shell/mygoshell is the one package that imports MyGo, so a change in
// MyGo's API, or a different framework, is contained there (docs/additive-features.md, the p2 desktop record).
//
// Everything here is plain Go: no framework type crosses the seam, so the app's behavior is testable without a display
// (shell/fakeshell).
package shell

import (
	"context"
	"errors"
)

// ErrNotificationsDenied is what Notify reports when the user refused notifications (or the platform has none).
var ErrNotificationsDenied = errors.New("shell: notifications are not allowed")

// Command is something a menu item or the tray asks the app to do. The adapter maps a command to the framework's own role
// for it where one exists (so labels, accelerators and enablement follow the platform), and to a callback otherwise.
type Command int

const (
	CmdNone Command = iota
	CmdQuit
	CmdClose
	CmdReload
	CmdForceReload
	CmdToggleDevTools
	CmdToggleFullScreen
	CmdMinimize
	CmdZoomIn
	CmdZoomOut
	CmdResetZoom
	// CmdShow shows (and focuses) the main window, opening it again when it was closed.
	CmdShow
	// CmdAbout shows the platform's about panel.
	CmdAbout
)

// Role is a standard menu the platform builds itself: Edit's items follow the focused page's state, for example.
type Role int

const (
	RoleNone Role = iota
	RoleApp
	RoleEdit
	RoleWindow
)

// MenuItem is one menu entry. Exactly one of Role, Command or Submenu or Separator is meaningful.
type MenuItem struct {
	Label       string
	Accelerator string
	Role        Role
	Command     Command
	Separator   bool
	Submenu     []MenuItem
}

// Menu is a template: the menu bar, or the tray's menu.
type Menu []MenuItem

// WindowSpec is what the app asks of its main window.
type WindowSpec struct {
	Title         string
	Width, Height int
	MinWidth      int
	MinHeight     int
	// StateKey remembers the window's bounds across launches.
	StateKey string
	// URL is loaded once the window exists; Load changes it later. Empty shows a blank page.
	URL string
	// Background fills the window until the page paints (CSS color syntax).
	Background string
	// Origin is the only origin the page may navigate to, or download from; empty until the app knows it (Load sets it).
	// Everything else opens in the system browser (http and https) or is refused.
	Origin string
	// DevTools allows the web inspector. It is false in release builds.
	DevTools bool
	// Microphone lets the page use the microphone (the voice mode), after the system's own prompt.
	Microphone bool
}

// Window is the app's main window.
type Window interface {
	// Load navigates to url and makes url's origin the window's Origin.
	Load(url string)
	Show()
	Focus()
	// Focused reports whether the window is the active window of the active app.
	Focused() bool
	Close()
	// OnClosed runs once the window closed.
	OnClosed(fn func())
}

// Notification is a system notification.
type Notification struct {
	// ID replaces a notification still shown with the same ID, and comes back to OnNotificationClick.
	ID    string
	Title string
	Body  string
}

// TraySpec describes the tray (menu bar, notification area) icon.
type TraySpec struct {
	// Icon is a PNG, black on transparent: macOS tints it as a template.
	Icon    []byte
	ToolTip string
	Menu    Menu
}

// Handlers are what the platform calls back, set before Run so that a deep link or a second launch that started the app
// is not missed.
type Handlers struct {
	// OpenURL gets a URL of the app's registered scheme (pig://), also the one the app was started with.
	OpenURL func(rawURL string)
	// SecondInstance gets the command line of another launch of the app (Windows and Linux start a new process for a
	// deep link; macOS delivers it to OpenURL).
	SecondInstance func(args []string)
	// NotificationClick gets the ID of the notification the user clicked.
	NotificationClick func(id string)
	// Activate runs when the app is activated again with no window open (the Dock icon on macOS).
	Activate func()
	// Command runs a menu or tray command the adapter does not run itself (CmdShow).
	Command func(cmd Command)
	// Quit runs once the event loop stopped, right before Run returns: the app releases what it holds here.
	Quit func()
}

// Platform is the desktop framework.
type Platform interface {
	// Debug reports a debug build: the web inspector and its menu item exist only there.
	Debug() bool
	// SingleInstance asks for the app's only-one-process lock and reports whether this process holds it. A process that
	// does not hold it handed its command line to the one that does and must exit.
	SingleInstance() bool
	// Run runs the event loop on the calling goroutine (the main one) until the app quits. ready runs on the loop once
	// the app has launched: the window, menu and tray are created from there.
	Run(h Handlers, ready func()) error
	// Quit quits the app; Run returns once the windows closed.
	Quit()
	SetMenu(Menu)
	NewWindow(WindowSpec) (Window, error)
	NewTray(TraySpec) error
	// Notify shows a notification and returns once the system has it. The first one may wait for the user's answer to the
	// system's permission question (macOS): ctx ends that wait. It reports ErrNotificationsDenied when the user refused
	// notifications.
	Notify(ctx context.Context, n Notification) error
	// OpenExternal opens an http or https URL in the system browser.
	OpenExternal(rawURL string)
	// ShowError shows a blocking error dialog.
	ShowError(title, message string)
	// Version is the version the framework reports for the app (the build's own).
	Version() string
}
