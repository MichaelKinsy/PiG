// Package models is p2's models plugin: provider access, the configuration files' observer and the default model.
package models

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/fake.ts: the fake provider of demo and test mode (P2_FAKE, `fake: true`): prompts that make
// the faux model call a tool (scripted and the `<tool> <json>` forms), one-shot agents' sample answers, and echoes
// otherwise. Responses depend on committed input, not a volatile queue position, so reopening an interrupted request is
// deterministic.

func call(name string, args map[string]any) ai.FauxContentBlock {
	return ai.FauxToolCall(name, args, nil)
}

// scripted are the fake-mode prompts that make the faux model call one tool; anything else is echoed.
var scripted = map[string]func() ai.FauxContentBlock{
	"tool": func() ai.FauxContentBlock { return call("read", map[string]any{"path": "fixture.txt"}) },
	"write": func() ai.FauxContentBlock {
		return call("write", map[string]any{"path": "created.txt", "content": "before\n"})
	},
	"edit": func() ai.FauxContentBlock {
		return call("edit", map[string]any{"path": "created.txt", "edits": []any{map[string]any{"oldText": "before", "newText": "after"}}})
	},
	"bash":      func() ai.FauxContentBlock { return call("bash", map[string]any{"command": "pwd"}) },
	"send-file": func() ai.FauxContentBlock { return call("send_file", map[string]any{"path": "created.txt"}) },
	"artifact": func() ai.FauxContentBlock {
		return call("artifact", map[string]any{
			"name": "demo", "title": "Demo artifact", "type": "html",
			"content": "<!doctype html><title>Demo</title><h1>Artifact demo</h1>",
		})
	},
	"subagent": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{
			"action": "spawn", "name": "helper", "message": "hello helper", "thinkingLevel": "high", "tools": []any{"read"},
		})
	},
	"subagent-configure": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{
			"action": "configure", "name": "helper", "model": "faux/faux-1", "thinkingLevel": "high", "tools": []any{"bash", "artifact"},
		})
	},
	"subagent-second": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{"action": "spawn", "name": "second", "message": "hello second", "tools": []any{"read"}})
	},
	"subagent-answer": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{"action": "answer", "name": "helper"})
	},
	"subagent-send": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{"action": "send", "name": "helper", "message": "hello again"})
	},
	"subagent-archive": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{"action": "archive", "name": "helper"})
	},
	"subagent-status": func() ai.FauxContentBlock { return call("subagent", map[string]any{"action": "status"}) },
	"subagent-bad": func() ai.FauxContentBlock {
		return call("subagent", map[string]any{"action": "spawn", "name": "bad", "message": "x", "model": "nope/missing"})
	},
	// Code mode: a script whose nested calls are two tools (codemode plugin).
	"codemode": func() ai.FauxContentBlock {
		return call("codemode", map[string]any{"code": strings.Join([]string{
			`const written = await tools.bash({ command: "echo first >> codemode.log; printf written" }).then((result) => result.output);`,
			`const read = await tools.read({ path: "codemode.log" });`,
			`text("bash: " + written);`,
			"return read;",
		}, "\n")})
	},
	// Code mode across a worker restart: the slow middle call is interrupted, the script catches it and continues.
	"codemode-restart": func() ai.FauxContentBlock {
		return call("codemode", map[string]any{"code": strings.Join([]string{
			`const one = (await tools.bash({ command: "echo one >> codemode.log; printf one" })).output;`,
			"let two;",
			`try { two = (await tools.bash({ command: "sleep 5; echo two >> codemode.log; printf two" })).output; }`,
			`catch (error) { two = "two failed: " + error.message; }`,
			`const three = (await tools.bash({ command: "echo three >> codemode.log; printf three" })).output;`,
			`return [one, two, three].join(" | ");`,
		}, "\n")})
	},
	"slow-tool": func() ai.FauxContentBlock {
		return call("bash", map[string]any{"command": "printf tool-running; sleep 4; printf tool-finished"})
	},
	// A tool call that lasts until the run is stopped: a test that needs the run still going, with no clock for the machine's load to beat.
	"hold-tool": func() ai.FauxContentBlock {
		return call("bash", map[string]any{"command": "sleep 3600"})
	},
}

// oneShotSchemaMarker ends a one-shot agent's instructions (plugins/agent): its schema follows on the same line.
const oneShotSchemaMarker = "matching this JSON Schema, without prose or code fences:\n"

// userText is the text of a user message, without the sender block in front.
func userText(message ai.Message) string {
	user, ok := message.(ai.UserMessage)
	if !ok {
		return ""
	}
	switch content := user.Content.(type) {
	case ai.UserText:
		return string(content)
	case ai.UserContentBlocks:
		blocks := []ai.UserContentBlock(content)
		if len(blocks) > 0 {
			if first, ok := blocks[0].(ai.TextContent); ok && strings.HasPrefix(first.Text, runtime.SenderPrefix) {
				blocks = blocks[1:]
			}
		}
		var parts []string
		for _, block := range blocks {
			if text, ok := block.(ai.TextContent); ok {
				parts = append(parts, text.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// oneShotSchema is a one-shot agent's answer schema, from its conversation's system messages.
func oneShotSchema(messages []ai.Message) (schema any, ok bool) {
	for _, message := range messages {
		system, isSystem := message.(ai.SystemMessage)
		if !isSystem {
			continue
		}
		var texts []string
		switch content := system.Content.(type) {
		case ai.SystemText:
			texts = append(texts, string(content))
		case ai.SystemTextBlocks:
			var joined strings.Builder
			for _, part := range content {
				joined.WriteString(part.Text)
			}
			texts = append(texts, joined.String())
		}
		for _, section := range system.Sections {
			if section.Value != nil && *section.Value != "" {
				texts = append(texts, *section.Value)
			}
		}
		for _, text := range texts {
			at := strings.Index(text, oneShotSchemaMarker)
			if at < 0 {
				continue
			}
			line, _, _ := strings.Cut(text[at+len(oneShotSchemaMarker):], "\n")
			value, err := chordjson.Decode([]byte(line))
			if err != nil {
				panic("the one-shot agent's schema is not JSON: " + err.Error())
			}
			return value, true
		}
	}
	return nil, false
}

// sampleOf is a small value matching a JSON Schema: the first enum value, every property, one array item, empty strings
// and minimums. Enough for code mode workflow scripts to run end to end in fake mode (docs/workflows.md).
func sampleOf(schema any) any {
	spec, ok := schema.(*chordjson.Object)
	if !ok {
		return nil
	}
	if enum, ok := spec.Value("enum").([]any); ok {
		if len(enum) == 0 {
			return nil
		}
		return enum[0]
	}
	if spec.Has("const") {
		return spec.Value("const")
	}
	switch spec.Value("type") {
	case "object":
		out := chordjson.NewObject(0)
		if properties, ok := spec.Value("properties").(*chordjson.Object); ok {
			for key, value := range properties.All() {
				out.Set(key, sampleOf(value))
			}
		}
		return out
	case "array":
		if limit, ok := spec.Value("maxItems").(float64); ok && limit == 0 {
			return []any{}
		}
		return []any{sampleOf(spec.Value("items"))}
	case "string":
		if minimum, ok := spec.Value("minLength").(float64); ok {
			return strings.Repeat("x", int(minimum))
		}
		return ""
	case "integer", "number":
		if minimum, ok := spec.Value("minimum").(float64); ok {
			return minimum
		}
		return float64(0)
	case "boolean":
		return false
	}
	return nil
}

// fauxToolPrefixed are the prompts `<tool> <json>` that call that tool with the JSON as its arguments.
func fauxToolPrefixed(text string) (name string, ok bool) {
	name, _, found := strings.Cut(text, " ")
	if !found {
		return "", false
	}
	switch name {
	case "artifact", "view", "history_search", "history_read", "history_handoff", "location_envs", "location_dirs", "location_move", "tool_search", "terminal":
		return name, true
	}
	if rest, has := strings.CutPrefix(name, "mcp__"); has && rest != "" && strings.IndexFunc(rest, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return name, true
	}
	return "", false
}

// jsonArgs parses the JSON after a prompt's tool name as the call's arguments.
func jsonArgs(text string) map[string]any {
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		panic("the prompt's arguments are not JSON: " + err.Error())
	}
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil
	}
	args := make(map[string]any, object.Len())
	for key, member := range object.All() {
		args[key] = member
	}
	return args
}

// scriptedCall is the tool call the prompt asks for, if any.
func scriptedCall(text string) (ai.FauxContentBlock, bool) {
	if build, ok := scripted[text]; ok {
		return build(), true
	}
	switch {
	case strings.HasPrefix(text, "conversations "):
		return call("conversations", jsonArgs(text[len("conversations "):])), true
	case strings.HasPrefix(text, "rename-session "):
		return call("rename_session", map[string]any{"title": text[len("rename-session "):]}), true
	case strings.HasPrefix(text, "ports "):
		return call("ports", jsonArgs(text[len("ports "):])), true
	// `codemode <script>` runs that script (the bare `codemode` above is a fixed one).
	case strings.HasPrefix(text, "codemode "):
		return call("codemode", map[string]any{"code": text[len("codemode "):]}), true
	case strings.HasPrefix(text, "subagent "):
		return call("subagent", jsonArgs(text[len("subagent "):])), true
	case strings.HasPrefix(text, "team "):
		return call("team", jsonArgs(text[len("team "):])), true
	}
	if name, ok := fauxToolPrefixed(text); ok {
		return call(name, jsonArgs(text[len(name)+1:])), true
	}
	return ai.FauxContentBlock{}, false
}

// toolResults counts the tool results after the last user message.
func toolResults(messages []ai.Message) int {
	asked := -1
	for i, message := range messages {
		if message.MessageRole() == "user" {
			asked = i
		}
	}
	results := 0
	for _, message := range messages[asked+1:] {
		if message.MessageRole() == "toolResult" {
			results++
		}
	}
	return results
}

func toolUse(content ...ai.FauxContentBlock) ai.AssistantMessage {
	return ai.FauxAssistantMessage(ai.FauxContentBlocks(content), ai.FauxAssistantMessageOptions{StopReason: string(ai.StopReasonToolUse)})
}

func firstUserText(messages []ai.Message) string {
	for _, message := range messages {
		if message.MessageRole() == "user" {
			return userText(message)
		}
	}
	return ""
}

// fakeResponse is the scripted answer to the transcript (the factory of createFakeModels).
func fakeResponse(transcript ai.TranscriptContext) ai.AssistantMessage {
	messages := transcript.Messages()
	var text string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].MessageRole() == "user" {
			text = userText(messages[i])
			break
		}
	}
	// A one-shot agent answers a small value matching its schema, unless its task is a `json …` or `slow…` test prompt
	// (exact answers, retries with non-JSON echoes, a long stream to stop).
	if schema, ok := oneShotSchema(messages); ok {
		if task := firstUserText(messages); !strings.HasPrefix(task, "json ") && !strings.HasPrefix(task, "slow") {
			sample, err := chordjson.Marshal(sampleOf(schema))
			if err != nil {
				panic("the one-shot sample is not JSON: " + err.Error())
			}
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText(string(sample))}, ai.FauxAssistantMessageOptions{})
		}
	}
	// `steps`: a turn as real models make them: thinking and a call, words and another call, thinking and the answer (the
	// transcript's stability tests).
	if text == "steps" {
		switch toolResults(messages) {
		case 0:
			return toolUse(ai.FauxThinking("Planning the steps. "+strings.Repeat("Looking at the files first. ", 12)), call("read", map[string]any{"path": "fixture.txt"}))
		case 1:
			return toolUse(ai.FauxText("The first read is done. "+strings.Repeat("Reading it once more to be sure. ", 6)), call("read", map[string]any{"path": "fixture.txt"}))
		}
		return ai.FauxAssistantMessage(ai.FauxContentBlocks{
			ai.FauxThinking("Wrapping up. " + strings.Repeat("Both reads agree. ", 12)),
			ai.FauxText("Echo: steps. " + strings.Repeat("Both reads show the same text, so the file is stable. ", 8)),
		}, ai.FauxAssistantMessageOptions{})
	}
	// `inline-steps`: a call, an inline artifact, another call, the answer (the artifact shows in place, between two
	// wheels).
	if text == "inline-steps" {
		steps := []func() ai.FauxContentBlock{
			func() ai.FauxContentBlock { return call("read", map[string]any{"path": "fixture.txt"}) },
			func() ai.FauxContentBlock {
				return call("artifact", map[string]any{
					"name": "steps", "title": "Steps", "type": "markdown",
					"content": "# Between the calls\n\nShown where it was made.", "inline": true,
				})
			},
			func() ai.FauxContentBlock { return call("read", map[string]any{"path": "fixture.txt"}) },
		}
		if results := toolResults(messages); results < len(steps) {
			return toolUse(steps[results]())
		}
		return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("Echo: inline-steps. The artifact is above.")}, ai.FauxAssistantMessageOptions{})
	}
	// A section that changed during the run (tools loaded by `tool_search`) adds a system message after the result.
	var last ai.Message
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].MessageRole() != "system" {
			last = messages[i]
			break
		}
	}
	if last == nil || last.MessageRole() != "toolResult" {
		if scriptedTool, ok := scriptedCall(text); ok {
			words := "Running " + scriptedTool.Name + "."
			if text == "tool" {
				words = "Reading fixture."
			}
			return toolUse(ai.FauxText(words), scriptedTool)
		}
	}
	answer := "Echo: " + text
	switch {
	// `json <text>` answers exactly `<text>` (code mode's one-shot agents parse it).
	case strings.HasPrefix(text, "json "):
		answer = text[len("json "):]
	case strings.HasPrefix(text, "slow"):
		answer = "Echo: " + text + " " + strings.Repeat("streaming ", 300)
	}
	return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxThinking("private-thinking-sentinel"), ai.FauxText(answer)}, ai.FauxAssistantMessageOptions{})
}

// requestLog appends each request's messages to the file P2_FAKE_REQUESTS names, as one JSON line (tests set it through
// the worker's environment).
type requestLog struct {
	mu   sync.Mutex
	path string
}

type loggedPart struct {
	Type     string `json:"type"`
	MimeType string `json:"mimeType"`
}

type loggedMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// reduceContent is a message's content as logged: an image keeps only its type and MIME type.
func reduceContent(raw json.RawMessage) (json.RawMessage, error) {
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return raw, nil // a string
	}
	for i, part := range parts {
		var head struct{ Type, MimeType string }
		if json.Unmarshal(part, &head) == nil && head.Type == "image" {
			reduced, err := json.Marshal(loggedPart{Type: "image", MimeType: head.MimeType})
			if err != nil {
				return nil, err
			}
			parts[i] = reduced
		}
	}
	if parts == nil {
		parts = []json.RawMessage{}
	}
	return json.Marshal(parts)
}

func (l *requestLog) append(messages []ai.Message) error {
	logged := make([]loggedMessage, len(messages))
	for i, message := range messages {
		raw, err := json.Marshal(message)
		if err != nil {
			return err
		}
		var content struct {
			Content json.RawMessage `json:"content"`
		}
		//perf:ok the fake provider logs a request for the tests only
		if err := json.Unmarshal(raw, &content); err != nil { //portlint:allow jsonclone a typed value converted from its stored JSON form, cold (see the perf:ok above)
			return err
		}
		reduced, err := reduceContent(content.Content)
		if err != nil {
			return err
		}
		logged[i] = loggedMessage{Role: message.MessageRole(), Content: reduced}
	}
	line, err := json.Marshal(logged)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return err
	}
	_, err = file.Write(append(line, '\n'))
	return errorsJoin(err, file.Close())
}

func errorsJoin(first, second error) error {
	if first != nil {
		return first
	}
	return second
}

// CreateFakeModels is the models of fake mode: the scripted faux provider over a fresh collection.
func CreateFakeModels(options ai.CreateModelsOptions) *ai.Models {
	var requests *requestLog
	if path := os.Getenv("P2_FAKE_REQUESTS"); path != "" {
		requests = &requestLog{path: path}
	}
	models := ai.CreateModels(options)
	tokens := 4
	faux := ai.NewFauxProvider(ai.FauxConfig{API: "p2-faux", TokensPerSecond: 200, TokenSize: &ai.FauxTokenSize{Min: &tokens, Max: &tokens}})
	provider := *faux.Provider()
	stream := func(ctx context.Context, _ *ai.Model, transcript ai.TranscriptContext, streamOptions ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		if requests != nil {
			if err := requests.append(transcript.Messages()); err != nil {
				return nil, err
			}
		}
		faux.AppendResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
			return fakeResponse(transcript), nil
		})})
		return faux.Stream(ctx, transcript, streamOptions)
	}
	provider.Stream, provider.StreamSimple = stream, stream
	models.SetProvider(&provider)
	return models
}
