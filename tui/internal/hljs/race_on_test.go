//go:build race

package hljs

// raceEnabled is true when the race detector is on. The streamed-frame tests run one goroutine per registry over every prefix of their inputs, which takes 20 to 27 times as long under the detector and gives it nothing to find, so they sample their inputs under it (the plain run takes all of them).
const raceEnabled = true
