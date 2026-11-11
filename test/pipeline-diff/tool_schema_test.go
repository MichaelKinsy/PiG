package pipelinediff

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	_ "github.com/MichaelKinsy/PiG/ai/providers/bedrock"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// wireParameters is a tool's parameters as an extension's JSON carries them: properties not in byte order, and integer-like keys, which a JavaScript object enumerates first.
const wireParameters = `{"type":"object","required":["b","a","2"],"properties":{"b":{"type":"string"},"a":{"description":"x","type":"number"},"2":{"type":"boolean"},"1":{"type":"integer"}}}`

// toolSchemaTargets are the API kinds that carry a tool's JSON Schema in their request, with the catalog model each is sent for.
var toolSchemaTargets = []struct{ name, provider, model, member string }{
	{"anthropic", "anthropic", "claude-sonnet-4-5", "tools"},
	{"openai-responses", "openai", "gpt-5", "tools"},
	{"azure-responses", "azure", "gpt-4.1", "tools"},
	{"completions-groq", "groq", "llama-3.1-8b-instant", "tools"},
	{"google", "google", "gemini-2.5-flash", "tools"},
	{"mistral", "mistral", "devstral-medium-latest", "tools"},
	{"bedrock", "amazon-bedrock", "anthropic.claude-sonnet-4-5-20250929-v1:0", "toolConfig"},
}

// piToolBodies runs pi-tools.mjs: Pi's request body for every target and case.
func piToolBodies(t *testing.T) map[string]string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	input, err := json.Marshal(map[string]json.RawMessage{"wire": json.RawMessage(wireParameters)})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), node, filepath.Join(testenv.ModuleRoot(t), "test", "pipeline-diff", "pi-tools.mjs"), piRoot(t))
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pi-tools.mjs: %v\n%s", err, stderr.String())
	}
	var bodies map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &bodies); err != nil {
		t.Fatalf("pi-tools.mjs output %q: %v", stdout.String(), err)
	}
	return bodies
}

// pigToolBody is the request body PiG sends the catalog model for tools, captured at a local server that refuses it.
func pigToolBody(t *testing.T, provider, modelID string, toolSet []ai.ToolSchema) []byte {
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
	model := ai.GetBuiltinModel(provider, modelID)
	if model == nil {
		t.Fatalf("no catalog model %s/%s", provider, modelID)
	}
	model.ProviderMeta.BaseURL = server.URL
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	stream, err := ai.Stream(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}}, Tools: toolSet}, ai.StreamOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	stream.Result()
	select {
	case body := <-bodies:
		return body
	default:
		t.Fatalf("%s/%s sent no request", provider, modelID)
		return nil
	}
}

func rawMember(t *testing.T, body []byte, member string) string {
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

// decodedTool is a tool as an extension registers it: its JSON reaches ai.ToolSchema through the decode every extension tool takes (coding.newBridgeTool).
func decodedTool(t *testing.T, name, parameters string) ai.ToolSchema {
	t.Helper()
	var tool ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":`+strconvQuote(name)+`,"description":"probe","parameters":`+parameters+`}`), &tool); err != nil {
		t.Fatal(err)
	}
	return tool
}

func strconvQuote(s string) string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}

// The tool schema a provider module writes into its request is the schema as its author wrote it: Pi writes a JavaScript object's members in insertion order (integer-like keys
// first), and PiG must send the same bytes for a StringEnum property, a multi-property object, the built-in tools and a schema that arrives as JSON from an extension. Pi is the
// oracle for every API kind that carries tool schemas.
func TestToolSchemaBytesMatchPi(t *testing.T) {
	for _, name := range []string{"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "AWS_BEDROCK_SKIP_AUTH"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_BEDROCK_FORCE_HTTP1", "1")
	pi := piToolBodies(t)

	enumParameters, err := json.Marshal(chordjson.ObjectOf(
		"type", "object",
		"properties", chordjson.ObjectOf(
			"zeta", chordjson.ObjectOf("type", "string", "description", "z"),
			"mode", ai.StringEnum([]string{"fast", "slow"}, &ai.StringEnumOptions{Description: "how", Default: "fast"}),
			"alpha", chordjson.ObjectOf("type", "number"),
		),
		"required", []string{"zeta", "mode"},
		"additionalProperties", false,
	))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]ai.ToolSchema{
		"enum": {decodedTool(t, "enum_probe", string(enumParameters))},
		"builtin": {
			(&tools.ReadTool{}).Schema(), (&tools.BashTool{}).Schema(), (&tools.EditTool{}).Schema(), (&tools.WriteTool{}).Schema(),
			(&tools.GrepTool{}).Schema(), (&tools.FindTool{}).Schema(), (&tools.LsTool{}).Schema(),
		},
		"wire": {decodedTool(t, "wire_probe", wireParameters)},
	}
	for _, target := range toolSchemaTargets {
		for name, toolSet := range cases {
			t.Run(target.name+"/"+name, func(t *testing.T) {
				want := rawMember(t, []byte(pi[target.name+"/"+name]), target.member)
				got := rawMember(t, pigToolBody(t, target.provider, target.model, toolSet), target.member)
				if got != want {
					t.Errorf("%s of %s\n pig: %s\n  pi: %s", target.member, target.name, got, want)
				}
			})
		}
	}
}
