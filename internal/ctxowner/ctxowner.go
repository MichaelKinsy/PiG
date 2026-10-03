// Package ctxowner joins the lifetime of one context with the request values of another.
package ctxowner

import (
	"context"
	"time"
)

// WithValuesOf returns a context that is done when owner is done and whose values are owner's, then values'. An extension reload runs for the command that asked for it (it keeps the command's request values) while the extension processes it starts live as long as owner, not as long as that command.
func WithValuesOf(owner, values context.Context) context.Context {
	return joined{owner: owner, values: values}
}

type joined struct {
	owner, values context.Context
}

func (c joined) Deadline() (time.Time, bool) { return c.owner.Deadline() }
func (c joined) Done() <-chan struct{}       { return c.owner.Done() }
func (c joined) Err() error                  { return c.owner.Err() }

func (c joined) Value(key any) any {
	if value := c.owner.Value(key); value != nil {
		return value
	}
	return c.values.Value(key)
}
