package extension

// BuildSystemPromptOptions mirrors upstream
// `core/system-prompt.ts::BuildSystemPromptOptions`.
//
// Extensions receive this struct on every `before_agent_start` event
// via `event.systemPromptOptions` so they can inspect what pi has
// already assembled (custom prompt, tools, append text, context files,
// skills) without re-discovering resources.
//
// All fields are nullable upstream; in Go we represent that with
// `omitempty` JSON tags and zero values. Wire shape (JSON keys) is
// preserved verbatim for cross-language extension parity.
//
// upstream: packages/coding-agent/src/core/system-prompt.ts:8-25
type BuildSystemPromptOptions struct {
	// CustomPrompt is the user-supplied prompt that replaces the default
	// (from --system-prompt, SYSTEM.md, or custom templates).
	CustomPrompt string `json:"customPrompt,omitempty"`
	// SelectedTools is the list of tool names included in the prompt.
	// Defaults upstream to [read, bash, edit, write].
	SelectedTools []string `json:"selectedTools,omitempty"`
	// ToolSnippets maps tool name → one-line description used in the
	// "Available tools" section.
	ToolSnippets map[string]string `json:"toolSnippets,omitempty"`
	// PromptGuidelines holds bullet lines appended to the default
	// guidelines section.
	PromptGuidelines []string `json:"promptGuidelines,omitempty"`
	// AppendSystemPrompt is the joined text appended after the main
	// prompt body (from --append-system-prompt flags / settings).
	AppendSystemPrompt string `json:"appendSystemPrompt,omitempty"`
	// Cwd is the working directory shown to the LLM.
	Cwd string `json:"cwd"`
	// ContextFiles are pre-loaded AGENTS.md / CLAUDE.md files in the
	// order they appear in the prompt.
	ContextFiles []SystemPromptContextFile `json:"contextFiles,omitempty"`
	// Skills lists skills surfaced in the prompt's skills section.
	Skills []SystemPromptSkill `json:"skills,omitempty"`
}

// SystemPromptContextFile mirrors the upstream anonymous
// `{ path: string; content: string }` element of contextFiles.
//
// upstream: packages/coding-agent/src/core/system-prompt.ts:22
type SystemPromptContextFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SystemPromptSkill mirrors the upstream `Skill` interface fields that
// extensions can rely on. Extensions inspect skill metadata; the
// physical SkillFrontmatter is not exposed.
//
// upstream: packages/coding-agent/src/core/skills.ts:74-82 Skill
type SystemPromptSkill struct {
	Name                   string `json:"name"`
	Description            string `json:"description"`
	FilePath               string `json:"filePath"`
	BaseDir                string `json:"baseDir,omitempty"`
	DisableModelInvocation bool   `json:"disableModelInvocation,omitempty"`
}
