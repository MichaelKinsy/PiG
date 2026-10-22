//go:build !linux

package codingagent

// pidNamespace is empty off Linux, which has no PID namespaces to tell apart; the host name alone identifies the machine.
func pidNamespace() string { return "" }
