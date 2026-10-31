//go:build !race

package host

// raceDetector is whether the test binary runs under the race detector: its runtime keeps freed memory and shadow state
// past a collection, so a heap-growth bound measures the detector, not the code (remote_host_link_test.go).
const raceDetector = false
