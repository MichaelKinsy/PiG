//go:build !linux

package codingagent

// newKernelFrameTimer returns nil: the runtime's timer keeps the frame deadline on these systems (frame_timer_linux.go says why Linux has another).
func newKernelFrameTimer() frameTimer { return nil }
