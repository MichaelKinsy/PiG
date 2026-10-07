//go:build linux && race

package codemode

// raceEnabled is true when the race detector is on; its shadow memory keeps resident memory above the baseline.
const raceEnabled = true
