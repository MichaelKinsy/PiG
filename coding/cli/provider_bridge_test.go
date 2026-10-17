package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The provider bridge is the transport a process with no extension runtime uses to reach PiG's provider implementations (pi-ai builtin APIs, D74): pi-subagents' detached runner,
// forked workers. Each case drives the real stream through an OpenAI-compatible server.
func bridgeServer(t *testing.T, hold chan struct{}) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"DO\"}}]}\n\n")
		flusher.Flush()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"NE\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(server.Close)
	return server
}

func bridgeLine(id, kind string, simple bool, baseURL string) string {
	line, _ := json.Marshal(map[string]any{
		"id": id, "type": kind, "simple": simple,
		"model": map[string]any{"provider": "bench", "id": "bench-1", "name": "Bench", "api": "openai-completions", "baseUrl": baseURL + "/v1", "reasoning": false,
			"input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}},
		"request": map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi", "timestamp": 0}}, "apiKey": "bridge-test-key"},
	})
	return string(line) + "\n"
}

type bridgeRun struct {
	stdin *io.PipeWriter
	lines chan providerBridgeReply
	done  chan int
}

func startBridge(t *testing.T) *bridgeRun {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	run := &bridgeRun{stdin: inW, lines: make(chan providerBridgeReply, 64), done: make(chan int, 1)}
	go func() {
		run.done <- runProviderBridge(inR, outW, "")
		_ = outW.Close()
	}()
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 1<<20), 64<<20)
		for scanner.Scan() {
			var reply providerBridgeReply
			if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
				t.Errorf("bridge output is not JSON: %q", scanner.Text())
			}
			run.lines <- reply
		}
		close(run.lines)
	}()
	t.Cleanup(func() { _ = run.stdin.Close() })
	return run
}

func (r *bridgeRun) until(t *testing.T, id, kind string) []providerBridgeReply {
	t.Helper()
	var seen []providerBridgeReply
	timeout := time.After(testbudget.Wait(t))
	for {
		select {
		case reply, ok := <-r.lines:
			if !ok {
				t.Fatalf("bridge output ended before %s %s; saw %d replies", id, kind, len(seen))
			}
			if reply.ID == id {
				seen = append(seen, reply)
				if reply.Type == kind {
					return seen
				}
			}
		case <-timeout:
			t.Fatalf("no %s for %s; saw %v", kind, id, seen)
		}
	}
}

func TestProviderBridgeStreamsEventsThenTheResultInOrder(t *testing.T) {
	server := bridgeServer(t, nil)
	run := startBridge(t)
	for _, simple := range []bool{false, true} {
		id := fmt.Sprintf("s-%v", simple)
		writeBridge(t, run.stdin, bridgeLine(id, "stream", simple, server.URL))
		got := run.until(t, id, "result")
		last := got[len(got)-1]
		raw, _ := json.Marshal(last.Result)
		if !strings.Contains(string(raw), "DONE") || !strings.Contains(string(raw), `"stopReason":"stop"`) {
			t.Fatalf("simple=%v: result %s", simple, raw)
		}
		if len(got) < 3 || got[0].Type != "event" {
			t.Fatalf("simple=%v: want events before the result, got %d replies, first %q", simple, len(got), got[0].Type)
		}
		for _, reply := range got[:len(got)-1] {
			if reply.Type != "event" {
				t.Fatalf("simple=%v: a %q reply before the result", simple, reply.Type)
			}
		}
	}
}

func TestProviderBridgeCancelEndsTheStreamWithAnAbortedTerminalEvent(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	server := bridgeServer(t, hold)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeLine("a", "stream", true, server.URL))
	run.until(t, "a", "event")
	writeBridge(t, run.stdin, `{"id":"a","type":"cancel"}`+"\n")
	got := run.until(t, "a", "result")
	raw, _ := json.Marshal(got[len(got)-1].Result)
	if !strings.Contains(string(raw), `"stopReason":"aborted"`) {
		t.Fatalf("a cancelled stream ends aborted, got %s", raw)
	}
	// The caller's stream (shims/provider-bridge-client.mjs) resolves its result only from a terminal event, so the provider's aborted error event must precede the result line.
	if len(got) < 2 || got[len(got)-2].Type != "event" {
		t.Fatalf("a cancelled stream delivers its terminal event before the result, got %v", got)
	}
	terminal, _ := json.Marshal(got[len(got)-2].Event)
	if !strings.Contains(string(terminal), `"type":"error"`) || !strings.Contains(string(terminal), `"reason":"aborted"`) {
		t.Fatalf("a cancelled stream's last event is the provider's aborted error event, got %s", terminal)
	}
}

func TestProviderBridgeAnInvalidRequestAnswersOnTheSameID(t *testing.T) {
	run := startBridge(t)
	writeBridge(t, run.stdin, `{"id":"bad","type":"stream","model":{"provider":"x"},"request":{}}`+"\n")
	got := run.until(t, "bad", "error")
	if got[len(got)-1].Error == "" {
		t.Fatal("an invalid model must answer with an error message")
	}
	writeBridge(t, run.stdin, `{"id":"w","type":"wobble"}`+"\n")
	if e := run.until(t, "w", "error"); !strings.Contains(e[0].Error, "unknown request type") {
		t.Fatalf("got %v", e)
	}
	// The request line has one strict shape: an unknown field is an error on the same id, not ignored.
	writeBridge(t, run.stdin, `{"id":"u","type":"stream","modelId":{"provider":"x"}}`+"\n")
	if e := run.until(t, "u", "error"); !strings.Contains(e[0].Error, "invalid request line") || !strings.Contains(e[0].Error, "modelId") {
		t.Fatalf("got %v", e)
	}
}

// A request line carries the whole transcript: Pi's JSONL framing (rpc/jsonl.ts) has no line limit, so a line far above any read buffer is served, and a blank line between records is skipped.
func TestProviderBridgeServesARequestLineLargerThanTheReadBuffer(t *testing.T) {
	run := startBridge(t)
	writeBridge(t, run.stdin, "\n")
	writeBridge(t, run.stdin, `{"id":"big","type":"stream","model":{"provider":"x"},"request":{"pad":"`+strings.Repeat("a", 3<<20)+`"}}`+"\n")
	if got := run.until(t, "big", "error"); got[len(got)-1].Error == "" || strings.Contains(got[len(got)-1].Error, "invalid request line") {
		t.Fatalf("a large line must be decoded and answered by the model check, got %v", got)
	}
}

func TestProviderBridgeEndsWithItsStarterAndAbortsWhatIsInFlight(t *testing.T) {
	hold := make(chan struct{})
	defer close(hold)
	server := bridgeServer(t, hold)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeLine("x", "stream", true, server.URL))
	run.until(t, "x", "event")
	_ = run.stdin.Close()
	select {
	case code := <-run.done:
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the bridge outlived its stdin")
	}
}

func writeBridge(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := io.WriteString(w, line); err != nil {
		t.Fatal(err)
	}
}

// bridgeCallbackLine is bridgeLine with the caller's callbacks named, as the client sends them.
func bridgeCallbackLine(id, baseURL string, callbacks map[string]bool) string {
	var line map[string]any
	_ = json.Unmarshal([]byte(bridgeLine(id, "stream", true, baseURL)), &line)
	for name := range callbacks {
		line[name] = true
	}
	encoded, _ := json.Marshal(line)
	return string(encoded) + "\n"
}

// The caller runs onPayload and onResponse; the provider awaits each answer, sends the payload onPayload returned, and calls onResponse with the response (pi-ai's onPayload replaces the payload).
func TestProviderBridgeRunsTheCallersCallbacksAroundTheProviderRequest(t *testing.T) {
	var mu sync.Mutex
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(data)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeCallbackLine("cb", server.URL, map[string]bool{"onPayload": true, "onResponse": true}))
	var order []string
	for len(order) < 2 {
		var reply providerBridgeReply
		for _, seen := range run.until(t, "cb", "callback") {
			if seen.Type == "callback" && seen.CallbackID > len(order) {
				reply = seen
			}
		}
		order = append(order, reply.Callback)
		answer := map[string]any{"id": "cb", "type": "callbackResult", "callbackId": reply.CallbackID}
		if reply.Callback == "onPayload" {
			answer["result"] = map[string]any{"defined": true, "value": map[string]any{"replaced": "payload"}}
		}
		line, _ := json.Marshal(answer)
		writeBridge(t, run.stdin, string(line)+"\n")
	}
	run.until(t, "cb", "result")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "onPayload,onResponse" {
		t.Fatalf("callbacks ran in order %v, want onPayload before the request and onResponse after it", order)
	}
	if body != `{"replaced":"payload"}` {
		t.Fatalf("provider request body %q: the caller's onPayload answer did not reach it", body)
	}
}

// A callback that fails rejects that request: the provider sends nothing and the stream ends in error carrying the caller's message.
func TestProviderBridgeACallbackFailureRejectsTheRequest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
	}))
	t.Cleanup(server.Close)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeCallbackLine("fail", server.URL, map[string]bool{"onPayload": true}))
	var reply providerBridgeReply
	for _, seen := range run.until(t, "fail", "callback") {
		reply = seen
	}
	line, _ := json.Marshal(map[string]any{"id": "fail", "type": "callbackResult", "callbackId": reply.CallbackID, "failure": "caller refused"})
	writeBridge(t, run.stdin, string(line)+"\n")
	got := run.until(t, "fail", "result")
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), "caller refused") || !strings.Contains(string(raw), `"stopReason":"error"`) {
		t.Fatalf("a failing callback must end the stream in error with its message, got %s", raw)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("the provider sent %d request(s) after its callback failed", requests)
	}
}

// A callback outstanding when the stream is cancelled returns the cancellation: the stream ends aborted, nothing waits on the caller.
func TestProviderBridgeCancelReleasesAnOutstandingCallback(t *testing.T) {
	server := bridgeServer(t, nil)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeCallbackLine("oc", server.URL, map[string]bool{"onPayload": true}))
	run.until(t, "oc", "callback")
	writeBridge(t, run.stdin, `{"id":"oc","type":"cancel"}`+"\n")
	got := run.until(t, "oc", "result")
	raw, _ := json.Marshal(got[len(got)-1].Result)
	if !strings.Contains(string(raw), `"stopReason":"aborted"`) {
		t.Fatalf("a stream cancelled with a callback outstanding ends aborted, got %s", raw)
	}
}

// An onPayload that returns undefined keeps the provider's own payload (pi-ai: a defined return replaces it).
func TestProviderBridgeAnUndefinedOnPayloadAnswerKeepsThePayload(t *testing.T) {
	var mu sync.Mutex
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(data)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	run := startBridge(t)
	writeBridge(t, run.stdin, bridgeCallbackLine("u", server.URL, map[string]bool{"onPayload": true}))
	var reply providerBridgeReply
	for _, seen := range run.until(t, "u", "callback") {
		reply = seen
	}
	line, _ := json.Marshal(map[string]any{"id": "u", "type": "callbackResult", "callbackId": reply.CallbackID, "result": map[string]any{"defined": false}})
	writeBridge(t, run.stdin, string(line)+"\n")
	run.until(t, "u", "result")
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, `"model":"bench-1"`) || strings.Contains(body, "replaced") {
		t.Fatalf("an undefined onPayload answer must leave the provider payload, got %q", body)
	}
}
