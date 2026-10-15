package ai

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/ai/src/utils/text.ts getSystemMessageText and renderSystemMessageUpdate against pinned Pi over random system messages decoded from JSON: text or block content, sections named with integer-like, duplicate, empty and unicode keys, null and empty values.
func TestSystemMessageTextMatchesPi(t *testing.T) {
	docs := systemMessageDocs()
	var want struct {
		Text   []string `json:"text"`
		Update []string `json:"update"`
	}
	pioracle.Run(t, `
const mod = await load("pi-ai/utils/text.js");
const messages = input.map((doc) => JSON.parse(doc));
emit({ text: messages.map((m) => mod.getSystemMessageText(m)), update: messages.map((m) => mod.renderSystemMessageUpdate(m)) });`, docs, &want)
	failures := 0
	for i, doc := range docs {
		message := systemMessageFromDoc(t, doc)
		if got := GetSystemMessageText(message); got != want.Text[i] {
			if failures++; failures <= 8 {
				t.Errorf("GetSystemMessageText(%s) = %q, Pi %q", doc, got, want.Text[i])
			}
		}
		if got := RenderSystemMessageUpdate(message); got != want.Update[i] {
			if failures++; failures <= 8 {
				t.Errorf("RenderSystemMessageUpdate(%s) = %q, Pi %q", doc, got, want.Update[i])
			}
		}
	}
	if failures > 8 {
		t.Errorf("%d results differ from Pi", failures)
	}
}

// TestSystemMessageDocsDump prints the corpus for the Pi side of the providers-faux-streaming/32 parity scenario.
func TestSystemMessageDocsDump(t *testing.T) {
	line, err := json.Marshal(systemMessageDocs())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("system-message-probes:%s\n", line)
}

// TestSystemMessageDocsParity prints PiG's getSystemMessageText and renderSystemMessageUpdate of each message, one JSON line per message, for the providers-faux-streaming/32 parity scenario.
func TestSystemMessageDocsParity(t *testing.T) {
	for _, doc := range systemMessageDocs() {
		message := systemMessageFromDoc(t, doc)
		fmt.Printf("system-message-observation:%s\n", jsonLine(GetSystemMessageText(message))+","+jsonLine(RenderSystemMessageUpdate(message)))
	}
}

// systemMessageDocs is 3000 seeded system messages as JSON: text or block content and sections named with integer-like, duplicate, empty and unicode keys, null and empty values.
func systemMessageDocs() []string {
	rng := rand.New(rand.NewSource(20261010))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var docs []string
	for range 3000 {
		var content string
		switch rng.Intn(4) {
		case 0:
			content = quote(pick("", "rules", "a\nb", "é😀"))
		case 1:
			content = "[]"
		default:
			var blocks []string
			for range rng.Intn(4) {
				blocks = append(blocks, `{"type":"text","text":`+quote(pick("", "x", "y z", "😀"))+`}`)
			}
			content = "[" + strings.Join(blocks, ",") + "]"
		}
		var members []string
		for range rng.Intn(6) {
			value := "null"
			if rng.Intn(4) != 0 {
				value = quote(pick("", "v", "multi\nline", "é"))
			}
			members = append(members, quote(pick("b", "a", "10", "2", "1", "01", "4294967294", "4294967295", "-1", "1.5", "", "é", "z"))+":"+value)
		}
		sections := ""
		if rng.Intn(5) != 0 {
			sections = `,"sections":{` + strings.Join(members, ",") + `}`
		}
		docs = append(docs, `{"role":"system","content":`+content+sections+`}`)
	}
	return docs
}

// systemMessageFromDoc decodes one corpus document into PiG's SystemMessage.
func systemMessageFromDoc(t *testing.T, doc string) SystemMessage {
	t.Helper()
	var wire struct {
		Content  json.RawMessage `json:"content"`
		Sections OrderedSections `json:"sections"`
	}
	if err := json.Unmarshal([]byte(doc), &wire); err != nil {
		t.Fatalf("%s: %v", doc, err)
	}
	message := SystemMessage{Sections: wire.Sections}
	if wire.Content[0] == '"' {
		var text string
		_ = json.Unmarshal(wire.Content, &text)
		message.Content = SystemText(text)
		return message
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(wire.Content, &blocks)
	content := SystemTextBlocks{}
	for _, block := range blocks {
		content = append(content, TextContent{Text: block.Text})
	}
	message.Content = content
	return message
}
