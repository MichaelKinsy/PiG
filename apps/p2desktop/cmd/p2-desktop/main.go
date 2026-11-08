// Command p2-desktop is the p2 desktop app: the p2 server in this process on a loopback port, shown in a window of the
// system's web view. It is also the session worker the server starts (`p2-desktop worker`), as `p2` is its own.
package main

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/desktop"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell/mygoshell"
)

//go:embed resources/tray.png
var trayIcon []byte

// pig additive (D480): the desktop app is one executable that is the app and the session worker the in-process server forks.
func main() {
	if len(os.Args) > 1 && os.Args[1] == hosts.WorkerArg {
		hosts.RunWorker()
		return
	}
	cfg := desktop.Config{
		Platform:  mygoshell.New("p2", ""),
		Title:     "p2",
		StateDir:  os.Getenv("P2_DESKTOP_STATE_DIR"),
		TrayIcon:  trayIcon,
		LeakCheck: os.Getenv("P2_DESKTOP_LEAKCHECK") == "1",
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
