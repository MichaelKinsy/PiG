package subprocess

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// promptOptionsEventArgs preserves an untyped selection between handlers without changing the native event's value-typed public API.
func promptOptionsEventArgs(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	selected := extension.BeforeAgentStartSelectedTools(ctx)
	if selected == nil {
		return raw, nil
	}
	var event map[string]json.RawMessage
	if err := json.Unmarshal(raw, &event); err != nil {
		return nil, err
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(event["systemPromptOptions"], &options); err != nil {
		return nil, err
	}
	options["selectedTools"] = selected
	encoded, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	event["systemPromptOptions"] = encoded
	return json.Marshal(event)
}

// applyPromptOptionEdits writes the handler's edits to every options field except sections and selectedTools into the per-run options the next handler receives (runner.ts:1339 shares one object). Those two fields travel separately because their wire values are ordered or untyped. The request carries customPrompt and forceSystemPrompt whenever they are defined, so their absence from the returned object is a deletion and leaves them undefined, as a JavaScript `delete` does. Another field the handler left out keeps its value, and an absent object keeps them all.
func applyPromptOptionEdits(target *extension.BuildSystemPromptOptions, raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	delete(fields, "sections")
	delete(fields, "selectedTools")
	trimmed, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var edited extension.BuildSystemPromptOptions
	if err := json.Unmarshal(trimmed, &edited); err != nil {
		return err
	}
	target.CustomPrompt, target.CustomPromptSet = edited.CustomPrompt, edited.CustomPromptSet
	target.ForceSystemPrompt = edited.ForceSystemPrompt
	if _, ok := fields["toolSnippets"]; ok {
		target.ToolSnippets = edited.ToolSnippets
	}
	if _, ok := fields["toolGuidelines"]; ok {
		target.ToolGuidelines = edited.ToolGuidelines
	}
	if _, ok := fields["promptGuidelines"]; ok {
		target.PromptGuidelines = edited.PromptGuidelines
	}
	if _, ok := fields["appendSystemPrompt"]; ok {
		target.AppendSystemPrompt = edited.AppendSystemPrompt
	}
	if _, ok := fields["cwd"]; ok {
		target.Cwd = edited.Cwd
	}
	if _, ok := fields["contextFiles"]; ok {
		target.ContextFiles = edited.ContextFiles
	}
	if _, ok := fields["skills"]; ok {
		target.Skills = edited.Skills
	}
	return nil
}
