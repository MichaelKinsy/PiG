//go:build !windows

package codingagent

// reportStartupIfRequested does nothing outside Windows, where no test
// observes how a child was started.
func reportStartupIfRequested() {}
