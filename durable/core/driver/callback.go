// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/abi/payload"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// Callback is host code Pi runs inside an open commit callback: the core holds a transaction (tx) open and waits for the
// result. The payload is the callback's JSON input.
type Callback func(ctx context.Context, payload json.RawMessage, tx *Tx) (any, error)

// Callbacks runs callback effects (ABI effect 13) by name.
type Callbacks struct {
	Client *Client
	// Named holds the callbacks the session installed, such as conversationCreated.
	Named map[string]Callback
}

// Handler returns the sqlhost handler of callback effects.
func (c *Callbacks) Handler() sqlhost.Handler { return c.run }

func (c *Callbacks) run(ctx context.Context, call *sqlhost.Call) error {
	var p payload.CallbackEffect
	if err := json.Unmarshal(call.Payload, &p); err != nil {
		return fmt.Errorf("callback payload: %w", err)
	}
	run, ok := c.Named[p.Name]
	if !ok {
		return fmt.Errorf("callback effect %d names %q, which the session did not install", call.ID, p.Name)
	}
	value, err := run(ctx, p.Payload, c.Client.Bind(p.TxID))
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("callback %s result: %w", p.Name, err)
	}
	call.Post(abi.Event{Kind: abi.EventHookDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: body})
	return nil
}
