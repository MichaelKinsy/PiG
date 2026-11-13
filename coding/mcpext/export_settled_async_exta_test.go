package mcpext

import "context"

// SetSettledForTest replaces the token-settlement wait of a connection, which only an OAuth server's provider sets.
func SetSettledForTest(c *Connection, settled func(context.Context)) { c.settled = settled }
