//go:build !windows

package env

import "testing"

// TestZZProbeReport prints what the process saw; it runs after the other tests of the package.
func TestZZProbeReport(t *testing.T) {
	t.Logf("REPORT redeliveries_total=%d signals:%s", probeRedeliveriesTotal.Load(), probeSignalReport())
}
