//go:build race

package tui

// raceEnabled is true when the race detector is on. The corpus tests that run one goroutine over hundreds of thousands of inputs take 20 to 27 times as long under it as without, and the detector has nothing to find in them, so they sample the corpus under it (the plain run takes all of it).
const raceEnabled = true
