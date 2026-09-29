//go:build windows

package codingagent

import "github.com/MichaelKinsy/PiG/internal/testenv"

// reportStartupIfRequested lets the test binary stand in for a spawned
// program and report how it was started.
func reportStartupIfRequested() { testenv.ReportStartupIfRequested() }
