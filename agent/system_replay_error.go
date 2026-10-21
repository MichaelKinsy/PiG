package agent

import (
	stdjson "encoding/json"
	"errors"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// iterationError is the end of V8's TypeError message for a for...of over a value that is not iterable.
const iterationError = " is not iterable (cannot read property Symbol(Symbol.iterator))"

// SystemReplayError returns the TypeError Pi's getCurrentSystemMessage throws while it replays messages, or nil. Pi parses a session without validation, so a system message read from a session can hold a content, toolsAdded or toolsRemoved member of a JSON type the replay cannot read. The replay first renders the content of every system message (pi-ai utils/transcript.ts getCurrentSystemMessage, utils/text.ts contentText) and then applies their tool lists (getCurrentTools), so a content error comes before any tool list error. Only a message decoded from such text can hold one; its retained source keeps the member as it was read.
func SystemReplayError(messages []AgentMessage) error {
	var systems []*orderedjson.Object
	for _, message := range messages {
		if message.System == nil || message.source == nil {
			continue
		}
		object, err := orderedjson.Parse(message.source.text)
		if err != nil {
			continue
		}
		systems = append(systems, object)
		if content, ok := object.Get("content"); ok {
			if err := contentTextError(content); err != nil {
				return err
			}
		}
	}
	for _, object := range systems {
		// getCurrentTools removes a message's tools before it adds its own.
		for _, member := range []string{"toolsRemoved", "toolsAdded"} {
			if tools, ok := object.Get(member); ok {
				if err := toolListError(tools); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ToolReplayError returns the TypeError Pi's _restoreToolsFromTranscript throws for the system messages of a whole branch, or nil. It first collects the tools the branch ever added or removed, `systemMessages.flatMap((message) => [...(message.toolsAdded ?? []), ...(message.toolsRemoved ?? [])]).map((tool) => tool.name)`, and then replays them with getCurrentTools; every member getCurrentTools cannot read fails in the first step, so only the first step's errors are reported. Unlike SystemReplayError it never reads a message's content. A spread of a value that is not iterable, in any message, throws before any element's name is read, a message's added list before its removed list; a string spreads into characters that have no name.
func ToolReplayError(messages []AgentMessage) error {
	var lists [][]byte
	for _, message := range messages {
		if message.System == nil || message.source == nil {
			continue
		}
		object, err := orderedjson.Parse(message.source.text)
		if err != nil {
			continue
		}
		for _, member := range []string{"toolsAdded", "toolsRemoved"} {
			tools, ok := object.Get(member)
			if !ok || tools[0] == 'n' {
				continue
			}
			switch tools[0] {
			case '"':
			case '[':
				lists = append(lists, tools)
			default:
				return errors.New("(message." + member + " ?? []) is not iterable")
			}
		}
	}
	// The names are read after every message's lists were spread.
	for _, list := range lists {
		if err := elementError(list, "name"); err != nil {
			return err
		}
	}
	return nil
}

// CurrentSystemMessageError returns the TypeError Pi's getCurrentSystemMessage throws while it replays the agent's history, or nil (see SystemReplayError). It reads the history under the lock a run appends under, without copying it.
func (a *Agent) CurrentSystemMessageError() error {
	a.messagesMu.RLock()
	defer a.messagesMu.RUnlock()
	return SystemReplayError(a.messages)
}

// contentTextError is the TypeError contentText throws for content: a string is returned as is, and anything else is filtered as an array of blocks whose type is read.
func contentTextError(content []byte) error {
	switch content[0] {
	case '"', 'n':
		// The session context replays a null or missing content as "" (session-manager.ts:444).
		return nil
	case '[':
		return elementError(content, "type")
	}
	return errors.New("content.filter is not a function")
}

// toolListError is the TypeError getCurrentTools throws for a toolsAdded or toolsRemoved member: a missing or null list is empty, a string iterates its characters, and every element's name is read.
func toolListError(tools []byte) error {
	switch tools[0] {
	case 'n', '"':
		return nil
	case '[':
		return elementError(tools, "name")
	case '{':
		return errors.New("object" + iterationError)
	case 't', 'f':
		return errors.New("boolean " + string(tools) + iterationError)
	}
	// The retained text writes a number as JSON.stringify does, which is String(number) for every finite number.
	return errors.New("number " + string(tools) + iterationError)
}

// elementError is the TypeError reading property of a null element of array throws; every other element has the property or reads it as undefined.
func elementError(array []byte, property string) error {
	var elements []stdjson.RawMessage
	if stdjson.Unmarshal(array, &elements) != nil {
		return nil
	}
	for _, element := range elements {
		if string(element) == "null" {
			return errors.New("Cannot read properties of null (reading '" + property + "')")
		}
	}
	return nil
}
