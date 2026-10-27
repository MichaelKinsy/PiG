package bedrock

import (
	"bufio"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

func assertShapeJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("request shape\n got: %s\nwant: %s", got, want)
	}
}

func cloneGeneratedModel(t *testing.T, spec string) *ai.GeneratedModel {
	t.Helper()
	provider, id, ok := strings.Cut(spec, "/")
	if !ok {
		t.Fatalf("expected provider/model, got %q", spec)
	}
	model := *mustGeneratedModel(t, provider, id)
	model.Compat = cloneModelCompat(model.Compat)
	return &model
}

func rejectingProviderServer(t *testing.T) (*httptest.Server, <-chan capturedProviderRequest) {
	t.Helper()
	requests := make(chan capturedProviderRequest, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests <- capturedProviderRequest{header: request.Header.Clone(), host: request.Host, body: body}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("x-amzn-errortype", "ValidationException")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"expected test rejection"}`))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

// providerEventRecorder is the observer the upstream tests push into. The upstream callbacks yield once
// (`await Promise.resolve()`); a blocking Go call has the same ordering effect.
type providerEventRecorder struct {
	mu     sync.Mutex
	events []any
	models []*ai.Model
}

func (r *providerEventRecorder) observe(_ context.Context, data any, model *ai.Model) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, data)
	r.models = append(r.models, model)
	return nil
}

func (r *providerEventRecorder) snapshot() ([]any, []*ai.Model) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]any(nil), r.events...), append([]*ai.Model(nil), r.models...)
}

func userTranscript(text string) ai.TranscriptContext {
	return ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(text), Timestamp: 1}}})
}

func providerWireTranscript() ai.TranscriptContext {
	return ai.NormalizeContext(ai.Context{
		SystemPrompt: "system",
		Messages:     []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}},
	})
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// streamingWriteCall is write arguments with size bytes of file content and their JSON text.
func streamingWriteCall(t testing.TB, size int) (arguments ai.JsonObject, text string) {
	t.Helper()
	content := streamingArgumentContent[:size]
	arguments = ai.JsonObject{"path": "/src/file.ts", "content": content}
	// JSON.stringify keeps the members in the order they were written: path first.
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return arguments, `{"path":"/src/file.ts","content":` + string(encoded) + `}`
}

// piCodingAgentPackage is Pi 0.87.1's pinned pi-coding-agent package.
func piCodingAgentPackage(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent"))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mustTools(t *testing.T, raw string) []ai.ToolSchema {
	t.Helper()
	var tools []ai.ToolSchema
	if err := json.Unmarshal([]byte(raw), &tools); err != nil {
		t.Fatal(err)
	}
	return tools
}

func requestHeaderOptions() ai.StreamOptions {
	return ai.StreamOptions{Headers: ai.ProviderHeaders{
		"x-configured": new("request"),
		"X-Delete":     nil,
		"X-Request":    new("yes"),
	}}
}

func mustGeneratedModel(t *testing.T, provider, modelID string) *ai.GeneratedModel {
	t.Helper()
	model, ok := ai.LookupModelExact(provider + "/" + modelID)
	if !ok {
		t.Fatalf("missing model %s/%s", provider, modelID)
	}
	return model
}

func expectNoStreamState(t *testing.T, message *ai.AssistantMessage, label string) {
	t.Helper()
	encoded, err := json.Marshal(message.Observe())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Content []map[string]json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, block := range decoded.Content {
		var kind string
		_ = json.Unmarshal(block["type"], &kind)
		for key := range block {
			if !slices.Contains(streamingBlockKeys[kind], key) {
				t.Fatalf("%s: %s.%s is stream state in a partial message", label, kind, key)
			}
		}
	}
}

func assertRetryableTransportResult(t *testing.T, result *ai.AssistantMessage, category string) {
	t.Helper()
	if result.StopReason != ai.StopReasonError || !strings.Contains(result.ErrorMessage, category) {
		t.Fatalf("result = reason %q error %q, want transport category %q", result.StopReason, result.ErrorMessage, category)
	}
	if !ai.IsRetryableAssistantError(*result) {
		t.Fatalf("transport result is not retryable: %q", result.ErrorMessage)
	}
}

func streamingDeltas(text string, size int) []string {
	var deltas []string
	for index := 0; index < len(text); index += size {
		deltas = append(deltas, text[index:min(index+size, len(text))])
	}
	return deltas
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func equalArguments(a, b ai.JsonObject) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right) || reflectJSONEqual(left, right)
}

func compromisedTLSIntermediate(t *testing.T) *x509.Certificate {
	t.Helper()
	for _, certificate := range tlsdistrusttest.CrossSigned(t) {
		if tlsdistrusttest.IsTLS(certificate) {
			return certificate
		}
	}
	t.Fatal("no TLS intermediate in testdata")
	return nil
}

func closeHTTPConnection(t *testing.T, writer http.ResponseWriter) (net.Conn, *bufio.ReadWriter) {
	t.Helper()
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		t.Fatal("test server does not support hijacking")
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("hijack: %v", err)
	}
	return connection, buffered
}

type capturedProviderRequest struct {
	header http.Header
	host   string
	body   []byte
}

func assertUsageJSON(t *testing.T, usage ai.Usage, want string) {
	t.Helper()
	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	var got, expected map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("usage = %s\nwant  %s", encoded, want)
	}
}

func assertPreparedProviderHeaders(t *testing.T, request capturedProviderRequest) {
	t.Helper()
	if got := request.header.Get("X-Configured"); got != "request" {
		t.Fatalf("X-Configured = %q, want request", got)
	}
	if got := request.header.Get("X-Delete"); got != "" {
		t.Fatalf("X-Delete = %q, want deleted", got)
	}
	if got := request.header.Get("X-Request"); got != "yes" {
		t.Fatalf("X-Request = %q, want yes", got)
	}
}

// assertEventModels checks the model argument of every observed event. Adapters that keep the selected model
// (ModelMetadata) hand it back unchanged, as upstream's toEqual([model, ...]) requires.
func assertEventModels(t *testing.T, got []*ai.Model, want *ai.Model, count int) {
	t.Helper()
	if len(got) != count {
		t.Fatalf("observed %d models, want %d", len(got), count)
	}
	for i, model := range got {
		if !reflect.DeepEqual(model, want) {
			t.Fatalf("event %d model = %#v, want %#v", i, model, want)
		}
	}
}

// allocatedBytes is what work allocates on the heap.
func allocatedBytes(work func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	work()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// streamingArgumentContent is the file content of a write call: 20,000 lines with a backslash and a quote in each.
var streamingArgumentContent = func() string {
	var content strings.Builder
	for line := range 20_000 {
		fmt.Fprintf(&content, "const line%d = \"value %d\"; // \\ done\n", line, line)
	}
	return content.String()
}()

// streamingBlockKeys are the members a content block may carry; anything else is stream state leaking into partial messages.
var streamingBlockKeys = map[string][]string{
	"text":     {"type", "text", "textSignature"},
	"thinking": {"type", "thinking", "thinkingSignature", "redacted"},
	"toolCall": {"type", "id", "name", "arguments", "argumentsError", "thoughtSignature", "namespace"},
}

func reflectJSONEqual(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && fmt.Sprint(left) == fmt.Sprint(right)
}

// cloneModelCompat copies compat through JSON, as the catalog's own clone does.
func cloneModelCompat(in *ai.ModelCompat) *ai.ModelCompat {
	if in == nil {
		return nil
	}
	data, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	var out ai.ModelCompat
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return &out
}
