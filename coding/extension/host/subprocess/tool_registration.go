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
	all, err := h.uiBridge.handleCall(ctx, me.config.Name, conn, &CallPayload{Method: "getAllTools"})
	if err != nil {
		return nil, err
	}
	active, err := h.uiBridge.handleCall(ctx, me.config.Name, conn, &CallPayload{Method: "getActiveTools"})
	if err != nil {
		return nil, err
	}
	var allTools, activeTools struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(all.Result, &allTools); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(active.Result, &activeTools); err != nil {
		return nil, err
	}
	result, err := json.Marshal(map[string]any{"allTools": allTools.Tools, "activeTools": activeTools.Tools})
	return &CallResultPayload{Result: result}, err
}
