// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
)

// PhaseEffect is a phase effect (ABI effect 8): the core asks the host to run one phase of a custom task, or its abort
// handler when Abort is true. Record is the task's record as the core holds it and Invocation the task's invocation
// identity.
type PhaseEffect struct {
	TaskID     int64           `json:"taskId"`
	Kind       string          `json:"kind"`
	Phase      *string         `json:"phase"`
	Record     json.RawMessage `json:"record"`
	Invocation int64           `json:"invocation"`
	Abort      bool            `json:"abort"`
	// Handler is the registry table index of the task definition the core reserved the task under; nil when the core
	// names none (the registry's definition of Kind runs then).
	Handler *int `json:"handler"`
	// Extension is the extension that installed the definition the core reserved the task under (scheduler.ts installerOf);
	// empty for a built-in task.
	Extension string `json:"extension"`
	// EffectID is the ID of the effect that carries this phase; the core cancels it when the invocation ends.
	EffectID uint32 `json:"-"`
}

// Phases runs the phases and abort handlers of custom tasks.
type Phases struct {
	// Run executes one phase or abort handler and returns when it finishes. A returned error completes the effect as
	// thrown; the core settles the task from it.
	Run func(ctx context.Context, effect PhaseEffect) error
}

// Handler returns the sqlhost handler of phase effects.
func (p *Phases) Handler() sqlhost.Handler { return p.run }

func (p *Phases) run(ctx context.Context, call *sqlhost.Call) error {
	var effect PhaseEffect
	if err := json.Unmarshal(call.Payload, &effect); err != nil {
		return fmt.Errorf("phase payload: %w", err)
	}
	if p.Run == nil {
		return errors.New("the host has no custom task runtime")
	}
	effect.EffectID = call.ID
	err := callDefinition(func() error { return p.Run(ctx, effect) })
	if err != nil {
		call.Post(abi.Event{Kind: abi.EventPhaseDone, ID: call.ID, Phase: abi.OutcomeThrown, Payload: wireError(err)})
		return nil
	}
	call.Post(abi.Event{Kind: abi.EventPhaseDone, ID: call.ID, Phase: abi.OutcomeResult, Payload: []byte("null")})
	return nil
}
