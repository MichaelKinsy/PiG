package subprocess

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// frameOf builds the component of a header or footer factory the way a host does and returns it, or nil for a cleared surface.
func frameOf(build any, _ int) any {
	switch factory := build.(type) {
	case extension.HeaderFactory:
		if factory != nil {
			return unframed(factory(nil, nil))
		}
	case extension.FooterFactory:
		if factory != nil {
			return unframed(factory(nil, nil, nil))
		}
	}
	return nil
}

// unframed is the lines of a frame whose width is unknown and the frame itself otherwise.
func unframed(component any) any {
	if frame, ok := component.(extension.WidthLines); ok && frame.Width == 0 {
		return frame.Lines
	}
	return component
}
