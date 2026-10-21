package engine

// RgbColor preserves JavaScript numeric channels, including NaN when an overflowing channel is scaled by an infinite maximum.
type RgbColor struct {
	R float64
	G float64
	B float64
}
