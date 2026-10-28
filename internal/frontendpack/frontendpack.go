// Package frontendpack holds the frontend member fused into a Piglet Binary
// (D91). The build generates registry_generated.go, which registers the
// member's Frontend factory. Stock PiG compiles this package with no member,
// so Selected returns nil and the interactive mode paints with its ANSI
// renderer.
package frontendpack

import "github.com/MichaelKinsy/PiG/extensions/sdk/frontend"

var selected func() frontend.Frontend

// register records the member's factory. A Piglet has at most one frontend.
func register(factory func() frontend.Frontend) { selected = factory }

// Fused reports whether a frontend member is fused into this executable.
func Fused() bool { return selected != nil }

// SelectedIf is Selected when on is true and nil otherwise, for a run that starts the Binary without its fused frontend.
func SelectedIf(on bool) frontend.Frontend {
	if !on {
		return nil
	}
	return Selected()
}

// Selected constructs the fused frontend, or returns nil when none is fused.
func Selected() frontend.Frontend {
	if selected == nil {
		return nil
	}
	return selected()
}
