package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providerStreamAbortExtension registers an extension provider whose stream shows "alpha beta " and then waits for the abort of its request. The model id names what its
// author does once the request's signal aborts. A prompt without the word HOLD is answered at once with the context the request received, as JSON text.
const providerStreamAbortExtension = `import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
const ids = ["author-error", "author-result", "author-ignores", "author-throws"];
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
    await aborted(options.signal);
    if (model.id === "author-error") {
      output.stopReason = "aborted";
      output.errorMessage = "the author saw the abort";
      stream.push({ type: "error", reason: "aborted", error: output });
      stream.end();
    } else if (model.id === "author-result") {
      stream.end({ ...output, content: [{ type: "text", text: block.text }], stopReason: "stop" });
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

// TestRPCAbortedExtensionProviderTurnKeepsWhatTheAuthorGives: Pi's consumer of a response has no check of the abort signal (packages/agent/src/agent-loop.ts:424-478), so what
// the stream of an extension's streamSimple does after the abort decides the turn's final message. Pi 1.5.2, run with this extension in its own process, gives: an author
// that pushes an error event with its partial keeps the partial, its stopReason and its errorMessage; an author that ends its stream with a result gives that result
// (stopReason stop); an author that ignores the signal and completes gives the whole message, and the delta it pushed after the abort is shown; an author whose stream throws
// gives an empty aborted message whose errorMessage is the thrown text. The message_end event, the persisted session line and the context the provider's next request
// receives all hold that message. The host used to end the stream at the cancel with a message of its own: empty content, aborted, "context canceled", in every case.
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
	for _, packed := range []bool{false, true} {
		for _, tc := range cases {
			name := tc.model + "/isolated"
			if packed {
				name = tc.model + "/packed"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				fixture := filepath.Join(dir, "provider-abort.mjs")
				if err := os.WriteFile(fixture, []byte(providerStreamAbortExtension), 0o600); err != nil {
					t.Fatal(err)
				}
				sessions := filepath.Join(dir, "sessions")
				args := []string{"--model", "abort-probe/" + tc.model, "--session-dir", sessions, "-e", fixture}
				if packed {
					sibling := filepath.Join(dir, "provider-abort-sibling.mjs")
					if err := os.WriteFile(sibling, []byte(providerStreamAbortSibling), 0o600); err != nil {
						t.Fatal(err)
					}
					args = append(args, "-e", sibling)
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
				if shown != tc.shown {
					t.Errorf("the turn's updates show %q, want %q", shown, tc.shown)
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
