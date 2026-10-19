package ai

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// A response.output_item.added/.done item that Pi's processResponsesStream cannot read throws a V8 TypeError, which the
// stream reports as its error; Go must surface the same message on the same operation instead of skipping the event
// (openai-responses-shared.ts: createSlot/applyMessagePhaseStopReason read item.type, :689-690 summary/content .map,
// :702 content .map with c.type, :741 parseToolCallArguments(json?.trim), Pi 87f9a364).
func TestOpenAIResponsesOutputItemTypeErrorsMatchPi(t *testing.T) {
	const (
		typeUndefined = "Cannot read properties of undefined (reading 'type')"
		typeNull      = "Cannot read properties of null (reading 'type')"
		textNull      = "Cannot read properties of null (reading 'text')"
		summaryMap    = "item.summary?.map is not a function"
		contentMap    = "item.content?.map is not a function"
		trim          = "json?.trim is not a function"
	)
	event := func(kind, item string) string {
		return `{"type":"response.output_item.` + kind + `","output_index":0` + item + `}`
	}
	for _, test := range []struct {
		name   string
		events []string
		error  string
	}{
		{"added without an item", []string{event("added", "")}, typeUndefined},
		{"added null item", []string{event("added", `,"item":null`)}, typeNull},
		{"done without an item", []string{event("done", "")}, typeUndefined},
		{"done null item", []string{event("done", `,"item":null`)}, typeNull},

		{"reasoning summary object", []string{event("done", `,"item":{"type":"reasoning","summary":{}}`)}, summaryMap},
		{"reasoning summary empty string", []string{event("done", `,"item":{"type":"reasoning","summary":""}`)}, summaryMap},
		{"reasoning summary zero", []string{event("done", `,"item":{"type":"reasoning","summary":0}`)}, summaryMap},
		{"reasoning content string", []string{event("done", `,"item":{"type":"reasoning","summary":[],"content":"x"}`)}, contentMap},
		{"reasoning summary null element", []string{event("done", `,"item":{"type":"reasoning","summary":[null]}`)}, textNull},
		{"reasoning content null element", []string{event("done", `,"item":{"type":"reasoning","content":[null]}`)}, textNull},
		{"reasoning summary is read before content", []string{event("done", `,"item":{"type":"reasoning","summary":{},"content":[null]}`)}, summaryMap},
		{"reasoning after its added item", []string{event("added", `,"item":{"type":"reasoning"}`), event("done", `,"item":{"type":"reasoning","summary":true}`)}, summaryMap},

		{"message content object", []string{event("done", `,"item":{"type":"message","content":{}}`)}, contentMap},
		{"message content null element", []string{event("done", `,"item":{"type":"message","content":[null]}`)}, "Cannot read properties of null (reading 'type')"},

		{"function call arguments object", []string{event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":{}}`)}, trim},
		{"function call arguments number", []string{event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":5}`)}, trim},
		{"function call arguments array", []string{event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":["a"]}`)}, trim},

		// createSlot keeps a truthy non-string added `arguments` as partialJson; output_item.done without its own arguments then parses it and throws, unless a delta or arguments.done made it a string first (measured with Pi's pinned openai-responses stream).
		{"added arguments number, done without arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":5}`), event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n"}`)}, trim},
		{"added arguments number, done empty arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":5}`), event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":""}`)}, trim},
		{"added arguments true, done null arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":true}`), event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":null}`)}, trim},
		{"added arguments empty array, done without arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":[]}`), event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n"}`)}, trim},
		{"added arguments number, a delta, done without arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":5}`), `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"a\":1}"}`, event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n"}`)}, "toolUse"},
		{"added arguments object, arguments.done, done without arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":{}}`), `{"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"b\":2}"}`, event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n"}`)}, "toolUse"},
		{"added arguments falsy, done without arguments", []string{event("added", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":0}`), event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n"}`)}, "toolUse"},

		// Pi reads nothing that throws in these items, so the stream completes; a falsy arguments value falls back to "{}" and the call is kept (stop reason toolUse).
		{"reasoning summary null", []string{event("done", `,"item":{"type":"reasoning","summary":null}`)}, ""},
		{"reasoning summary primitive elements", []string{event("done", `,"item":{"type":"reasoning","summary":["a",1,true]}`)}, ""},
		{"message content primitive elements", []string{event("done", `,"item":{"type":"message","content":["a",1]}`)}, ""},
		{"function call falsy arguments", []string{event("done", `,"item":{"type":"function_call","call_id":"c","id":"i","name":"n","arguments":0}`)}, "toolUse"},
		{"scalar item", []string{event("added", `,"item":"x"`), event("done", `,"item":7`)}, ""},
		{"item without a block type", []string{event("done", `,"item":{"type":"web_search_call","summary":{}}`)}, ""},
		{"a text slot already holds the index", []string{event("added", `,"item":{"type":"message"}`), event("done", `,"item":{"type":"reasoning","summary":{}}`)}, ""},
	} {
		for _, api := range []API{APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses} {
			t.Run(string(api)+"/"+test.name, func(t *testing.T) {
				result := itemStreamResult(t, api, nil, itemStreamBody(test.events...))
				wantStop, wantError := StopReasonStop, test.error
				switch test.error {
				case "":
				case "toolUse":
					wantStop, wantError = StopReasonToolUse, ""
				default:
					wantStop = StopReasonError
				}
				if result.StopReason != wantStop || result.ErrorMessage != wantError {
					t.Fatalf("stop=%q error=%q, want %q %q", result.StopReason, result.ErrorMessage, wantStop, wantError)
				}
			})
		}
	}
}

// itemStreamBody is the Responses event stream a subtest serves: response.created, the events, response.completed.
func itemStreamBody(events ...string) string {
	body := "data: " + `{"type":"response.created","response":{"id":"r1"}}` + "\n\n"
	for _, e := range events {
		body += "data: " + e + "\n\n"
	}
	return body + "data: " + `{"type":"response.completed","response":{"id":"r1","status":"completed"}}` + "\n\n"
}

// itemStreamResult is one request through the provider for api to a loopback server on listener (a fresh port when nil) that answers it with body, and the stream's result. When it returns the server is closed and
// the client's process-wide connection pool holds no connection to it.
//
// The server closes its connection after its response (Connection: close), so the client never pools one. Every provider shares one keep-alive pool per host:port (baseStreamingHTTPClient), and the next subtest's server
// can get the closed server's address again: the kernel hands a freed loopback port out again, as the llama.cpp classify oracle's probes found. A request that finds the closed server's pooled connection first writes to it
// and fails before response headers ("Connection error." where Pi's stream reports the item's TypeError): the client's connection reader must run between the close and that request to drop the connection, a loaded
// machine does not run it in time, and a POST is never replayed (nor is a plain fetch failure by Pi's retryProviderRequest).
func itemStreamResult(t *testing.T, api API, listener net.Listener, body string) *AssistantMessage {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Connection", "close")
		_, _ = io.WriteString(w, body)
	}))
	if listener != nil {
		_ = server.Listener.Close()
		server.Listener = listener
	}
	server.Start()
	defer server.Close()
	provider := observationProvider(t, api, server.URL)
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

// TestOpenAIResponsesItemStreamServerLeavesNothingForTheNextServer pins what keeps the subtests of TestOpenAIResponsesOutputItemTypeErrorsMatchPi independent of each other: the server of one request, closed, must leave no
// pooled connection for the next server at the same address to meet. Pinning one P makes the schedule that is a rare failure under load certain: the test goroutine goes on to the second server's request without the client's
// connection reader ever running in between, so a server that lets the client pool its connection fails every time with "Connection error." instead of the item's TypeError.
func TestOpenAIResponsesItemStreamServerLeavesNothingForTheNextServer(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const want = "item.content?.map is not a function"
	body := itemStreamBody(`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","summary":[],"content":"x"}}`)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	for i, api := range []API{APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses, APIOpenAIResponses} {
		if i > 0 {
			if listener, err = net.Listen("tcp", address); err != nil {
				t.Fatalf("listening on %s again: %v", address, err)
			}
		}
		if result := itemStreamResult(t, api, listener, body); result.StopReason != StopReasonError || result.ErrorMessage != want {
			t.Fatalf("%s server %d at %s: stop=%q error=%q, want %q %q", api, i, address, result.StopReason, result.ErrorMessage, StopReasonError, want)
		}
	}
}
