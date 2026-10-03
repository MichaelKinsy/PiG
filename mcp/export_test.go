package mcp

import "time"

// SetClientAfterFuncForTest replaces the timer source, as vitest's fake
// timers replace setTimeout upstream.
func SetClientAfterFuncForTest(c *Client, f func(d time.Duration, fn func()) (stop func() bool)) {
	c.afterFunc = f
}
