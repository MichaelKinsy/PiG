//go:build !linux

package main

// rssMB and cpuSeconds report nothing where /proc and getrusage are unavailable.
func rssMB() (current, peak float64) { return 0, 0 }

func cpuSeconds() float64 { return 0 }

func switches() (voluntary, involuntary int64) { return 0, 0 }
