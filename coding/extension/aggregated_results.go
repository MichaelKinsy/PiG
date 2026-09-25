package extension

// BeforeAgentStartCombinedResult is the aggregated result from all
// before_agent_start handlers. Returned by the runner's
// EmitBeforeAgentStart.
//
// upstream: runner.ts:103-107 (interface BeforeAgentStartCombinedResult)
//
//	interface BeforeAgentStartCombinedResult {
//		messages?: NonNullable<BeforeAgentStartEventResult["message"]>[];
//		systemPrompt?: string;
//	}
//
// Upstream declares this type as `interface` private to runner.ts;
// pig exports it because Go callers need to type-assert on it after
// EmitBeforeAgentStart returns. Field semantics match upstream
// verbatim.
type BeforeAgentStartCombinedResult struct {
	// Messages is the slice of custom messages collected from every
	// handler that returned `result.message`. Empty when no handler
	// pushed a message.
	Messages []CustomMessageRef `json:"messages,omitempty"`

	// SystemPrompt is the final mutated system prompt, non-nil only if at
	// least one handler set `result.systemPrompt` (an empty prompt included).
	SystemPrompt *string `json:"systemPrompt,omitempty"`
}

// AttributedResourcePath pairs a resource path with the extension that
// declared it. Used by EmitResourcesDiscover to surface where each
// skill/prompt/theme path came from.
//
// upstream: anonymous type at runner.ts:944-947
//
//	skillPaths: Array<{ path: string; extensionPath: string }>;
//
// pig names this type because Go's anonymous-type composition is
// less ergonomic than TS's; same wire shape.
type AttributedResourcePath struct {
	Path          string `json:"path"`
	ExtensionPath string `json:"extensionPath"`
}

// ResourcesDiscoverAggregateResult is the combined output of all
// resources_discover handlers, with each path attributed to its source
// extension.
//
// upstream: anonymous return type at runner.ts:944-948
type ResourcesDiscoverAggregateResult struct {
	SkillPaths  []AttributedResourcePath `json:"skillPaths"`
	PromptPaths []AttributedResourcePath `json:"promptPaths"`
	ThemePaths  []AttributedResourcePath `json:"themePaths"`
}
