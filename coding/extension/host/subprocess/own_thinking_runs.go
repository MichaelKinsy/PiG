package subprocess

import (
	"encoding/json"
	"fmt"
)

// handleOwnThinkingRuns applies CallOwnThinkingRuns: it sets the calling extension's own flag, which the runner ORs with the other
// extensions' and the interactive mode follows. `enabled` is required, so a call without it is rejected and changes nothing.
// A call that arrives before the registration built the Extension is held and applied with it.
// pig additive (D214)
func (h *Host) handleOwnThinkingRuns(me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	var payload OwnThinkingRunsPayload
	if err := json.Unmarshal(call.Args, &payload); err != nil {
		return nil, fmt.Errorf("decode %s: %w", call.Method, err)
	}
	if payload.Enabled == nil {
		return nil, fmt.Errorf("%s requires enabled", call.Method)
	}
	enabled := *payload.Enabled
	h.mu.Lock()
	ext := me.ext
	if ext == nil {
		me.earlyOwnsThinkingRuns = &enabled
	}
	h.mu.Unlock()
	if ext != nil {
		ext.SetOwnsThinkingRuns(enabled)
	}
	return &CallResultPayload{}, nil
}

// dropOwnedThinkingRuns withdraws the ownership of an extension whose connection ended: it no longer draws anything. A restarted
// extension's earlier connection ending late must not withdraw what its successor declared, so only the current connection does.
// pig additive (D214)
func (h *Host) dropOwnedThinkingRuns(me *managedExt, conn *Conn) {
	h.mu.Lock()
	ext, current := me.ext, me.connection() == conn
	h.mu.Unlock()
	if ext != nil && current {
		ext.SetOwnsThinkingRuns(false)
	}
}
