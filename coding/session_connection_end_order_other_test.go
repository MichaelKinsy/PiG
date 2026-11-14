//go:build !linux

package coding

// The row of a close while the request body is still arriving is Linux's (session_connection_end_order_linux_test.go).
func platformSessionConnectionEnds() []sessionConnectionEnd { return nil }
