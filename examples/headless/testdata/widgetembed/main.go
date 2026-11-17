// Command widgetembed is the program of TestHeadlessEmbedWithViewWidgetsLinksNoEditor: it embeds the extension host and
// enables the view widgets, and not the editor.
package main

import "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

func main() {
	subprocess.EnableViewWidgets()
	_ = subprocess.NewUIBridge(func() {})
}
