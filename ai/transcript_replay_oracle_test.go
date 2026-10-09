package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/ai/src/utils/transcript.ts

// The replay helpers of transcript.ts (getCurrentSystemMessage, getCurrentSystemPrompt, getCurrentTools, getDeclaredTools, hasToolRedefinitions,
// hasNonAdditiveToolChanges, getInitialSystemMessage, withoutInitialSystemMessage, collapseSystemMessages) run in the pinned pi-ai and in PiG over
// generated histories decoded from the same JSON: system messages with text or block content, sections (null removes one; names include
// integer-like ones, which a JavaScript object orders first), tool additions and removals, and user messages between them.
func TestTranscriptReplayMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	names := []string{"a", "b", "git", "10", "2", "1", "007", "-1", "4294967295", "4294967294", "x y", "é"}
	toolNames := []string{"read", "write", "bash", "10", "2", "edit"}
	texts := []string{"", "alpha", "beta \u2028 <&>", "line1\nline2", "😀", "x"}
	pick := func(items []string) string { return items[rng.Intn(len(items))] }
	jsonString := func(s string) string { encoded, _ := json.Marshal(s); return string(encoded) }
	var histories []json.RawMessage
	var goHistories [][]Message
	for range 1500 {
		var messages []string
		var goMessages []Message
		for range rng.Intn(6) {
			if rng.Intn(4) == 0 {
				text, stamp := pick(texts), int64(rng.Intn(1000))
				messages = append(messages, fmt.Sprintf(`{"role":"user","content":%s,"timestamp":%d}`, jsonString(text), stamp))
				goMessages = append(goMessages, UserMessage{Content: UserText(text), Timestamp: stamp})
				continue
			}
			var members []string
			var system SystemMessage
			if rng.Intn(5) == 0 {
				first, second := pick(texts), pick(texts)
				members = append(members, fmt.Sprintf(`"content":[{"type":"text","text":%s},{"type":"text","text":%s}]`, jsonString(first), jsonString(second)))
				system.Content = SystemTextBlocks{{Text: first}, {Text: second}}
			} else {
				text := pick(texts)
				members = append(members, `"content":`+jsonString(text))
				system.Content = SystemText(text)
			}
			if rng.Intn(2) == 0 {
				var sections []string
				used := map[string]bool{}
				for range 1 + rng.Intn(4) {
					name := pick(names)
					if used[name] {
						continue
					}
					used[name] = true
					value := "null"
					section := PromptSection{Name: name}
					if rng.Intn(4) != 0 {
						text := pick(texts)
						value, section.Value = jsonString(text), &text
					}
					sections = append(sections, jsonString(name)+":"+value)
					system.Sections = append(system.Sections, section)
				}
				members = append(members, `"sections":{`+strings.Join(sections, ",")+`}`)
				// A JavaScript object lists integer-like names first; the Go side is given the order such an object would have.
				names := make([]string, len(system.Sections))
				for i, section := range system.Sections {
					names[i] = section.Name
				}
				ordered := make(OrderedSections, len(names))
				for i, index := range jsObjectKeyOrder(names) {
					ordered[i] = system.Sections[index]
				}
				system.Sections = ordered
			}
			tool := func() (string, ToolSchema) {
				name, description, property := pick(toolNames), pick(texts), pick(names)
				var schema ToolSchema
				schemaJSON := fmt.Sprintf(`{"name":%s,"description":%s,"parameters":{"type":"object","properties":{%s:{"type":"string"}},"required":[]}}`, jsonString(name), jsonString(description), jsonString(property))
				if err := json.Unmarshal([]byte(schemaJSON), &schema); err != nil {
					t.Fatal(err)
				}
				return schemaJSON, schema
			}
			if rng.Intn(2) == 0 {
				var added []string
				for range 1 + rng.Intn(3) {
					text, schema := tool()
					added = append(added, text)
					system.ToolsAdded = append(system.ToolsAdded, schema)
				}
				members = append(members, `"toolsAdded":[`+strings.Join(added, ",")+`]`)
			}
			if rng.Intn(3) == 0 {
				var removed []string
				for range 1 + rng.Intn(2) {
					name := pick(toolNames)
					removed = append(removed, fmt.Sprintf(`{"name":%s}`, jsonString(name)))
					system.ToolsRemoved = append(system.ToolsRemoved, ToolReference{Name: name})
				}
				members = append(members, `"toolsRemoved":[`+strings.Join(removed, ",")+`]`)
			}
			system.Timestamp = int64(1 + rng.Intn(1000))
			messages = append(messages, `{"role":"system",`+strings.Join(members, ",")+fmt.Sprintf(`,"timestamp":%d}`, system.Timestamp))
			goMessages = append(goMessages, system)
		}
		histories = append(histories, json.RawMessage("["+strings.Join(messages, ",")+"]"))
		goHistories = append(goHistories, goMessages)
	}
	input, err := json.Marshal(histories)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/transcript_replay.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []map[string]json.RawMessage
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, history := range histories {
		messages := goHistories[i]
		encode := func(value any) string {
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			return string(encoded)
		}
		orNull := func(message *SystemMessage) any {
			if message == nil {
				return nil
			}
			return withRole(*message)
		}
		tools := GetCurrentTools(messages)
		declared := GetDeclaredTools(messages)
		if tools == nil {
			tools = []ToolSchema{}
		}
		if declared == nil {
			declared = []ToolSchema{}
		}
		got := map[string]string{
			"current":        encode(orNull(GetCurrentSystemMessage(messages))),
			"prompt":         encode(GetCurrentSystemPrompt(messages)),
			"tools":          encode(tools),
			"declared":       encode(declared),
			"redefinitions":  encode(HasToolRedefinitions(messages)),
			"nonAdditive":    encode(HasNonAdditiveToolChanges(messages)),
			"initial":        encode(orNull(GetInitialSystemMessage(messages))),
			"withoutInitial": encode(nonNil(WithoutInitialSystemMessage(messages))),
			"collapsed":      encode(nonNil(CollapseSystemMessages(newTranscriptContext(messages)).Messages())),
		}
		for key, value := range got {
			if !sameReplay(value, string(want[i][key])) {
				if failures++; failures <= 8 {
					t.Errorf("history %d %s:\n history %s\n PiG %s\n Pi  %s", i, key, history, value, want[i][key])
				}
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d differences from Pi", failures)
	}
}

// withRole is a system message with its role member, as it appears in a transcript.
func withRole(message SystemMessage) any {
	return struct {
		Role string `json:"role"`
		SystemMessage
	}{"system", message}
}

func nonNil(messages []Message) []Message {
	if messages == nil {
		return []Message{}
	}
	return messages
}

// sameReplay compares two JSON texts as values, and the order of the members of every "sections" object, which carries the order of the prompt. The
// member order of the other objects and the escaping of <, & and U+2028 are the writers' own.
func sameReplay(got, want string) bool {
	var gotValue, wantValue any
	if json.Unmarshal([]byte(got), &gotValue) != nil || json.Unmarshal([]byte(want), &wantValue) != nil {
		return false
	}
	return reflect.DeepEqual(gotValue, wantValue) && sectionOrders(got) == sectionOrders(want)
}

// sectionOrders lists the member names of every object that is the value of a "sections" member, in document order.
func sectionOrders(text string) string {
	decoder := json.NewDecoder(strings.NewReader(text))
	var out []string
	var walk func() bool
	walk = func() bool {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return true
		}
		switch delim {
		case '[':
			for decoder.More() {
				if !walk() {
					return false
				}
			}
		case '{':
			for decoder.More() {
				keyToken, _ := decoder.Token()
				key, _ := keyToken.(string)
				if key == "sections" {
					var raw json.RawMessage
					if decoder.Decode(&raw) != nil {
						return false
					}
					inner := json.NewDecoder(bytes.NewReader(raw))
					_, _ = inner.Token()
					var names []string
					for inner.More() {
						nameToken, _ := inner.Token()
						names = append(names, fmt.Sprint(nameToken))
						var skip json.RawMessage
						_ = inner.Decode(&skip)
					}
					out = append(out, strings.Join(names, ","))
					continue
				}
				if !walk() {
					return false
				}
			}
		}
		_, err = decoder.Token()
		return err == nil
	}
	walk()
	return strings.Join(out, "|")
}
