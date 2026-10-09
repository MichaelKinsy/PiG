package mcp

import "time"

// SetClientAfterFuncForTest replaces the timer source, as vitest's fake
// timers replace setTimeout upstream.
func SetClientAfterFuncForTest(c *Client, f func(d time.Duration, fn func()) (stop func() bool)) {
	c.afterFunc = f
}

// ReconnectDelayForTest is the delay a transport with options waits before reconnection attempt attempt, after the
// server sent retry (nil when it sent none).
func ReconnectDelayForTest(options StreamableHTTPReconnectOptions, attempt int, retry *int) time.Duration {
	t := &StreamableHTTPTransport{options: StreamableHTTPTransportOptions{Reconnect: options}}
	cursor := &streamCursor{}
	if retry != nil {
		cursor.retryMs, cursor.hasRetry = *retry, true
	}
	return t.reconnectDelay(attempt, cursor)
}
