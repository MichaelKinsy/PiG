package ai_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/bodyreadhook/readbarrier"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// responseProbe is one scripted provider response: the SSE body a model of the given provider is answered with, and the stream options.
type responseProbe struct {
	Provider  string `json:"provider"`
	ModelID   string `json:"modelId,omitempty"`
	Reasoning bool   `json:"reasoning"`
	APIKey    string `json:"apiKey,omitempty"`
	SSE       string `json:"sse"`
	// Status and ContentType, when set, answer the request instead of 200 and text/event-stream (an error response).
	Status      int               `json:"status,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
	Options     map[string]any    `json:"options,omitempty"`
	SessionID   string            `json:"sessionId,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	// Sequence answers the successive attempts of one request (the last repeats); the comparison then includes the number of attempts.
	Sequence []responseProbe `json:"sequence,omitempty"`
	// ResponseHeaders are sent with the response.
	ResponseHeaders map[string]string `json:"responseHeaders,omitempty"`
	// AbortAfterMs keeps the response open after its body and aborts the request that many milliseconds later.
	AbortAfterMs int `json:"abortAfterMs,omitempty"`
	// CheckRequest compares the request path, method and headers instead of the events and the message.
	CheckRequest bool `json:"-"`
	// MessageOnly compares the final message without the stream events: Pi's delta events carry a mistyped delta raw, which a Go string cannot hold.
	MessageOnly bool `json:"-"`
	// StrayAt names the side ("pi" or "pig") whose server a client other than the provider's requests before the probe runs: a process polling a port
	// that another server held before reaches whichever server holds it now (on the Linux lab host a Go client outside the tests sends GET /readyz to
	// 127.0.0.1 ports).
	StrayAt string `json:"strayAt,omitempty"`
}

// checkResponses runs every probe through the pinned pi-ai API module (its fetch answers with the SSE body) and through PiG (a local server does), and
// requires the stream events and the final assistant message to be equal as parsed JSON, partial snapshots, the timestamp and the duration aside.
func checkResponses(t *testing.T, module, apiName string, api ai.API, run func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error), probes []responseProbe) {
	t.Helper()
	compareResponses(t, module, apiName, api, run, probes, true)
}

// checkResponsesAllowEmpty is checkResponses for probes whose messages are errors with no content.
func checkResponsesAllowEmpty(t *testing.T, module, apiName string, api ai.API, run func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error), probes []responseProbe) {
	t.Helper()
	compareResponses(t, module, apiName, api, run, probes, false)
}

// generatedToolCallID matches the id D120 generates for a streamed tool call that carries none.
var generatedToolCallID = regexp.MustCompile(`"id":"[^"]*_\d{13}_\d+"`)

func compareResponses(t *testing.T, module, apiName string, api ai.API, run func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error), probes []responseProbe, needContent bool) {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/api_response.mjs", pigversion.UpstreamPackageVersion, module, apiName)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Events   []map[string]any `json:"events"`
		Message  map[string]any   `json:"message"`
		Request  map[string]any   `json:"request"`
		Attempts int              `json:"attempts"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, answered := 0, 0
	classes := map[string]int{}
	barrier := readbarrier.New(t)
	for i, p := range probes {
		if content, _ := want[i].Message["content"].([]any); len(content) > 0 {
			answered++
		}
		var gotRequest map[string]any
		attempts := 0
		// abortAtRead aborts the request once the client has read the whole held-open body and reads the socket again (readbarrier).
		var abortAtRead context.CancelFunc
		client := &probeClient{pending: map[string]int{}}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !client.sent(r) {
				http.Error(w, "not a request of this probe's client", http.StatusMisdirectedRequest)
				return
			}
			headers := map[string]any{}
			for name, values := range r.Header {
				headers[strings.ToLower(name)] = strings.Join(values, ", ")
			}
			gotRequest = map[string]any{"path": r.URL.RequestURI(), "method": r.Method, "headers": headers}
			step := p
			if len(p.Sequence) > 0 {
				step = p.Sequence[min(attempts, len(p.Sequence)-1)]
			}
			attempts++
			w.Header().Set("Content-Type", cmp.Or(step.ContentType, "text/event-stream"))
			for name, value := range step.ResponseHeaders {
				w.Header().Set(name, value)
			}
			if step.Status != 0 {
				w.WriteHeader(step.Status)
			}
			_, _ = w.Write([]byte(step.SSE))
			if p.AbortAfterMs > 0 {
				w.(http.Flusher).Flush()
				if abortAtRead != nil && barrier.WaitReading(r.Context(), r.RemoteAddr) {
					abortAtRead()
				}
				<-r.Context().Done()
			}
		})
		var server *httptest.Server
		if p.AbortAfterMs > 0 && strings.HasPrefix(apiName, "google") {
			server = barrier.Server(t, handler)
		} else {
			server = httptest.NewServer(handler)
		}
		if p.StrayAt == "pig" {
			strayRequest(t, server.URL)
		}
		id := p.ModelID
		if id == "" {
			id = "m"
		}
		model := &ai.Model{ID: id, DisplayName: "m", Input: []string{"text"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384, InputCostPer1M: 1, OutputCostPer1M: 2, CacheReadCostPer1M: 0.5, CacheWriteCostPer1M: 3}, ProviderMeta: ai.ProviderMetadata{API: api, ProviderID: p.Provider, BaseURL: server.URL, Reasoning: p.Reasoning}}
		transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}})
		options := ai.StreamOptions{APIKey: "k"}
		if p.APIKey != "" {
			options.APIKey = p.APIKey
		}
		options.SessionID = p.SessionID
		if p.Headers != nil {
			options.Headers = ai.ProviderHeaders{}
			for name, value := range p.Headers {
				options.Headers[name] = &value
			}
		}
		if value, ok := p.Options["maxRetries"].(float64); ok {
			options.MaxRetries = new(int(value))
		}
		if value, ok := p.Options["maxRetryDelayMs"].(float64); ok {
			options.MaxRetryDelayMs = new(int(value))
		}
		if p.Options["transport"] == "sse" {
			options.Transport = ai.TransportSSE
		}
		runCtx := client.trace(requestvalidity.Allow(t.Context()))
		if p.AbortAfterMs > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithCancel(runCtx)
			defer cancel()
			// Pi reads and parses the in-memory body in microtasks, before its abort timer can fire. PiG reads a real socket, so a wall-clock
			// abort could land before the client has read the whole prefix on a loaded host: the request is aborted when the client asks for
			// more of the held-open body after reading all of it, by which time every complete record has been handled.
			// The Google adapters reject a custom fetch (google-generative-ai.ts:36), so the server aborts them through the read barrier instead.
			if strings.HasPrefix(apiName, "google") {
				abortAtRead = cancel
			} else {
				options.Fetch = &http.Client{Transport: drainCancelTransport{cancel: cancel, size: len(p.SSE)}}
			}
		}
		stream, err := run(runCtx, model, transcript, options)
		var events []map[string]any
		var message map[string]any
		if err != nil {
			message = map[string]any{"thrown": err.Error()}
		} else {
			for event := range stream.Events(t.Context()) {
				raw, _ := json.Marshal(event)
				var decoded map[string]any
				_ = json.Unmarshal(raw, &decoded)
				delete(decoded, "partial")
				delete(decoded, "message")
				delete(decoded, "error")
				events = append(events, decoded)
			}
			raw, _ := json.Marshal(stream.Result())
			_ = json.Unmarshal(raw, &message)
			delete(message, "timestamp")
			delete(message, "durationMs")
		}
		server.Close()
		gotParts := map[string]any{"events": events, "message": message}
		wantParts := map[string]any{"events": want[i].Events, "message": want[i].Message}
		if p.MessageOnly {
			delete(gotParts, "events")
			delete(wantParts, "events")
		}
		if len(p.Sequence) > 0 {
			gotParts["attempts"], wantParts["attempts"] = attempts, want[i].Attempts
		}
		gotJSON, _ := json.Marshal(gotParts)
		// D120: PiG gives a tool call streamed without an id a generated "<name>_<epoch ms>_<counter>" id where Pi stores "".
		// The Google adapters generate the id themselves in Pi (google-generative-ai.ts:195-200), so both sides carry a generated id there.
		if !strings.HasPrefix(apiName, "google") {
			gotJSON = generatedToolCallID.ReplaceAll(gotJSON, []byte(`"id":""`))
		}
		wantJSON, _ := json.Marshal(wantParts)
		// A generated tool call id carries the clock (`name_<Date.now()>_<n>`).
		gotJSON, wantJSON = generatedIDClock.ReplaceAll(gotJSON, []byte("_CLOCK_N")), generatedIDClock.ReplaceAll(wantJSON, []byte("_CLOCK_N"))
		if p.CheckRequest {
			gotJSON, _ = json.Marshal(map[string]any{"request": comparableRequest(gotRequest)})
			wantJSON, _ = json.Marshal(map[string]any{"request": comparableRequest(want[i].Request)})
		}
		if !bytes.Equal(gotJSON, wantJSON) {
			if p.CheckRequest {
				gotHeaders, _ := comparableRequest(gotRequest)["headers"].(map[string]any)
				wantHeaders, _ := comparableRequest(want[i].Request)["headers"].(map[string]any)
				var diff []string
				for name, value := range wantHeaders {
					if gotHeaders[name] != value {
						diff = append(diff, fmt.Sprintf("%s: Pi %v PiG %v", name, value, gotHeaders[name]))
					}
				}
				for name, value := range gotHeaders {
					if _, ok := wantHeaders[name]; !ok {
						diff = append(diff, fmt.Sprintf("%s: Pi none PiG %v", name, value))
					}
				}
				slices.Sort(diff)
				classes[p.Provider+" key="+p.APIKey+" session="+fmt.Sprint(p.SessionID != "")+" custom="+fmt.Sprint(p.Headers)+" => "+strings.Join(diff, "; ")]++
			}
			if failures++; failures <= 4 && !p.CheckRequest {
				t.Errorf("probe %d reasoning=%v:\nSSE %s\n%s", i, p.Reasoning, strings.ReplaceAll(fmt.Sprint(p.SSE, p.Sequence), "\n", "⏎"), firstDifference(gotJSON, wantJSON))
			}
		}
	}
	for class, count := range classes {
		t.Logf("%3d  %s", count, class)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	// A script that Pi never answers would match PiG's errors vacuously.
	if needContent && answered*10 < len(probes)*5 {
		t.Fatalf("Pi built content for only %d of %d probes", answered, len(probes))
	}
}

// firstDifference shows each side from the first byte where the two encodings differ.
func firstDifference(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-60)
	return fmt.Sprintf(" PiG …%.400s\n Pi  …%.400s", got[from:], want[from:])
}

// probeClient tells the requests of the client a probe drives from any other request its server receives. A loopback port belongs to the host: a
// process polling a port that another server held before reaches whichever server holds it now (on the Linux lab host a Go client outside the tests sends
// GET /readyz to 127.0.0.1 ports), and taken for an attempt it would get a scripted response. The transport reports each connection it takes for a
// request (GotConn) before it writes the request there, so the server knows every request of the client's by the time it arrives.
type probeClient struct {
	mu sync.Mutex
	// pending counts the requests announced on each connection ("client address>server address") that the server has not received yet.
	pending map[string]int
}

// trace has the transport announce the requests made with ctx.
func (c *probeClient) trace(ctx context.Context) context.Context {
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		c.mu.Lock()
		c.pending[info.Conn.LocalAddr().String()+">"+info.Conn.RemoteAddr().String()]++
		c.mu.Unlock()
	}})
}

// sent reports whether the client announced request on its connection, and takes the announcement.
func (c *probeClient) sent(request *http.Request) bool {
	local, _ := request.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if local == nil {
		return false
	}
	key := request.RemoteAddr + ">" + local.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending[key] == 0 {
		return false
	}
	c.pending[key]--
	return true
}

// strayRequest is the request a loopback port poller sends the server that now holds the port, from a client of its own.
func strayRequest(t *testing.T, base string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	response, err := client.Get(base + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

// comparableRequest drops the headers that name the client library or the transport (user agent, the SDKs' x-stainless telemetry, length, host,
// encoding), which differ between a Node SDK and Go's HTTP client by design.
func comparableRequest(request map[string]any) map[string]any {
	headers, _ := request["headers"].(map[string]any)
	kept := map[string]any{}
	for name, value := range headers {
		switch {
		case strings.HasPrefix(name, "x-stainless-"), name == "user-agent", name == "x-goog-api-client", name == "accept-encoding", name == "content-length", name == "host", name == "connection", name == "accept-language", name == "sec-fetch-mode":
		default:
			kept[name] = value
		}
	}
	return map[string]any{"path": request["path"], "method": request["method"], "headers": kept}
}

var generatedIDClock = regexp.MustCompile(`_\d{13}_\d+`)

// drainCancelTransport cancels a request once its client has read size bytes of the response body and calls Read again.
type drainCancelTransport struct {
	cancel context.CancelFunc
	size   int
}

func (d drainCancelTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err == nil {
		response.Body = &drainCancelBody{ReadCloser: response.Body, cancel: d.cancel, left: d.size}
	}
	return response, err
}

type drainCancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	left   int
}

func (b *drainCancelBody) Read(buffer []byte) (int, error) {
	if b.left <= 0 {
		b.cancel()
	}
	n, err := b.ReadCloser.Read(buffer)
	b.left -= n
	return n, err
}
