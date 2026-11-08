package tui

// Helpers the plugins' TUI facets share.

import pitui "github.com/MichaelKinsy/PiG/tui"

// Percent is an overlay size of percent of the terminal ("90%").
func Percent(percent float64) *pitui.OverlayValue {
	return &pitui.OverlayValue{Value: percent, Percent: true}
}
