package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providerStreamAbortExtension registers an extension provider whose stream shows "alpha beta " and then waits for the abort of its request. The model id names what its
// author does once the request's signal aborts. A prompt without the word HOLD is answered at once with the context the request received, as JSON text.
// The author-never model does nothing after the abort: it neither pushes nor ends its stream. It shows the id of its process after "alpha beta ".
const providerStreamAbortExtension = `import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
const ids = ["author-error", "author-result", "author-ignores", "author-throws", "author-never"];
const usage = () => ({ input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } });
const texts = (message) => Array.isArray(message.content) ? message.content.map((part) => part.type === "text" ? part.text : part.type) : message.content;
const aborted = (signal) => new Promise((resolve) => { if (signal.aborted) resolve(); else signal.addEventListener("abort", () => resolve(), { once: true }); });
const later = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function streamProbe(model, context, options) {
  const output = { role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id, usage: usage(), stopReason: "stop", timestamp: Date.now() };
  const block = { type: "text", text: "" };
  if (!JSON.stringify(texts(context.messages[context.messages.length - 1])).includes("HOLD")) {
    const stream = createAssistantMessageEventStream();
    block.text = "CONTEXT " + JSON.stringify(context.messages.map((message) => ({ role: message.role, stopReason: message.stopReason, errorMessage: message.errorMessage, content: texts(message) })));
    output.content.push(block);
    queueMicrotask(() => {
      stream.push({ type: "start", partial: output });
      stream.push({ type: "text_start", contentIndex: 0, partial: output });
      stream.push({ type: "text_delta", contentIndex: 0, delta: block.text, partial: output });
      stream.push({ type: "text_end", contentIndex: 0, content: block.text, partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
    });
    return stream;
  }
  if (model.id === "author-throws") {
    return {
      async *[Symbol.asyncIterator]() {
        yield { type: "start", partial: output };
        output.content.push(block);
        yield { type: "text_start", contentIndex: 0, partial: output };
        block.text = "alpha ";
        yield { type: "text_delta", contentIndex: 0, delta: "alpha ", partial: output };
        block.text = "alpha beta ";
        yield { type: "text_delta", contentIndex: 0, delta: "beta ", partial: output };
        await aborted(options.signal);
        throw new Error("the author's stream threw at the abort");
      },
      result: async () => output,
    };
  }
  const stream = createAssistantMessageEventStream();
  const delta = (piece) => { block.text += piece; stream.push({ type: "text_delta", contentIndex: 0, delta: piece, partial: output }); };
  (async () => {
    stream.push({ type: "start", partial: output });
    output.content.push(block);
    stream.push({ type: "text_start", contentIndex: 0, partial: output });
    delta("alpha ");
    await later(5);
    delta("beta ");
    if (model.id === "author-never") delta("pid " + process.pid + " ");
    await aborted(options.signal);
    if (model.id === "author-error") {
      output.stopReason = "aborted";
      output.errorMessage = "the author saw the abort";
      stream.push({ type: "error", reason: "aborted", error: output });
      stream.end();
    } else if (model.id === "author-result") {
      stream.end({ ...output, content: [{ type: "text", text: block.text }], stopReason: "stop" });
    } else if (model.id === "author-never") {
      await new Promise(() => {});
    } else {
      await later(300);
      delta("gamma ");
      stream.push({ type: "text_end", contentIndex: 0, content: block.text, partial: output });
      stream.push({ type: "done", reason: "stop", message: output });
      stream.end();
    }
  })();
  return stream;
}
export default function (pi) {
  pi.registerProvider("abort-probe", {
    baseUrl: "http://localhost:0", apiKey: "unused", api: "abort-probe-api",
    models: ids.map((id) => ({ id, name: id, api: "abort-probe-api", provider: "abort-probe", baseUrl: "http://localhost:0", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 })),
    streamSimple: streamProbe,
  });
}
`

// providerStreamAbortSibling is a second Node extension, so that the host packs the provider's extension with it in one runtime cell.
const providerStreamAbortSibling = `export default function (pi) {
  pi.registerCommand("provider-abort-sibling", { description: "nothing", handler: async () => {} });
}
`

// providerStreamAbortGoEntered is the notification the Go fixture's author-throws model sends once its streamSimple runs, before it waits for its signal.
const providerStreamAbortGoEntered = "author-throws waits for the abort"

// providerStreamAbortGoModule is the go.mod of the Go fixture; the argument is the directory of this tree's Go extension SDK.
const providerStreamAbortGoModule = `module provider-abort-go

go 1.26

require github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0

replace github.com/MichaelKinsy/PiG/extensions/sdk => %s
`

// providerStreamAbortGoExtension is providerStreamAbortExtension written with the Go extension SDK: the same provider, models and texts. A Go author's stream has Push and
// End, and no iterator that can fail, so the author-throws model fails in streamSimple itself: it shows nothing, tells the host that it runs, waits for its signal and
// returns the error.
const providerStreamAbortGoExtension = `package providerabortgo

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	ext := sdk.New("provider-abort-go")
	models := []any{}
	for _, id := range []string{"author-error", "author-result", "author-ignores", "author-throws"} {
		models = append(models, map[string]any{"id": id, "name": id, "api": "abort-probe-api", "provider": "abort-probe", "baseUrl": "http://localhost:0", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 128000, "maxTokens": 4096})
	}
	ext.RegisterProvider("abort-probe", sdk.ProviderConfig{"baseUrl": "http://localhost:0", "apiKey": "unused", "api": "abort-probe-api", "models": models, "streamSimple": streamProbe})
	return ext
}

func usage() map[string]any {
	return map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}
}

// texts is a message's content as the Node fixture reports it: a string as it is, or each part's text (its type, for a part that is not text).
func texts(message map[string]any) any {
	parts, ok := message["content"].([]any)
	if !ok {
		return message["content"]
	}
	out := []string{}
	for _, entry := range parts {
		part, _ := entry.(map[string]any)
		member := "type"
		if part["type"] == "text" {
			member = "text"
		}
		text, _ := part[member].(string)
		out = append(out, text)
	}
	return out
}

func quote(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// report is the context a request received, with the members in the order of the Node fixture's JSON.stringify (role, stopReason, errorMessage, content); a member the message does not have is left out.
func report(messages []any) string {
	var out strings.Builder
	out.WriteString("CONTEXT [")
	for i, entry := range messages {
		message, _ := entry.(map[string]any)
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString("{\"role\":" + quote(message["role"]))
		for _, member := range []string{"stopReason", "errorMessage"} {
			if value, ok := message[member]; ok {
				out.WriteString(",\"" + member + "\":" + quote(value))
			}
		}
		out.WriteString(",\"content\":" + quote(texts(message)) + "}")
	}
	out.WriteString("]")
	return out.String()
}

func streamProbe(ctx sdk.Context, model, request, _ map[string]any) (*sdk.ModelEventStream, error) {
	id, _ := model["id"].(string)
	output := map[string]any{"role": "assistant", "content": []any{}, "api": model["api"], "provider": model["provider"], "model": id, "usage": usage(), "stopReason": "stop", "timestamp": time.Now().UnixMilli()}
	block := map[string]any{"type": "text", "text": ""}
	messages, _ := request["messages"].([]any)
	last := map[string]any{}
	if len(messages) > 0 {
		last, _ = messages[len(messages)-1].(map[string]any)
	}
	stream := sdk.CreateAssistantMessageEventStream()
	if !strings.Contains(quote(texts(last)), "HOLD") {
		block["text"] = report(messages)
		output["content"] = []any{block}
		stream.Push(map[string]any{"type": "start", "partial": output})
		stream.Push(map[string]any{"type": "text_start", "contentIndex": 0, "partial": output})
		stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": block["text"], "partial": output})
		stream.Push(map[string]any{"type": "text_end", "contentIndex": 0, "content": block["text"], "partial": output})
		stream.Push(map[string]any{"type": "done", "reason": "stop", "message": output})
		return stream, nil
	}
	if id == "author-throws" {
		ctx.Notify("author-throws waits for the abort", "info")
		<-ctx.Done()
		return nil, errors.New("the author's stream threw at the abort")
	}
	delta := func(piece string) {
		text, _ := block["text"].(string)
		block["text"] = text + piece
		stream.Push(map[string]any{"type": "text_delta", "contentIndex": 0, "delta": piece, "partial": output})
	}
	go func() {
		stream.Push(map[string]any{"type": "start", "partial": output})
		output["content"] = []any{block}
		stream.Push(map[string]any{"type": "text_start", "contentIndex": 0, "partial": output})
		delta("alpha ")
		time.Sleep(5 * time.Millisecond)
		delta("beta ")
		<-ctx.Done()
		switch id {
		case "author-error":
			output["stopReason"] = "aborted"
			output["errorMessage"] = "the author saw the abort"
			stream.Push(map[string]any{"type": "error", "reason": "aborted", "error": output})
		case "author-result":
			result := map[string]any{}
			for key, value := range output {
				result[key] = value
			}
			result["content"] = []any{map[string]any{"type": "text", "text": block["text"]}}
			stream.End(result)
		default:
			time.Sleep(300 * time.Millisecond)
			delta("gamma ")
			stream.Push(map[string]any{"type": "text_end", "contentIndex": 0, "content": block["text"], "partial": output})
			stream.Push(map[string]any{"type": "done", "reason": "stop", "message": output})
		}
	}()
	return stream, nil
}
`

// TestRPCAbortedExtensionProviderTurnKeepsWhatTheAuthorGives: Pi's consumer of a response has no check of the abort signal (packages/agent/src/agent-loop.ts:424-478), so what
// the stream of an extension's streamSimple does after the abort decides the turn's final message. Pi 1.5.2, run with this extension in its own process, gives: an author
// that pushes an error event with its partial keeps the partial, its stopReason and its errorMessage; an author that ends its stream with a result gives that result
// (stopReason stop); an author that ignores the signal and completes gives the whole message, and the delta it pushed after the abort is shown; an author whose stream throws
// gives an empty aborted message whose errorMessage is the thrown text. The message_end event, the persisted session line and the context the provider's next request
// receives all hold that message. The host used to end the stream at the cancel with a message of its own: empty content, aborted, "context canceled", in every case.
//
// The rows run the Node extension alone in its process and packed with a second one, and the same provider written with the Go SDK (go). The Go author-throws model fails
// in streamSimple itself, so its turn shows no text; its final message is the same.
func TestRPCAbortedExtensionProviderTurnKeepsWhatTheAuthorGives(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model, shown, text, stopReason, errorMessage, context string
	}{
		{model: "author-error", shown: "alpha beta ", text: "alpha beta ", stopReason: "aborted", errorMessage: "the author saw the abort", context: `{"role":"assistant","stopReason":"aborted","errorMessage":"the author saw the abort","content":["alpha beta "]}`},
		{model: "author-result", shown: "alpha beta ", text: "alpha beta ", stopReason: "stop", context: `{"role":"assistant","stopReason":"stop","content":["alpha beta "]}`},
		{model: "author-ignores", shown: "alpha beta gamma ", text: "alpha beta gamma ", stopReason: "stop", context: `{"role":"assistant","stopReason":"stop","content":["alpha beta gamma "]}`},
		{model: "author-throws", shown: "alpha beta ", stopReason: "aborted", errorMessage: "the author's stream threw at the abort", context: `{"role":"assistant","stopReason":"aborted","errorMessage":"the author's stream threw at the abort","content":[]}`},
	}
	for _, kind := range []string{"isolated", "packed", "go"} {
		for _, tc := range cases {
			t.Run(tc.model+"/"+kind, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				sessions := filepath.Join(dir, "sessions")
				args := []string{"--model", "abort-probe/" + tc.model, "--session-dir", sessions}
				// entered is the notification that tells the test a model which shows nothing before the abort is running.
				shownWant, entered := tc.shown, ""
				if kind == "go" {
					source := filepath.Join(dir, "provider-abort-go")
					sdkDir, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(source, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, "go.mod"), fmt.Appendf(nil, providerStreamAbortGoModule, sdkDir), 0o600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(source, "extension.go"), []byte(providerStreamAbortGoExtension), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-e", source)
					if tc.model == "author-throws" {
						shownWant, entered = "", providerStreamAbortGoEntered
					}
				} else {
					fixture := filepath.Join(dir, "provider-abort.mjs")
					if err := os.WriteFile(fixture, []byte(providerStreamAbortExtension), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-e", fixture)
					if kind == "packed" {
						sibling := filepath.Join(dir, "provider-abort-sibling.mjs")
						if err := os.WriteFile(sibling, []byte(providerStreamAbortSibling), 0o600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "-e", sibling)
					}
				}
				p := startRPCProcess(t, nil, args...)

				// The text the turn's updates show, delta by delta.
				shown := ""
				show := func(record rpcRecord) {
					if record["type"] != "message_update" {
						return
					}
					event, _ := record["assistantMessageEvent"].(map[string]any)
					if event["type"] == "text_delta" {
						delta, _ := event["delta"].(string)
						shown += delta
					}
				}
				p.send(`{"id":"hold","type":"prompt","message":"HOLD the stream"}`)
				p.await("the partial text before the abort", func(record rpcRecord) bool {
					show(record)
					if entered != "" {
						return record["type"] == "extension_ui_request" && record["method"] == "notify" && record["message"] == entered
					}
					return shown == "alpha beta "
				})
				p.send(`{"id":"abort","type":"abort"}`)
				var final map[string]any
				p.await("the turn's final message and the abort's answer", func(record rpcRecord) bool {
					show(record)
					if record["type"] == "message_end" {
						if message, _ := record["message"].(map[string]any); message["role"] == "assistant" {
							final = message
						}
					}
					return isSuccessResponse(record, "abort")
				})
				if final == nil {
					t.Fatal("the aborted turn ended with no assistant message")
				}
				if got := providerAbortMessage(final); got != providerAbortWant(tc.text, tc.stopReason, tc.errorMessage) {
					t.Errorf("the aborted turn's final message is %s, want %s: what the author's stream gave after the abort", got, providerAbortWant(tc.text, tc.stopReason, tc.errorMessage))
				}
				if shown != shownWant {
					t.Errorf("the turn's updates show %q, want %q", shown, shownWant)
				}

				// The provider's next request receives the aborted turn's message as the Session holds it.
				report := ""
				p.send(`{"id":"report","type":"prompt","message":"REPORT what the request received"}`)
				p.await("the next request's report", func(record rpcRecord) bool {
					if record["type"] == "message_end" {
						if message, _ := record["message"].(map[string]any); message["role"] == "assistant" {
							report = providerAbortText(message)
						}
					}
					return record["type"] == "agent_end"
				})
				if !strings.Contains(report, tc.context) {
					t.Errorf("the next request received %s, want it to hold the aborted turn as %s", report, tc.context)
				}
				p.closeAndWait("after the aborted provider turn")

				// The persisted line of the aborted turn.
				var persisted []string
				files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
				if err != nil || len(files) != 1 {
					t.Fatalf("session files = %v (%v), want one", files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
					var entry struct {
						Type    string         `json:"type"`
						Message map[string]any `json:"message"`
					}
					if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "message" && entry.Message["role"] == "assistant" {
						persisted = append(persisted, providerAbortMessage(entry.Message))
					}
				}
				if len(persisted) != 2 || persisted[0] != providerAbortWant(tc.text, tc.stopReason, tc.errorMessage) {
					t.Errorf("the session file holds the assistant messages %q, want two, the first %s", persisted, providerAbortWant(tc.text, tc.stopReason, tc.errorMessage))
				}
			})
		}
	}
}

// providerAbortText is the text of a message's text blocks.
func providerAbortText(message map[string]any) string {
	var text strings.Builder
	blocks, _ := message["content"].([]any)
	for _, block := range blocks {
		if part, _ := block.(map[string]any); part["type"] == "text" {
			value, _ := part["text"].(string)
			text.WriteString(value)
		}
	}
	return text.String()
}

// providerAbortMessage states what the test compares of an assistant message: how many content blocks it has, their text, its stopReason and its errorMessage.
func providerAbortMessage(message map[string]any) string {
	blocks, _ := message["content"].([]any)
	stopReason, _ := message["stopReason"].(string)
	errorMessage, _ := message["errorMessage"].(string)
	state, _ := json.Marshal(map[string]any{"blocks": len(blocks), "text": providerAbortText(message), "stopReason": stopReason, "errorMessage": errorMessage})
	return string(state)
}

// providerAbortWant is providerAbortMessage of the expected message: one text block, or none when the text is empty.
func providerAbortWant(text, stopReason, errorMessage string) string {
	blocks := 1
	if text == "" {
		blocks = 0
	}
	state, _ := json.Marshal(map[string]any{"blocks": blocks, "text": text, "stopReason": stopReason, "errorMessage": errorMessage})
	return string(state)
}

// TestRPCAbortedExtensionProviderTurnIsKeptWhenStdinClosesAtTheAbort: the client sends the abort and closes stdin at once, without waiting for the turn's end. Pi's
// shutdown awaits session.abort() (rpc-mode.ts shutdown, agent-session-runtime.ts dispose), and its event loop stays alive while the author's stream has work pending: an
// author that answers the abort in its listener, and one that goes on for 0.3 s on a timer and completes, both end the turn, the message is persisted, and the process
// exits 0. (An author that never reacts leaves Pi's loop empty, and the process exits without the message: TestRPCAbortedExtensionProviderTurnWhoseAuthorNeverReacts.)
func TestRPCAbortedExtensionProviderTurnIsKeptWhenStdinClosesAtTheAbort(t *testing.T) {
	t.Parallel()
	cases := []struct{ model, text, stopReason, errorMessage string }{
		{model: "author-error", text: "alpha beta ", stopReason: "aborted", errorMessage: "the author saw the abort"},
		{model: "author-result", text: "alpha beta ", stopReason: "stop"},
		{model: "author-ignores", text: "alpha beta gamma ", stopReason: "stop"},
	}
	for _, kind := range []string{"isolated", "packed"} {
		for _, tc := range cases {
			t.Run(tc.model+"/"+kind, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				fixture := filepath.Join(dir, "provider-abort.mjs")
				if err := os.WriteFile(fixture, []byte(providerStreamAbortExtension), 0o600); err != nil {
					t.Fatal(err)
				}
				sessions := filepath.Join(dir, "sessions")
				args := []string{"--model", "abort-probe/" + tc.model, "--session-dir", sessions, "-e", fixture}
				if kind == "packed" {
					sibling := filepath.Join(dir, "provider-abort-sibling.mjs")
					if err := os.WriteFile(sibling, []byte(providerStreamAbortSibling), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-e", sibling)
				}
				p := startRPCProcess(t, nil, args...)
				shown := ""
				p.send(`{"id":"hold","type":"prompt","message":"HOLD the stream"}`)
				p.await("the partial text before the abort", func(record rpcRecord) bool {
					if record["type"] == "message_update" {
						if event, _ := record["assistantMessageEvent"].(map[string]any); event["type"] == "text_delta" {
							delta, _ := event["delta"].(string)
							shown += delta
						}
					}
					return shown == "alpha beta "
				})
				p.send(`{"id":"abort","type":"abort"}`)
				p.closeAndWait("at the abort of an extension provider turn")
				if got, want := providerAbortPersisted(t, sessions), []string{providerAbortWant(tc.text, tc.stopReason, tc.errorMessage)}; strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Errorf("the session file holds the assistant messages %q, want %q: the turn's message as its author's stream ended it", got, want)
				}
			})
		}
	}
}

// providerAbortPersisted is providerAbortMessage of each assistant message in the one session file under sessions.
func providerAbortPersisted(t *testing.T, sessions string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sessions, "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %v (%v), want one", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	persisted := []string{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry struct {
			Type    string         `json:"type"`
			Message map[string]any `json:"message"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Type == "message" && entry.Message["role"] == "assistant" {
			persisted = append(persisted, providerAbortMessage(entry.Message))
		}
	}
	return persisted
}
