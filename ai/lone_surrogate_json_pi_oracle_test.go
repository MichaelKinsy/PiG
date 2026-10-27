package ai_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A message that holds a lone UTF-16 surrogate (a Go string holds it as WTF-8) must come out of JSON as Pi's does:
// JSON.stringify writes the unit as the escape `\ud83d` and leaves `<`, `>` and `&` as they are, and JSON.parse of that text
// gives the unit back. Each document below is a message written with a placeholder where the surrogate goes, then given
// the escape; the oracle is Node's JSON.stringify(JSON.parse(document)). Decoding the document into the message and
// encoding it again must give the oracle's text, byte for byte, with the surrogate in every string the message holds.

const surrogate = `\ud83d`

func loneSurrogateDocuments(t *testing.T) map[string]string {
	t.Helper()
	const mark = "§"
	text := mark + "<>&" + mark
	toolResult := ai.ToolResultMessage{ToolCallID: "call" + mark, ToolName: "tool" + mark,
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
		Details: map[string]any{"preview" + mark: text, "nested": []any{text, map[string]any{"k" + mark: text}}, "number": float64(1)}}
	user := ai.UserMessage{Content: ai.UserText(text)}
	assistant := ai.AssistantMessage{API: "faux", Provider: "p" + mark, Model: "m" + mark,
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}, ai.ThinkingContent{Thinking: text},
			ai.ToolCall{ID: "id" + mark, Name: "name" + mark, Arguments: ai.JsonObject{"arg" + mark: text, "list": []any{text}}}}}
	docs := map[string]string{}
	_ = user
	for name, message := range map[string]any{"tool result": toolResult, "assistant": assistant} {
		encoded, err := sdkjson.MarshalStringify(message)
		if err != nil {
			t.Fatal(err)
		}
		docs[name] = strings.ReplaceAll(string(encoded), mark, surrogate)
	}
	return docs
}

func TestMessagesKeepALoneSurrogateAsPiDoes(t *testing.T) {
	docs := loneSurrogateDocuments(t)
	names := make([]string, 0, len(docs))
	list := make([]string, 0, len(docs))
	for name, doc := range docs {
		names = append(names, name)
		list = append(list, doc)
	}
	input, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/stringify_roundtrip_oracle.mjs")
	cmd.Dir = testenv.PackageDir(t)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi's JSON: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil || len(expected) != len(list) {
		t.Fatalf("oracle output: %v", err)
	}
	for i, name := range names {
		var decoded any
		switch name {
		case "tool result":
			decoded = new(ai.ToolResultMessage)
		case "user":
			continue
		default:
			decoded = new(ai.AssistantMessage)
		}
		if err := sdkjson.Unmarshal([]byte(list[i]), decoded); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		got, err := sdkjson.MarshalStringify(decoded)
		if err != nil {
			t.Fatalf("%s: encode: %v", name, err)
		}
		if string(got) != expected[i] {
			t.Errorf("%s: after decoding and encoding\n got  %s\n Pi's %s", name, got, expected[i])
		}
	}
}
