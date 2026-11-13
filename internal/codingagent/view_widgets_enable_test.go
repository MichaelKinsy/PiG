package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

// The package's tests draw the widgets of an extension's views, as the pig command does, which enables them before it
// runs a command (coding/cli enableViews).
func init() { subprocess.EnableViewWidgets() }
