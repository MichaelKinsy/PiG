package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// pi: packages/coding-agent/src/core/session-manager.ts:1188 (a session line is JSON.stringify of the entry), packages/agent/src/agent-loop.ts
// declareToolChanges (the system message that adds and removes tools)

// A stored system message is read and written again as the line it was: a session Pi wrote holds a lone UTF-16 surrogate of a tool as its
// escape, which is kept for the next request; a session a PiG before this test wrote holds U+FFFD there, which stays U+FFFD: nothing guesses
// the unit back.
func TestStoredSystemMessageKeepsAToolsLoneSurrogate(t *testing.T) {
	for name, line := range map[string]string{
		"written by Pi": `{"role":"system","content":"","timestamp":2,"toolsAdded":[{"name":"N[\ud83d]","description":"D[\ude00]","parameters":{"type":"object","properties":{"K[\ude00\ud83d]":{"type":"string","description":"PD[\ud83d]","enum":["E[\ude00]"],"default":"DF[\ud83d]"}},"required":["K[\ude00\ud83d]"]}},` +
			`{"name":"N[\ude00]","description":"D[\ud83d]","parameters":{"type":"object","properties":{}}}],"toolsRemoved":[{"name":"gone[\ude00]"}]}`,
		"written with U+FFFD": "{\"role\":\"system\",\"content\":\"\",\"timestamp\":2,\"toolsAdded\":[{\"name\":\"N[\uFFFD\uFFFD\uFFFD]\",\"description\":\"D[\uFFFD\uFFFD\uFFFD]\",\"parameters\":{\"type\":\"object\",\"properties\":{\"K[\uFFFD\uFFFD]\":{\"type\":\"string\",\"description\":\"PD[\uFFFD]\"}},\"required\":[\"K[\uFFFD\uFFFD]\"]}}],\"toolsRemoved\":[{\"name\":\"gone[\uFFFD]\"}]}",
	} {
		t.Run(name, func(t *testing.T) {
			message, err := decodeSystemMessage([]byte(line))
			if err != nil {
				t.Fatal(err)
			}
			if name == "written by Pi" && (len(message.ToolsAdded) != 2 || message.ToolsAdded[0].Name == message.ToolsAdded[1].Name) {
				t.Fatalf("two tools whose names differ only in the lone unit were read as %q and %q", message.ToolsAdded[0].Name, message.ToolsAdded[len(message.ToolsAdded)-1].Name)
			}
			written, err := jsstring.MarshalJSON(*message)
			if err != nil {
				t.Fatal(err)
			}
			if string(written) != line {
				t.Errorf("written again as\n%s\nwant\n%s", written, line)
			}
		})
	}
}
