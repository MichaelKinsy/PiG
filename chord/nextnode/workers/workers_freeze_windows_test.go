//go:build windows

package workers

import "time"

// freeze: a blocked event loop is emulated by stopping the process, which Windows has no signal for; the tests that block
// a worker are Unix-only (workers_blocked_unix_test.go).
func freeze(time.Duration) { panic("workers: a blocked worker is not emulated on Windows") }

func resumeFixture() {}
