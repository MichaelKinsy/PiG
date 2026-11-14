package coding

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The tool schemas are held once, not copied per prompt, and the request bodies a session sends must not notice. This test drives
// a session with three extension tools (one whose schema needs normalisation for strict providers) and the default built-in
// tools through each API that converts tools, against a recording server, and compares every request body with the file
// testdata/tool_schema_request_bodies.golden.json, which holds the length and SHA-256 of every body and was written by the code before the cut (9cd955810e; WRITE_TOOL_SCHEMA_GOLDEN=1 rewrites it). The scenario
// is fresh session (two prompts), one compaction, a third prompt, then the session resumed with one tool removed and one changed
// since the transcript was written (two prompts).
const requestBodiesGolden = "testdata/tool_schema_request_bodies.golden.json"

var (
	bodyUUID = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	bodyDate = regexp.MustCompile(`\d{4}-\d\d-\d\dT[\d:.]+Z`)
)

type requestShape struct {
	name     string
	reply    string
	provider func(base string) ai.Provider
	path     string
}

const (
	chatReply      = "data: {\"id\":\"c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
	responsesReply = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"m\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	anthropicReply = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	googleReply = "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1,\"candidatesTokenCount\":1,\"totalTokenCount\":2}}\n\n"
)

func fakeCodexToken() string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acct_1"}}`)) + ".sig"
}

// strippableRequestShapes holds the rows of the providers a Piglet strip tag leaves out of the build; the file with that
// tag's constraint adds its row (tool_schema_request_bodies_mistral_test.go).
var strippableRequestShapes []requestShape

func requestShapes() []requestShape {
	return append([]requestShape{
		{name: "openai-completions", reply: chatReply, path: "/v1", provider: func(base string) ai.Provider {
			return ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: base + "/v1", APIKey: "k", Model: "model-a"})
		}},
		{name: "openai-completions-custom-base-url", reply: chatReply, path: "/gateway/openai/v1", provider: func(base string) ai.Provider {
			return ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: base + "/gateway/openai/v1", APIKey: "k", Model: "model-a", ProviderID: "custom"})
		}},
		{name: "openai-responses", reply: responsesReply, path: "/v1", provider: func(base string) ai.Provider {
			return ai.NewOpenAIResponsesProvider(ai.OpenAIResponsesConfig{BaseURL: base + "/v1", APIKey: "k", Model: "model-a"})
		}},
		{name: "openai-codex-responses", reply: responsesReply, path: "", provider: func(base string) ai.Provider {
			return ai.NewOpenAICodexResponsesProvider(ai.OpenAICodexResponsesConfig{BaseURL: base, APIKey: fakeCodexToken(), Model: "model-a"})
		}},
		{name: "anthropic-messages", reply: anthropicReply, path: "", provider: func(base string) ai.Provider {
			return ai.NewAnthropicProvider(ai.AnthropicConfig{BaseURL: base, APIKey: "k", Model: "model-a"})
		}},
		{name: "google", reply: googleReply, path: "", provider: func(base string) ai.Provider {
			return ai.NewGoogleProvider(ai.GoogleConfig{BaseURL: base, APIKey: "k", Model: "model-a"})
		}},
	}, strippableRequestShapes...)
}

// requestToolDefinition is an extension tool; the schema of "ext_norm" needs normalisation for strict providers: an untyped
// items node, a nullable type list, a union and a const.
func requestToolDefinition(name string, schema string) extension.RegisteredTool {
	definition := registryTool(name, name, "extension tool "+name, "Run "+name)
	definition.Parameters = []byte(schema)
	return extension.RegisteredTool{Definition: definition}
}

const (
	normSchema    = `{"type":"object","properties":{"list":{"type":"array","items":{}},"maybe":{"type":["string","null"],"description":"nullable"},"either":{"anyOf":[{"type":"string"},{"type":"number"}]},"fixed":{"const":"x"},"nested":{"type":"object","properties":{"a":{"type":"integer","minimum":1}},"additionalProperties":true}},"required":["list"]}`
	plainSchema   = `{"type":"object","properties":{"path":{"type":"string","description":"a path"},"count":{"type":"integer"}},"required":["path"]}`
	changedSchema = `{"type":"object","properties":{"path":{"type":"string","description":"a path, changed since the transcript"},"count":{"type":"integer"},"extra":{"type":"boolean"}},"required":["path"]}`
)

func TestRequestBodiesAreUnchangedByHoldingToolSchemasOnce(t *testing.T) {
	got := map[string][]string{}
	bodies := map[string][]string{}
	for _, shape := range requestShapes() {
		bodies[shape.name] = recordRequestBodies(t, shape)
		for _, body := range bodies[shape.name] {
			got[shape.name] = append(got[shape.name], fmt.Sprintf("%d %x", len(body), sha256.Sum256([]byte(body))))
		}
	}
	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("WRITE_TOOL_SCHEMA_GOLDEN") != "" {
		if err := os.WriteFile(requestBodiesGolden, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	golden, err := os.ReadFile(requestBodiesGolden)
	if err != nil {
		t.Fatal(err)
	}
	var want map[string][]string
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	for name, digests := range got {
		if len(want[name]) != len(digests) {
			t.Errorf("%s: %d request bodies, golden has %d", name, len(digests), len(want[name]))
			continue
		}
		for i := range digests {
			if digests[i] != want[name][i] {
				t.Errorf("%s: request %d is %s, the golden has %s; the body:\n%s", name, i, digests[i], want[name][i], bodies[name][i])
			}
		}
	}
}

// recordRequestBodies runs the scenario against one provider and returns its request bodies in order, with what varies between
// runs (temp directories, ids, dates) replaced.
func recordRequestBodies(t *testing.T, shape requestShape) []string {
	t.Helper()
	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") == "zstd" {
			if decoder, err := zstd.NewReader(nil); err == nil {
				if plain, err := decoder.DecodeAll(body, nil); err == nil {
					body = plain
				}
				decoder.Close()
			}
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(shape.reply))
	}))
	defer server.Close()
	// A small keepRecentTokens lets a few short prompts be compacted.
	services := newTestServicesWithSettings(t, icodingagent.Settings{Compaction: &icodingagent.CompactionSettingsJSON{KeepRecentTokens: new(100.)}})
	cwd, agentDir := services.CWD(), os.Getenv("PIG_HOME")
	open := func(resume string, tools map[string]string) *Session {
		ext := extension.Extension{Tools: map[string]extension.RegisteredTool{}}
		for _, name := range []string{"ext_norm", "ext_plain", "ext_third"} {
			schema, ok := tools[name]
			if !ok {
				continue
			}
			ext.Tools[name] = requestToolDefinition(name, schema)
			ext.ToolOrder = append(ext.ToolOrder, name)
		}
		ext.InitializeToolRegistry()
		model := &ai.Model{ID: "model-a", DisplayName: "model-a", Provider: shape.provider(server.URL), Capabilities: ai.ModelCapabilities{ContextWindow: 8000}}
		session, err := NewSession(services, SessionOptions{Model: model, SystemPrompt: "You are a test agent.", ResumePath: resume, Runner: inproc.NewRunner([]extension.Extension{ext}, services.CWD())})
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	send := func(session *Session, text string) {
		t.Helper()
		if _, err := session.Send(context.Background(), text); err != nil {
			t.Fatalf("%s: send %q: %v", shape.name, text, err)
		}
	}
	first := open("", map[string]string{"ext_norm": normSchema, "ext_plain": plainSchema, "ext_third": plainSchema})
	path := first.Path()
	filler := strings.Repeat("a long prompt that fills the context. ", 40)
	send(first, "first prompt "+filler)
	send(first, "second prompt "+filler)
	// With keepRecentTokens 100 the second prompt may already have been compacted automatically; either way one compaction has run.
	if _, err := first.Compact(context.Background(), ""); err != nil && !strings.Contains(err.Error(), "Already compacted") {
		t.Fatalf("%s: compaction: %v", shape.name, err)
	}
	send(first, "third prompt")
	_ = first.Close()
	// Resumed: ext_third is gone and ext_plain has a changed schema.
	resumed := open(path, map[string]string{"ext_norm": normSchema, "ext_plain": changedSchema})
	send(resumed, "fourth prompt")
	send(resumed, "fifth prompt")
	_ = resumed.Close()
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(bodies))
	for i, body := range bodies {
		body = strings.ReplaceAll(body, cwd, "<cwd>")
		body = strings.ReplaceAll(body, agentDir, "<agent-dir>")
		body = bodyUUID.ReplaceAllString(body, "<uuid>")
		out[i] = bodyDate.ReplaceAllString(body, "<time>")
	}
	if len(out) < 5 {
		t.Fatalf("%s: only %d requests reached the server (%s)", shape.name, len(out), fmt.Sprint(shape.path))
	}
	return out
}
