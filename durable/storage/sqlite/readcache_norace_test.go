//go:build !race

package sqlite_test

// raceDetector is true when the test binary is built with -race, which runs the randomized cache tests about ten times
// slower.
const raceDetector = false
