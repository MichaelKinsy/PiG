package extension

import (
	"encoding/json"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
)

// BuildSystemPromptOptions mirrors upstream
// `core/system-prompt.ts::BuildSystemPromptOptions`.
//
// Extensions receive this struct on every `before_agent_start` event
// via `event.systemPromptOptions` so they can inspect what pi has
// already assembled (custom prompt, tools, append text, context files,
// skills) without re-discovering resources.
//
// Extensions receive upstream's NormalizedBuildSystemPromptOptions
// (system-prompt.ts:37-46): every collection is present, so an empty
// selectedTools is `[]`, not an omitted key. MarshalJSON writes that shape;
// the struct tags describe only decoding.
//
// upstream: packages/coding-agent/src/core/system-prompt.ts:9-69
type BuildSystemPromptOptions struct {
	// CustomPrompt is the user-supplied prompt that replaces the default
	// (from --system-prompt, SYSTEM.md, or custom templates).
	CustomPrompt string `json:"customPrompt,omitempty"`
	// CustomPromptSet preserves a present empty customPrompt without changing the existing string field. A nonempty CustomPrompt is always present.
	CustomPromptSet bool `json:"-"`
	// ForceSystemPrompt is the exact full prompt a before_agent_start handler returned as systemPrompt; later handlers observe it (runner.ts:1346-1348). Nil is Pi's undefined; an empty string is a forced empty prompt.
	ForceSystemPrompt *string `json:"forceSystemPrompt,omitempty"`
	// SelectedTools is the list of tool names included in the prompt.
	// Defaults upstream to [read, bash, edit, write].
	SelectedTools []string `json:"selectedTools,omitzero"`
	// HiddenTools are the selected tools whose declarations requests leave out (`prepareLoadout` hidden declarations). They are
	// reachable only through another tool, so the tool list and rules leave them out too. The Session sets it before every
	// request; an edit by a handler does not change what the request carries.
	HiddenTools []string `json:"hiddenTools,omitzero"`
	// ToolSnippets maps tool name → one-line description used in the
	// "Available tools" section.
	ToolSnippets map[string]string `json:"toolSnippets,omitzero"`
	// ToolGuidelines contributes rules only while its tool is active.
	ToolGuidelines map[string][]string `json:"toolGuidelines,omitzero"`
	// PromptGuidelines holds bullet lines appended to the default
	// guidelines section.
	PromptGuidelines []string `json:"promptGuidelines,omitzero"`
	// AppendSystemPrompt is the joined text appended after the main
	// prompt body (from --append-system-prompt flags / settings).
	AppendSystemPrompt string `json:"appendSystemPrompt"`
	// Sections holds additional XML-wrapped sections in authored order. Event handlers mutate the per-run collection, not the Session's base options.
	Sections *ai.OrderedSections `json:"sections,omitempty"`
	// Cwd is the working directory shown to the LLM.
	Cwd string `json:"cwd"`
	// ContextFiles are pre-loaded AGENTS.md / CLAUDE.md files in the
	// order they appear in the prompt.
	ContextFiles []SystemPromptContextFile `json:"contextFiles,omitzero"`
	// Skills lists skills surfaced in the prompt's skills section.
	Skills []SystemPromptSkill `json:"skills,omitzero"`
}

// UnmarshalJSON retains customPrompt presence for command and event option round trips.
func (o *BuildSystemPromptOptions) UnmarshalJSON(data []byte) error {
	type fields BuildSystemPromptOptions
	var wire struct {
		fields
		CustomPrompt *string `json:"customPrompt"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*o = BuildSystemPromptOptions(wire.fields)
	o.CustomPromptSet = wire.CustomPrompt != nil
	if wire.CustomPrompt != nil {
		o.CustomPrompt = *wire.CustomPrompt
	}
	return nil
}

// NormalizeBuildSystemPromptOptions supplies the collection-complete shape exposed to extensions while copying the per-run mutable collections.
//
// Ports packages/coding-agent/src/core/system-prompt.ts
func NormalizeBuildSystemPromptOptions(input BuildSystemPromptOptions) BuildSystemPromptOptions {
	out := input
	if input.SelectedTools == nil {
		out.SelectedTools = []string{"read", "bash", "edit", "write"}
	} else {
		out.SelectedTools = append([]string{}, input.SelectedTools...)
	}
	out.HiddenTools = append([]string{}, input.HiddenTools...)
	out.ToolSnippets = make(map[string]string, len(input.ToolSnippets))
	maps.Copy(out.ToolSnippets, input.ToolSnippets)
	out.ToolGuidelines = make(map[string][]string, len(input.ToolGuidelines))
	for name, guidelines := range input.ToolGuidelines {
		out.ToolGuidelines[name] = append([]string{}, guidelines...)
	}
	out.PromptGuidelines = append([]string{}, input.PromptGuidelines...)
	sections := ai.OrderedSections{}
	if input.Sections != nil {
		sections = append(sections, (*input.Sections)...)
		for i := range sections {
			if sections[i].Value != nil {
				sections[i].Value = new(*sections[i].Value)
			}
		}
	}
	out.Sections = &sections
	out.ContextFiles = append([]SystemPromptContextFile{}, input.ContextFiles...)
	out.Skills = append([]SystemPromptSkill{}, input.Skills...)
	if input.ForceSystemPrompt != nil {
		out.ForceSystemPrompt = new(*input.ForceSystemPrompt)
	}
	return out
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
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	FilePath               string     `json:"filePath"`
	BaseDir                string     `json:"baseDir"`
	SourceInfo             SourceInfo `json:"sourceInfo,omitzero"`
	DisableModelInvocation bool       `json:"disableModelInvocation"`
}

// MarshalJSON writes the collection-complete shape that upstream's
// normalizeBuildSystemPromptOptions (system-prompt.ts:48-64) exposes to
// extensions: empty collections stay present, while customPrompt is omitted
// when it is undefined.
func (o BuildSystemPromptOptions) MarshalJSON() ([]byte, error) {
	var custom *string
	if o.CustomPromptSet || o.CustomPrompt != "" {
		custom = &o.CustomPrompt
	}
	sections := json.RawMessage("{}")
	if o.Sections != nil && len(*o.Sections) > 0 {
		encoded, err := json.Marshal(*o.Sections)
		if err != nil {
			return nil, err
		}
		sections = encoded
	}
	return json.Marshal(struct {
		CustomPrompt       *string                   `json:"customPrompt,omitempty"`
		ForceSystemPrompt  *string                   `json:"forceSystemPrompt,omitempty"`
		SelectedTools      []string                  `json:"selectedTools"`
		HiddenTools        []string                  `json:"hiddenTools"`
		ToolSnippets       map[string]string         `json:"toolSnippets"`
		ToolGuidelines     map[string][]string       `json:"toolGuidelines"`
		PromptGuidelines   []string                  `json:"promptGuidelines"`
		AppendSystemPrompt string                    `json:"appendSystemPrompt"`
		Sections           json.RawMessage           `json:"sections"`
		Cwd                string                    `json:"cwd"`
		ContextFiles       []SystemPromptContextFile `json:"contextFiles"`
		Skills             []SystemPromptSkill       `json:"skills"`
	}{
		CustomPrompt:       custom,
		ForceSystemPrompt:  o.ForceSystemPrompt,
		SelectedTools:      nonNil(o.SelectedTools),
		HiddenTools:        nonNil(o.HiddenTools),
		ToolSnippets:       nonNilMap(o.ToolSnippets),
		ToolGuidelines:     nonNilMap(o.ToolGuidelines),
		PromptGuidelines:   nonNil(o.PromptGuidelines),
		AppendSystemPrompt: o.AppendSystemPrompt,
		Sections:           sections,
		Cwd:                o.Cwd,
		ContextFiles:       nonNil(o.ContextFiles),
		Skills:             nonNil(o.Skills),
	})
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

func nonNilMap[K comparable, V any](values map[K]V) map[K]V {
	if values == nil {
		return map[K]V{}
	}
	return values
}
