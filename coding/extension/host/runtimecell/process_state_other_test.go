//go:build !windows

package runtimecell_test

// describeProcess reports nothing beyond the kill on these hosts, where a
// deadline kill already reads as "signal: killed".
func describeProcess(int) string { return "no state capture on this platform" }
