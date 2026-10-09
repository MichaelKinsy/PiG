# Reviewed-type proposals awaiting the lead (lg-review-c3, lg-c4-ai-b-r2)

`test/parity/interface-closure/autobind/reviewed-types.json` holds lead-approved exceptions only. Lane lg-c4-ai-b proposed the two entries below (931eb325e, cherry-picked to lg-c4-ai-b-r2 as d9992b82c and to lg-c4-ai-b-r3 as e3794b906). Review lg-review-c3 checked each against Pi 1.1.0 and took it out of the file until the lead approves it, as lg-review-c2 did in d1f8412e8. To approve one, move it back into `reviewed-types.json` unchanged.

## `pkg:agent/.#AgentTool::property:constrainedSampling` → `ai/types.go#ToolSchema.ConstrainedSampling`

Pi agent/src/types.ts:466 AgentTool extends Tool, so constrainedSampling is the Tool member of ai/src/types.ts:721 (false | ConstrainedSamplingConfig). The Go agent.AgentTool is an interface whose Schema() returns ai.ToolSchema; the three states live there as ToolSchema.ConstrainedSampling (nil when absent or false) and ToolSchema.ConstrainedSamplingDisabled (an explicit false, ai/types.go:385). ai/constrained_sampling_false_test.go and coding/extension_bridge_test.go:87 lock all three states, the same evidence as the ai Tool row.

Reviewer check: agent/src/types.ts:466 `AgentTool<...> extends Tool`, and ai/src/types.ts declares `constrainedSampling?: false | ConstrainedSamplingConfig` on Tool. The Go agent.AgentTool is an interface whose Schema() returns ai.ToolSchema, which carries ConstrainedSampling, the member the ai Tool row already closes. The lead decides whether an inherited member may bind through the Schema() accessor.

## `pkg:mcp/.#SupportedProtocolVersion` → `mcp/types.go#SupportedProtocolVersion`

Pi mcp/src/protocol/types.ts:10 SupportedProtocolVersion = (typeof SUPPORTED_PROTOCOL_VERSIONS)[number], the element type of a const array, which A3 cannot read as a literal union. Go's SupportedProtocolVersion is a string type whose values are SupportedProtocolVersions (types.go:16); TestSupportedProtocolVersionsAreThePinnedSourceArray reads the pinned types.ts and compares the latest version and the array, in order, with the Go values (red when one differs).

Reviewer check: mcp protocol/types.ts:9-10 `SUPPORTED_PROTOCOL_VERSIONS = [...] as const` and `SupportedProtocolVersion = (typeof SUPPORTED_PROTOCOL_VERSIONS)[number]`. Go has a string type whose values are SupportedProtocolVersions, and TestSupportedProtocolVersionsAreThePinnedSourceArray compares them with the pinned source. lg-c4-tui-b e1d937a6a closes the same row by rule A6 instead; integrate only one of the two.
