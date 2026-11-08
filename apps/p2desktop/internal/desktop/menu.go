package desktop

import "github.com/MichaelKinsy/PiG/apps/p2desktop/shell"

// buildMenu is the menu bar. macOS has the application menu (About, Hide, Quit) and puts Quit there; other platforms get it
// in File. The web inspector's item exists only in debug builds.
func buildMenu(goos string, debug bool) shell.Menu {
	file := shell.Menu{{Label: "Close Window", Accelerator: "CmdOrCtrl+W", Command: shell.CmdClose}}
	if goos != "darwin" {
		file = append(file, shell.MenuItem{Separator: true}, shell.MenuItem{Label: "Quit", Accelerator: "CmdOrCtrl+Q", Command: shell.CmdQuit})
	}
	view := shell.Menu{
		{Label: "Reload", Accelerator: "CmdOrCtrl+R", Command: shell.CmdReload},
		{Label: "Force Reload", Accelerator: "CmdOrCtrl+Shift+R", Command: shell.CmdForceReload},
		{Separator: true},
		{Label: "Actual Size", Accelerator: "CmdOrCtrl+0", Command: shell.CmdResetZoom},
		{Label: "Zoom In", Accelerator: "CmdOrCtrl+Plus", Command: shell.CmdZoomIn},
		{Label: "Zoom Out", Accelerator: "CmdOrCtrl+-", Command: shell.CmdZoomOut},
		{Separator: true},
		{Label: "Toggle Full Screen", Command: shell.CmdToggleFullScreen},
	}
	if debug {
		view = append(view, shell.MenuItem{Separator: true}, shell.MenuItem{Label: "Toggle Developer Tools", Accelerator: "CmdOrCtrl+Alt+I", Command: shell.CmdToggleDevTools})
	}
	menu := shell.Menu{}
	if goos == "darwin" {
		menu = append(menu, shell.MenuItem{Role: shell.RoleApp})
	}
	return append(menu,
		shell.MenuItem{Label: "File", Submenu: file},
		shell.MenuItem{Role: shell.RoleEdit},
		shell.MenuItem{Label: "View", Submenu: view},
		shell.MenuItem{Role: shell.RoleWindow},
	)
}

// buildTray is the tray icon's menu.
func buildTray() shell.Menu {
	return shell.Menu{
		{Label: "Show", Command: shell.CmdShow},
		{Separator: true},
		{Label: "Quit", Command: shell.CmdQuit},
	}
}
