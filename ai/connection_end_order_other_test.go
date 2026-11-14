//go:build !linux

package ai

// The row of a close while the request body is still arriving is Linux's (connection_end_order_linux_test.go).
func platformConnectionEnds() []connectionEnd { return nil }
