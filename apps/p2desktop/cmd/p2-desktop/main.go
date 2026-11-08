// Command p2-desktop is the p2 desktop app: the p2 server in this process on a loopback port, shown in a window of the
// system's web view. It is also the session worker the server starts (`p2-desktop worker`), as `p2` is its own.
package main

import (
	_ "embed"
	"fmt"
	"os"
	"runtime"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/desktop"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell/mygoshell"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell/mygoshell/native"
)

// webview says the debug fallback was asked for: the web UI in the system's web view instead of the native screens.
func webview() bool {
	if os.Getenv("P2_DESKTOP_WEBVIEW") == "1" {
		return true
	}
	for _, arg := range os.Args[1:] {
		if arg == "--webview" {
			return true
		}
	}
	return false
}

// The tray icons are the PiG pixel pig, made by icons/gen: a monochrome template image for the macOS menu bar, a coloured one
// for the Windows notification area and the Linux panel.
var (
	//go:embed resources/tray-template.png
	trayTemplate []byte
	//go:embed resources/tray-color-32.png
	trayColor32 []byte
	//go:embed resources/tray-color-64.png
	trayColor64 []byte
)

func trayIcon() (icon []byte, template bool) {
	switch runtime.GOOS {
	case "darwin":
		return trayTemplate, true
	case "windows":
		return trayColor32, false
	}
	return trayColor64, false
}

// pig additive (D481): the desktop app is one executable that is the app and the session worker the in-process server forks.
func main() {
	if len(os.Args) > 1 && os.Args[1] == hosts.WorkerArg {
		hosts.RunWorker()
		return
	}
	icon, template := trayIcon()
	cfg := desktop.Config{
		Platform:     mygoshell.New("PiG", ""),
		Title:        "PiG",
		StateDir:     os.Getenv("P2_DESKTOP_STATE_DIR"),
		TrayIcon:     icon,
		TrayTemplate: template,
		LeakCheck:    os.Getenv("P2_DESKTOP_LEAKCHECK") == "1",
	}
	// The native client is the app; the web view of p2's page is the debug fallback (--webview, P2_DESKTOP_WEBVIEW=1) until the
	// owner decides.
	if !webview() {
		cfg.Native = native.New()
	}
	app := desktop.New(cfg)
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "p2 desktop:", err)
		os.Exit(1)
	}
	if app.LeakError() != nil {
		os.Exit(3)
	}
}
