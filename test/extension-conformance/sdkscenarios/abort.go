package sdkscenarios

// abortRule is Escape during a tool call of an extension.
func abortRule() []Scenario {
	const group = "abort"
	return []Scenario{
		{
			ID: "noncooperative-tool-gets-a-grace-period-then-aborted", Group: group,
			Pi:     "agent-loop.ts awaits tool.execute after the signal aborts; pig divergence D111 (docs/parity/DIVERGENCES.md) for an extension tool that ignores its signal: signal it, wait ToolAbortGrace (3 s), then answer \"Operation aborted\" (agent-loop.ts:623), identically in every SDK",
			Covers: []string{"request tool_call"},
			Spec:   Spec{Tools: []Tool{{Name: "stuck", Description: "Ignores its abort signal", Behavior: ToolHang}}},
			Steps:  []Step{{Op: "abort-tool", Name: "stuck"}},
			Want: []string{
				`abort stuck result "Operation aborted" error=true`,
				`abort stuck grace=waited`,
			},
		},
		{
			ID: "cooperative-tool-finishes-within-the-grace-period", Group: group,
			Pi:     "agent-loop.ts awaits tool.execute: a tool that sees the abort signal and returns its result gets that result; the grace period only bounds a tool that never answers",
			Covers: []string{"request tool_call"},
			Spec:   Spec{Tools: []Tool{{Name: "polite", Description: "Waits for its abort signal", Behavior: ToolCooperative}}},
			Steps:  []Step{{Op: "abort-tool", Name: "polite"}},
			Want: []string{
				`abort polite result "cooperative stopped" error=false`,
				`abort polite grace=none`,
			},
		},
	}
}
