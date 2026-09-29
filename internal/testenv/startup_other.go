//go:build !windows

package testenv

// ReportStartupIfRequested does nothing outside Windows, where a process has
// no STARTUPINFO to report.
func ReportStartupIfRequested() {}
