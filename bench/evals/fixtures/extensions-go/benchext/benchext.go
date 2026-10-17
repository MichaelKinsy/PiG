// SPDX-License-Identifier: MIT

// Package benchext is the Go SDK port of bench-ext-NN.ts, the ten identical trivial extensions of the overhead bench: a tool, a command and four
// event handlers. The port is behaviour-equivalent to the TypeScript original (same tool result, same command, same handler set), so one scenario
// measures the same ten extensions in every realization: isolated subprocess, packed cell and fused Piglet Binary.
package benchext

import (
	"fmt"
	"sync/atomic"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// New returns extension bench-ext-<nn> with tool bench_tool_<nn>, command bench-<nn> and handlers for session_start, before_agent_start, tool_call and
// turn_end that count their events. The tool reports the count in its details, as the TypeScript original does.
func New(nn string) *sdk.Extension {
	ext := sdk.New("bench-ext-" + nn)
	var events atomic.Int64
	ext.RegisterTool(sdk.ToolDefinition{
		Name:        "bench_tool_" + nn,
		Label:       "Bench tool " + nn,
		Description: "Return a fixed string. Used by the overhead bench only.",
		Parameters: sdk.Schema{
			"type":                 "object",
			"properties":           map[string]any{"value": map[string]any{"type": "string"}},
			"additionalProperties": false,
		},
		Execute: func(sdk.Context, map[string]any) (any, error) {
			return sdk.ToolResult{Content: fmt.Sprintf("bench %s ok", nn), Details: map[string]any{"events": events.Load()}}, nil
		},
	})
	ext.Command("bench-"+nn, "Bench command "+nn, func(sdk.Context, string) error { return nil })
	for _, name := range []string{"session_start", "before_agent_start", "tool_call", "turn_end"} {
		ext.OnEvent(name, func(sdk.Context, map[string]any) (any, error) {
			events.Add(1)
			return nil, nil
		})
	}
	return ext
}
