//go:build !unix

package testenv

// ReportSessionIfRequested does nothing outside Unix, where a process has no session ID to report.
func ReportSessionIfRequested() {}
