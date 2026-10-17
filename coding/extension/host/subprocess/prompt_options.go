package subprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// renderSystemPromptCall is runner.ts:1426 renderCurrentSystemPrompt for an extension process. A before_agent_start
// handler's event.systemPrompt and ctx.getSystemPrompt() render the options when read, and the Node runtime holds the
// options the handler edits, so it sends them here once they differ from the options the event's prompt was rendered
// for. An invalid custom section is the getter's error, as Pi's builder throws.
func renderSystemPromptCall(args json.RawMessage) (*CallResultPayload, error) {
	var p struct {
		Options json.RawMessage `json:"options"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("parse renderSystemPrompt args: %w", err)
	}
	var options extension.BuildSystemPromptOptions
	if err := json.Unmarshal(p.Options, &options); err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Message: err.Error()}}, nil
	}
	prompt, err := inproc.RenderSystemPrompt(extension.NormalizeBuildSystemPromptOptions(options))
	if err != nil {
		return &CallResultPayload{Error: &ErrorInfo{Message: err.Error()}}, nil
	}
	//portlint:allow mapkeyorder a one-member object {prompt} has a single member order; it carries runner.ts:1426 renderCurrentSystemPrompt's string to the Node runtime
	result, err := json.Marshal(map[string]string{"prompt": prompt})
	return &CallResultPayload{Result: result}, err
}

// promptOptionsEventArgs preserves an untyped selection between handlers without changing the native event's value-typed public API.
func promptOptionsEventArgs(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	selected := extension.BeforeAgentStartSelectedTools(ctx)
	if selected == nil {
		return raw, nil
	}
	var options json.RawMessage
	members, err := objectMembers(raw)
	if err != nil {
		return nil, err
	}
	for _, member := range members {
		if member.key == "systemPromptOptions" {
			options = member.value
		}
	}
	optionMembers, err := objectMembers(options)
	if err != nil {
		return nil, err
	}
	for i := range optionMembers {
		if optionMembers[i].key == "selectedTools" {
			optionMembers[i].value = selected
		}
	}
	encoded, err := encodeObjectMembers(optionMembers)
	if err != nil {
		return nil, err
	}
	for i := range members {
		if members[i].key == "systemPromptOptions" {
			members[i].value = encoded
		}
	}
	return encodeObjectMembers(members)
}

type objectMember struct {
	key   string
	value json.RawMessage
}

// objectMembers reads a JSON object's members in document order; the extension sees the event's members in the order Pi builds them.
func objectMembers(raw json.RawMessage) ([]objectMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var members []objectMember
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		members = append(members, objectMember{key.(string), value})
	}
	return members, nil
}

func encodeObjectMembers(members []objectMember) (json.RawMessage, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, member := range members {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(member.key)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(member.value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
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
