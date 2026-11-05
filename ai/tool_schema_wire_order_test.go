package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// orderedProbeSchema is the tool `enum_probe` as a Pi extension writes it (pi-schema `obj({ zeta: str({description}), mode: StringEnum([...], {...}), alpha: opt(num()) })`): the
// properties are not in byte order and the StringEnum member is in StringEnum's order. It reaches ToolSchema as JSON, the way an extension's or an eval's tool does.
func orderedProbeSchema(t testing.TB) ToolSchema {
	t.Helper()
	parameters := chordjson.ObjectOf(
		"type", "object",
		"properties", chordjson.ObjectOf(
			"zeta", chordjson.ObjectOf("type", "string", "description", "z"),
			"mode", StringEnum([]string{"fast", "slow"}, &StringEnumOptions{Description: "how", Default: "fast"}),
			"alpha", chordjson.ObjectOf("type", "number"),
		),
		"required", []string{"zeta", "mode"},
		"additionalProperties", false,
	)
	encoded, err := json.Marshal(map[string]any{"name": "enum_probe", "description": "probe", "parameters": parameters})
	if err != nil {
		t.Fatal(err)
	}
	var tool ToolSchema
	if err := json.Unmarshal(encoded, &tool); err != nil {
		t.Fatal(err)
	}
	return tool
}

// captureToolsBody sends one request for the catalog model to a local server that refuses it, and returns the raw request body: the bytes Pi's provider module POSTs for the same
// model, which the oracle test in test/pipeline-diff compares.
func captureToolsBody(t *testing.T, providerID, modelID string, tools []ToolSchema) []byte {
	t.Helper()
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case bodies <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"capture"}}`)
	}))
	defer server.Close()
	model := GetBuiltinModel(providerID, modelID)
	if model == nil {
		t.Fatalf("no catalog model %s/%s", providerID, modelID)
	}
	model.ProviderMeta.BaseURL = server.URL
	stream, err := Stream(context.Background(), model, Context{Messages: []Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}, Tools: tools}, StreamOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	select {
	case body := <-bodies:
		return body
	default:
		t.Fatalf("%s/%s sent no request", providerID, modelID)
		return nil
	}
}

// toolsMember returns the raw bytes of a top-level member of a request body.
func toolsMember(t *testing.T, body []byte, member string) string {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("request body %s: %v", body, err)
	}
	raw, ok := members[member]
	if !ok {
		t.Fatalf("request body has no %q member: %s", member, body)
	}
	return string(raw)
}

// providerToolsWire is each provider's `tools` member for orderedProbeSchema, byte for byte what Pi's converter of that API writes (probed with Pi 1.5.0's providers against a
// local server): the schema's members keep the order its author wrote, StringEnum's included.
var providerToolsWire = []struct {
	api, provider, model, member, want string
}{
	{"anthropic-messages", "anthropic", "claude-sonnet-4-5", "tools",
		`[{"name":"enum_probe","description":"probe","eager_input_streaming":true,"input_schema":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"]},"cache_control":{"type":"ephemeral"}}]`},
	{"openai-responses", "openai", "gpt-5", "tools",
		`[{"type":"function","name":"enum_probe","description":"probe","parameters":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false},"strict":false}]`},
	{"azure-openai-responses", "azure", "gpt-4.1", "tools",
		`[{"type":"function","name":"enum_probe","description":"probe","parameters":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false},"strict":false}]`},
	{"openai-completions", "groq", "llama-3.1-8b-instant", "tools",
		`[{"type":"function","function":{"name":"enum_probe","description":"probe","parameters":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false},"strict":false}}]`},
	{"mistral-conversations", "mistral", "devstral-medium-latest", "tools",
		`[{"type":"function","function":{"name":"enum_probe","description":"probe","parameters":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false},"strict":false}}]`},
	{"google-generative-ai", "google", "gemini-2.5-flash", "tools",
		`[{"functionDeclarations":[{"name":"enum_probe","description":"probe","parametersJsonSchema":{"type":"object","properties":{"zeta":{"type":"string","description":"z"},"mode":{"type":"string","enum":["fast","slow"],"description":"how","default":"fast"},"alpha":{"type":"number"}},"required":["zeta","mode"],"additionalProperties":false}}]}]`},
}

// A tool whose schema carries StringEnum reaches each provider's request with its members in the order the author wrote them, as in Pi. Pi writes a JavaScript object's members in
// insertion order; before StringEnum was an ordered object the schema was a Go map, and every provider wrote it with its members sorted by name.
func TestToolSchemaKeepsAuthoredOrderInProviderRequests(t *testing.T) {
	for _, c := range providerToolsWire {
		t.Run(c.api, func(t *testing.T) {
			body := captureToolsBody(t, c.provider, c.model, []ToolSchema{orderedProbeSchema(t)})
			if got := toolsMember(t, body, c.member); got != c.want {
				t.Errorf("%s %s\n got: %s\nwant: %s", c.provider, c.member, got, c.want)
			}
		})
	}
}
