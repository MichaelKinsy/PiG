# Parked reviewed input from lg-c4-ai-a-4 (review of dfd2ecacc)

Under the reviewer policy shared by lg-review-c1/c2/c3, lane-added entries in the reviewed-input files wait for lead approval. The entry below was removed from `test/parity/interface-closure/autobind/reviewed-types.json`, so the one row it closes, `pkg:agent/.#AgentTool::property:constrainedSampling`, returns to pending.

## `pkg:agent/.#AgentTool::property:constrainedSampling` -> `ai/types.go#ToolSchema.ConstrainedSampling` (from 710dc59ad)

Lane text: "Pi agent/src/types.ts:466 AgentTool extends Tool, so it inherits ai Tool.constrainedSampling (`false | ConstrainedSamplingConfig`, ai/src/types.ts:721). A Go AgentTool is an interface whose Schema() returns the ai.ToolSchema that carries the three states exactly as for ai Tool: ToolSchema.ConstrainedSampling (nil when absent or false) plus ToolSchema.ConstrainedSamplingDisabled (an explicit false, ai/types.go:380); the same exception and locking tests as pkg:ai/.#Tool::property:constrainedSampling apply."

Reviewer note: this applies the existing reviewed placement of `pkg:ai/.#Tool::property:constrainedSampling` to the inherited AgentTool member. It is a strong candidate for approval as a direct extension of an approved entry, and the lead decides.
