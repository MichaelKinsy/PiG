package coding

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi keeps text as JavaScript strings, so a lone UTF-16 unit in a model's tool arguments, an extension's user message or a persisted usage entry reaches the next consumer and the session file as the same unit, written by JSON.stringify as the escape `\ud800` with `<`, `>` and `&` literal. The comparisons below take Pi's text from the qualified Node.

// piStringify is JSON.stringify(JSON.parse(text)) for each document, the bytes Pi writes for the same value.
func piStringify(t *testing.T, documents ...string) []string {
	t.Helper()
	return testenv.PiEval[[]string](t, `return input.map((document) => JSON.stringify(JSON.parse(document)));`, documents)
}

func unitsOf(text string) []uint16 { return jsstring.ToUTF16(text) }

func TestExtensionUserMessageKeepsALoneSurrogate(t *testing.T) {
	// agent-session.ts sendUserMessage joins the text blocks and prompts with the string; appendMessage persists it with JSON.stringify.
	lone := jsstring.FromUTF16([]uint16{0xd800})
	for name, content := range map[string]any{
		"decoded blocks": []any{map[string]any{"type": "text", "text": "a" + lone}, map[string]any{"type": "text", "text": "<&>"}},
		"wire blocks":    json.RawMessage(`[{"type":"text","text":"a\ud800"},{"type":"text","text":"<&>"}]`),
	} {
		t.Run(name, func(t *testing.T) {
			h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
			h.provider.responses = []scriptedResponse{fauxReply("response", ai.StopReasonStop, 0)}
			if err := h.session.SendUserMessage(t.Context(), content, nil); err != nil {
				t.Fatal(err)
			}
			messages := h.session.Messages()
			if want := "a" + lone + "\n<&>"; extractUserMessageText(messages[1].User.Content) != want {
				t.Fatalf("user text units=%x, want %x", unitsOf(extractUserMessageText(messages[1].User.Content)), unitsOf(want))
			}
			var line string
			for _, entry := range h.session.inner.Load().GetEntries() {
				if entry.Base().Type == "message" && strings.Contains(string(entry.Raw()), `"role":"user"`) {
					line = string(entry.Raw())
				}
			}
			if want := piStringify(t, line)[0]; line != want || !strings.Contains(line, `a\ud800\n<&>`) {
				t.Fatalf("user entry=%s\nPi's    %s", line, want)
			}
		})
	}
}

func TestToolCallHookKeepsALoneSurrogateInTheModelsArguments(t *testing.T) {
	// _beforeToolCall hands the handlers the parsed arguments object; a handler's edit is what the tool runs with (JSON.stringify of that object).
	lone := jsstring.FromUTF16([]uint16{0xd800})
	const modelArgs = `{"text":"a\ud800<&>","n":1}`
	for _, tc := range []struct {
		name string
		edit func(extension.CustomToolCallEvent)
		pi   string // JavaScript statement applied to `input` for Pi's edit
	}{
		{name: "no edit", pi: ``},
		{name: "edit of another member", edit: func(event extension.CustomToolCallEvent) { event.Input["n"] = 2.0 }, pi: `args.n = 2;`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen, wire string
			ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"tool_call": {func(args ...any) (any, error) {
				event := args[0].(extension.CustomToolCallEvent)
				seen, _ = event.Input["text"].(string)
				encoded, err := event.InputJSON()
				if err != nil {
					return nil, err
				}
				wire = string(encoded)
				if tc.edit != nil {
					tc.edit(event)
				}
				return nil, nil
			}}}}
			session, _ := newOrchestrationSession(t, ext)
			hook := session.extensionToolCallHook(t.Context(), "call", "bash", json.RawMessage(modelArgs))
			if seen != "a"+lone+"<&>" {
				t.Errorf("handler saw units %x, want %x", unitsOf(seen), unitsOf("a"+lone+"<&>"))
			}
			if wire != modelArgs {
				t.Errorf("event input %s, want the model's %s", wire, modelArgs)
			}
			want := testenv.PiEval[string](t, `const args = JSON.parse(input); `+tc.pi+` return JSON.stringify(args);`, modelArgs)
			if tc.edit == nil {
				if len(hook.Args) != 0 {
					t.Errorf("an unedited call got arguments %s", hook.Args)
				}
				return
			}
			if string(hook.Args) != want {
				t.Errorf("call arguments after the edit = %s, Pi's %s", hook.Args, want)
			}
		})
	}
}

func TestContextHookSeesAHandlersEditOfALoneSurrogate(t *testing.T) {
	// runner.emitContext hands the handlers the messages and uses what they return: one lone unit changed to another is a change.
	text := func(unit uint16) []extension.AgentMessage {
		return []extension.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "a" + jsstring.FromUTF16([]uint16{unit})}}, Timestamp: time.Now().UnixMilli()}}}
	}
	returned, visible := text(0xd801), text(0xd800)
	returned[0].User.Timestamp, visible[0].User.Timestamp = 1, 1
	if sameContextMessages(returned, visible) {
		t.Fatal("a message whose lone surrogate changed from \\ud800 to \\ud801 was read as unchanged")
	}
}

func TestEntryAppendedCarriesTheEntryAsTheSessionWroteIt(t *testing.T) {
	// The event reports the usage entry "exactly as it was persisted": Pi's JSON.stringify keeps `<`, `>`, `&` and a lone surrogate's escape.
	lone := jsstring.FromUTF16([]uint16{0xd800})
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	events := sess.Events()
	entry := icodingagent.UsageEntry{Kind: "cache_warm", Provider: "p<&>" + lone, Model: "m", Note: "n\u2028"}
	entry.Type, entry.ID, entry.Timestamp = "usage", "u1", "2026-01-01T00:00:00.000Z"
	sess.emitEntryAppended(t.Context(), entry)
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			appended, ok := event.(agent.EntryAppendedEvent)
			if !ok {
				continue
			}
			line := string(appended.Entry)
			if want := piStringify(t, line)[0]; line != want || !strings.Contains(line, `"provider":"p<&>\ud800"`) || !strings.Contains(line, "n\u2028") {
				t.Fatalf("entry_appended entry=%s\nPi's              %s", line, want)
			}
			return
		case <-timeout:
			t.Fatal("no entry_appended event")
		}
	}
}
