package extensionconformance

import "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"

// The conformance hosts serve the editors an extension's view holds, as the pig command's host does.
func init() { subprocess.EnableEditorViews() }
