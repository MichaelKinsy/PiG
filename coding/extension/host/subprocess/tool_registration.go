package subprocess

// Ports packages/coding-agent/src/core/extensions/loader.ts.

import (
	"context"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// handleToolRegistration mirrors loader.ts:273-284: validate before replacement and refresh before returning.
func (h *Host) handleToolRegistration(ctx context.Context, me *managedExt, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	h.toolRegistrationMu.Lock()
	defer h.toolRegistrationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var tool ToolDecl
	if err := json.Unmarshal(call.Args, &tool); err != nil {
		return nil, fmt.Errorf("decode registerTool: %w", err)
	}
	reg := &RegisterPayload{Name: me.config.Name, Tools: []ToolDecl{tool}}
	if err := validateRegisterPayload(me.config.Name, reg); err != nil {
		return nil, err
	}
	h.mu.Lock()
	registered := h.exts[me.config.Name]
	h.mu.Unlock()
	if registered != me || me.ext == nil {
		return nil, errors.New("extension registration is no longer active")
	}
	built := h.buildExtension(me, reg)
	me.ext.SetRegisteredTool(built.Tools[tool.Name])
	if h.uiBridge == nil {
		return &CallResultPayload{}, nil
	}
	if _, err := h.uiBridge.handleCall(ctx, me.config.Name, conn, &CallPayload{Method: "refreshTools"}); err != nil {
		return nil, err
	}
	// The registry is read through getAllTools and getActiveTools, which every SDK answers with a host call, so the reply carries none of it: a registration costs the same at any catalog size.
	return &CallResultPayload{}, nil
}
