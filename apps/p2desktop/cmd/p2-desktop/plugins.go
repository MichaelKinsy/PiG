package main

// The plugins the server and each session worker load: they register in init, and the worker is this same executable run
// as `p2-desktop worker`. Keep the list equal to the plugins of `p2 server` (apps/p2/cmd/p2/main.go; its plugins_test.go checks
// that one against plugins/): a plugin missing here is missing from the desktop app's server and workers.
import (
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)
