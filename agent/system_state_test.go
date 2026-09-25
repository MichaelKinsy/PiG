package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestAgentSystemTranscriptBaseline(t *testing.T) {
	tool := echoScriptTool("echo")
	a := NewAgent(AgentOptions{SystemPrompt: "You are helpful.", Tools: []AgentTool{tool}})
	if got := roles(a.Messages()); !reflect.DeepEqual(got, []string{"system"}) {
		t.Fatalf("initial roles %v", got)
	}
	if _, err := a.Continue(context.Background()); !errors.Is(err, ErrNoMessagesToContinue) {
		t.Fatalf("system-only continuation: %v", err)
	}
	if err := a.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := roles(a.Messages()); !reflect.DeepEqual(got, []string{"system"}) {
		t.Fatalf("reset roles %v", got)
	}
}

func TestAgentDeclaresToolLoadoutChanges(t *testing.T) {
	p := &scriptedProvider{respond: replyText("done")}
	a := NewAgent(AgentOptions{Model: scriptedModel(p), SystemPrompt: "You are helpful.", Tools: []AgentTool{echoScriptTool("first")}})
	mustSend(t, a, "one")
	a.SetTools([]AgentTool{echoScriptTool("second")})
	mustSend(t, a, "two")
	mustSend(t, a, "three")
	for i, want := range []int{1, 2, 2} {
		var systems []ai.SystemMessage
		for _, m := range p.request(i + 1).transcript.Messages() {
			if s, ok := m.(ai.SystemMessage); ok {
				systems = append(systems, s)
			}
		}
		if len(systems) != want {
			t.Fatalf("request %d: %d system messages, want %d", i+1, len(systems), want)
		}
		if systems[0].ToolsAdded[0].Name != "first" {
			t.Fatalf("rewritten initial loadout: %+v", systems)
		}
		if i > 0 && (systems[1].ToolsAdded[0].Name != "second" || systems[1].ToolsRemoved[0].Name != "first") {
			t.Fatalf("bad delta %+v", systems[1])
		}
	}
}

func TestAgentRewritesPendingToolDeclarations(t *testing.T) {
	p := &scriptedProvider{respond: replyText("done")}
	a := NewAgent(AgentOptions{Model: scriptedModel(p), SystemPrompt: "base", Tools: []AgentTool{echoScriptTool("first")}})
	var pending AgentMessage
	raw := []byte(`{"role":"system","content":"","sections":{"note":"keep"},"toolsAdded":[{"name":"second","parameters":{"type":"object"}}],"toolsRemoved":[{"name":"first"}],"timestamp":1}`)
	if err := json.Unmarshal(raw, &pending); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendMessages(context.Background(), []AgentMessage{pending, userMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	messages := p.request(1).transcript.Messages()
	if len(messages) != 3 {
		t.Fatalf("provider messages %+v", messages)
	}
	update, ok := messages[1].(ai.SystemMessage)
	if !ok || len(update.ToolsAdded) != 0 || len(update.ToolsRemoved) != 0 || len(update.Sections) != 1 {
		t.Fatalf("pending update %+v", messages[1])
	}
	wire, err := json.Marshal(a.Messages()[1])
	if err != nil {
		t.Fatal(err)
	}
	var replay AgentMessage
	if err := json.Unmarshal(wire, &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Role() != "system" {
		t.Fatalf("lost system role: %s", wire)
	}
}

func echoScriptTool(name string) *scriptTool {
	return &scriptTool{name: name, params: map[string]any{"type": "object"}}
}

func TestAgentForcedSystemPromptPreservesTranscript(t *testing.T) {
	p := &scriptedProvider{respond: replyText("done")}
	a := NewAgent(AgentOptions{Model: scriptedModel(p), SystemPrompt: "base", Tools: []AgentTool{echoScriptTool("echo")}})
	a.SetSystemPrompt("forced")
	mustSend(t, a, "hello")
	request := p.request(1).transcript.Messages()
	if got := ai.GetCurrentSystemPrompt(request); got != "forced" {
		t.Fatalf("request prompt %q", got)
	}
	if got := ai.GetCurrentTools(request); len(got) != 1 || got[0].Name != "echo" {
		t.Fatalf("request tools %+v", got)
	}
	if got := a.Messages()[0].System.Content; got != ai.SystemText("base") {
		t.Fatalf("rewritten transcript %v", got)
	}
}

func TestAgentMergesToolChangesIntoPendingSystemMessage(t *testing.T) {
	p := &scriptedProvider{respond: replyText("done")}
	a := NewAgent(AgentOptions{Model: scriptedModel(p), SystemPrompt: "base"})
	a.SetTools([]AgentTool{echoScriptTool("echo")})
	pending := AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "skills", Value: new("keep")}}, Timestamp: 1}}
	if _, err := a.SendMessages(context.Background(), []AgentMessage{pending, userMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	request := p.request(1).transcript.Messages()
	if len(request) != 3 {
		t.Fatalf("request messages %+v", request)
	}
	update, ok := request[1].(ai.SystemMessage)
	if !ok || len(update.ToolsAdded) != 1 || update.ToolsAdded[0].Name != "echo" || len(update.Sections) != 1 || *update.Sections[0].Value != "keep" || update.Timestamp != 1 {
		t.Fatalf("merged update %+v", request[1])
	}
	if len(pending.System.ToolsAdded) != 0 {
		t.Fatal("mutated caller's pending system message")
	}
}

func BenchmarkAgentToolLoadoutPrompt(b *testing.B) {
	tools := []AgentTool{echoScriptTool("read"), echoScriptTool("write"), echoScriptTool("bash")}
	for b.Loop() {
		p := &scriptedProvider{respond: replyText("done")}
		a := NewAgent(AgentOptions{Model: scriptedModel(p), SystemPrompt: "instructions", Tools: tools})
		if _, err := a.Send(context.Background(), "hello"); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSystemMessageContentBlocks(t *testing.T) {
	for _, content := range []ai.SystemContent{ai.SystemText("instructions"), ai.SystemTextBlocks{{Text: "instructions"}}} {
		message := AgentMessage{System: &ai.SystemMessage{Content: content}}
		blocks := message.ContentBlocks()
		if len(blocks) != 1 || blocks[0].(ai.TextContent).Text != "instructions" {
			t.Fatalf("system body lost: %+v", blocks)
		}
	}
}
