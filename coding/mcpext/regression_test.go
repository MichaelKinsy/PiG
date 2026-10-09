package mcpext_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
)

// Not upstream tests: regression guards for the Go realization.

// Closing a connection aborts a connect in flight instead of waiting for the
// server's answer or the request timeout.
func TestSessionShutdownReturnsWhileAServerIsStillConnecting(t *testing.T) {
	host := newFakeHost()
	entry := mcpext.McpServerEntry{Name: "slow", Config: extension.McpServerConfig{URL: "http://unused.invalid"}, Source: "test"}
	ext := mcpext.New(host, mcpext.Options{
		LoadConfig: func(context.Context) mcpext.LoadedMcpConfig {
			return mcpext.LoadedMcpConfig{Servers: []mcpext.McpServerEntry{entry}}
		},
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := mcptest.NewInMemoryTransportPair()
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
		LogPath:     filepath.Join(t.TempDir(), "mcp.log"),
	})
	ctx := eventContext(t.TempDir(), &notifications{})
	ext.SessionStart(ctx)
	waitFor(t, "the connection to start", func() bool {
		servers := ext.Servers()
		return len(servers) == 1 && servers[0].Connection != nil
	})
	done := make(chan struct{})
	go func() {
		ext.SessionShutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SessionShutdown waited for the server")
	}
}

func TestUpdateAddAndRemoveMcpServerConfigKeepOtherContentAndIndentation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "mcp.json")
	replaced, err := mcpext.AddMcpServerConfig(path, "files", extension.McpServerConfig{Command: "npx", Args: []string{"-y", "server"}})
	if err != nil || replaced {
		t.Fatalf("add = %v, %v", replaced, err)
	}
	if err := os.WriteFile(path, []byte("{\n\t\"note\": 1,\n\t\"mcpServers\": {\n\t\t\"files\": {\"command\": \"npx\"},\n\t\t\"docs\": {\"url\": \"https://example.com/mcp\"}\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := mcpext.UpdateMcpServerConfig(path, "docs", mcpext.McpServerConfigPatch{Enabled: &disabled, Exposure: extension.McpExposureDirect}, mcpext.UpdateMcpServerConfigOptions{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "{\n\t\"note\": 1,\n\t\"mcpServers\": {\n\t\t\"files\": {\n\t\t\t\"command\": \"npx\"\n\t\t},\n\t\t\"docs\": {\n\t\t\t\"url\": \"https://example.com/mcp\",\n\t\t\t\"enabled\": false,\n\t\t\t\"exposure\": \"direct\"\n\t\t}\n\t}\n}\n"
	if string(data) != want {
		t.Fatalf("file = %q\nwant %q", data, want)
	}
	// The defaults remove the keys again.
	enabled := true
	if err := mcpext.UpdateMcpServerConfig(path, "docs", mcpext.McpServerConfigPatch{Enabled: &enabled, Exposure: extension.McpExposureCodemode}, mcpext.UpdateMcpServerConfigOptions{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), "enabled") || strings.Contains(string(data), "exposure") {
		t.Fatalf("file = %s", data)
	}
	if err := mcpext.UpdateMcpServerConfig(path, "nope", mcpext.McpServerConfigPatch{Enabled: &enabled}, mcpext.UpdateMcpServerConfigOptions{}); err == nil || !strings.Contains(err.Error(), `does not define MCP server "nope"`) {
		t.Fatalf("update err = %v", err)
	}
	removed, err := mcpext.RemoveMcpServerConfig(path, "files")
	if err != nil || !removed {
		t.Fatalf("remove = %v, %v", removed, err)
	}
	if removed, _ := mcpext.RemoveMcpServerConfig(path, "files"); removed {
		t.Fatal("removed twice")
	}
	var parsed map[string]json.RawMessage
	data, _ = os.ReadFile(path)
	if err := json.Unmarshal(data, &parsed); err != nil || string(parsed["note"]) != "1" {
		t.Fatalf("file = %s, %v", data, err)
	}
	if removed, err := mcpext.RemoveMcpServerConfig(filepath.Join(t.TempDir(), "missing.json"), "x"); err != nil || removed {
		t.Fatalf("missing file: %v, %v", removed, err)
	}
}

func TestFormatMcpLogMessageHandlesNonObjectParams(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 1, 234_000_000, time.UTC)
	for raw, want := range map[string]string{
		`"plain"`:           "2026-09-29T12:00:01.234Z [s] info plain\n",
		`{"data":"a\r\nb"}`: "2026-09-29T12:00:01.234Z [s] info a\n    b\n",
		`{"level":"debug","logger":"","data":[1,{"a":2}]}`: "2026-09-29T12:00:01.234Z [s] debug [1,{\"a\":2}]\n",
		// JSON.stringify normalizes numbers and escapes; probed against upstream 0.99.1.
		`{"data":{"n":1.0,"e":"\u00e9","s":"a\u2028b"}}`: "2026-09-29T12:00:01.234Z [s] info {\"n\":1,\"e\":\"é\",\"s\":\"a\u2028b\"}\n",
	} {
		if got := mcpext.FormatMcpLogMessage("s", json.RawMessage(raw), now); got != want {
			t.Errorf("format(%s) = %q, want %q", raw, got, want)
		}
	}
}

// syncBrowser follows the authorization redirect inside ShowAuthorizationURL, so
// the callback arrives before the sign-in goes on to wait for it.
type syncBrowser struct{ t *testing.T }

func (b syncBrowser) ShowAuthorizationURL(u *url.URL) {
	response, err := http.Get(u.String())
	if err != nil {
		b.t.Error(err)
		return
	}
	_ = response.Body.Close()
}

func (syncBrowser) PromptForRedirectURL(ctx context.Context) (string, error) {
	<-ctx.Done()
	return "", nil
}

// A browser can reach the loopback callback before the sign-in waits for it, so
// the wait must be registered before the URL is shown.
func TestSignInRegistersTheCallbackWaitBeforeShowingTheAuthorizationURL(t *testing.T) {
	server := startOAuthMcpServer(t)
	store, err := mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, "").ForServer("test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := mcpext.SignInMcpServer(ctx, mcpext.SignInOptions{ServerURL: server.URL, Store: store, Prompt: syncBrowser{t}}); err != nil {
		t.Fatal(err)
	}
	if entries := server.logEntries(); !slices.Contains(entries, "token code") {
		t.Fatalf("log = %v", entries)
	}
}

// upstream limitMcpContent reports Math.ceil(totalBytes / 4) as the original token count.
func TestLimitMcpContentRoundsTheOriginalTokenCountUp(t *testing.T) {
	text := strings.Repeat("x", 30_001)
	content, path := mcpext.LimitMcpContent([]ai.ToolResultMessageContent{ai.TextContent{Text: text}}, func([]byte, string) (string, error) {
		return "/tmp/full.txt", nil
	})
	if path != "/tmp/full.txt" || len(content) != 1 {
		t.Fatalf("path = %q, content = %v", path, content)
	}
	if got := content[0].(ai.TextContent).Text; !strings.HasPrefix(got, "Warning: truncated output (original token count: 7501)\n") {
		t.Fatalf("text = %q", got[:80])
	}
	// A failed save is reported in place of the path.
	content, path = mcpext.LimitMcpContent([]ai.ToolResultMessageContent{ai.TextContent{Text: text}}, func([]byte, string) (string, error) {
		return "", errors.New("disk full")
	})
	if got := content[0].(ai.TextContent).Text; path != "" || !strings.HasSuffix(got, "[Could not save the full output: disk full]") {
		t.Fatalf("path = %q, text tail = %q", path, got[len(got)-60:])
	}
}

// Probed against upstream 0.99.1 (`createMcpToolDefinition` from the published
// package): label, description fallbacks, parameter schema merge, annotations.
func TestCreateMcpToolDefinitionMatchesUpstreamProbes(t *testing.T) {
	define := func(tool string) extension.ToolDefinition {
		var parsed mcp.Tool
		if err := json.Unmarshal([]byte(tool), &parsed); err != nil {
			t.Fatal(err)
		}
		return mcpext.CreateMcpToolDefinition(mcpext.McpToolOptions{
			Server: "s", Tool: parsed, Name: "mcp__s__t", Exposure: extension.McpExposureCodemode,
			Namespace: extension.ToolNamespace{Name: "mcp__s"}, Timeout: 1,
		})
	}
	for _, tc := range []struct{ tool, label, description, parameters, annotations string }{
		{`{"name":"a","inputSchema":{}}`, "s/a", "MCP tool a from server s", `{"type":"object","properties":{}}`, ""},
		// The merge keeps the schema's key order, and a null type becomes "object" in place.
		{`{"name":"b","inputSchema":{"properties":{"x":{"type":"string"}},"type":null}}`, "s/b", "MCP tool b from server s", `{"properties":{"x":{"type":"string"}},"type":"object"}`, ""},
		{`{"name":"c","description":"  padded  ","title":"T","inputSchema":{"type":"object","required":["z"],"properties":{"z":{}}},"annotations":{"title":"AT","readOnlyHint":true,"idempotentHint":"no","openWorldHint":false}}`,
			"s/c", "padded", `{"type":"object","required":["z"],"properties":{"z":{}}}`, `{"readOnlyHint":true,"openWorldHint":false}`},
		{`{"name":"d","title":"Only Title","inputSchema":{"x":1}}`, "s/d", "Only Title", `{"x":1,"type":"object","properties":{}}`, ""},
		{`{"name":"e","annotations":{"title":"Ann Title"},"inputSchema":{}}`, "s/e", "Ann Title", `{"type":"object","properties":{}}`, ""},
	} {
		d := define(tc.tool)
		if d.Label != tc.label || d.Description != tc.description || string(d.Parameters) != tc.parameters {
			t.Errorf("%s: label %q, description %q, parameters %s", tc.tool, d.Label, d.Description, d.Parameters)
		}
		if d.Exposure != extension.ToolExposureDeferred {
			t.Errorf("%s: exposure %s", tc.tool, d.Exposure)
		}
		if tc.annotations == "" {
			if d.Annotations != nil {
				t.Errorf("%s: annotations %v", tc.tool, d.Annotations)
			}
		} else {
			jsonEqual(t, d.Annotations, tc.annotations)
		}
		if want := `{"type":"object","properties":{"content":{"type":"array","items":{"type":"object"}},"isError":{"type":"boolean"},"_meta":{"type":"object"}},"required":["content"]}`; string(d.OutputSchema) != want {
			t.Errorf("%s: output schema %s", tc.tool, d.OutputSchema)
		}
	}
}

func TestConvertMcpResultMatchesUpstreamProbes(t *testing.T) {
	var result mcp.CallToolResult
	blob := base64.StdEncoding.EncodeToString([]byte(`{"a":1}`))
	if err := json.Unmarshal([]byte(`{"content":[
		{"type":"resource","resource":{"uri":"file:///x.json","mimeType":"application/json; charset=utf-8","blob":"`+blob+`"}},
		{"type":"resource_link","uri":"u","name":"n","size":1536},
		{"type":"weird"}]}`), &result); err != nil {
		t.Fatal(err)
	}
	converted, err := mcpext.ConvertMcpResult("s", "t", &result, mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, converted.Content, `[{"type":"text","text":"{\"a\":1}"},{"type":"text","text":"[Resource u \"n\" (1.5KB)]"},{"type":"text","text":"[unsupported MCP content weird]"}]`)

	// An error result whose only content is the structured result needs no placeholder text.
	var failure mcp.CallToolResult
	if err := json.Unmarshal([]byte(`{"content":[],"isError":true,"structuredContent":{"a":1}}`), &failure); err != nil {
		t.Fatal(err)
	}
	converted, err = mcpext.ConvertMcpResult("s", "t", &failure, mcpext.ConvertMcpResultOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, converted.Content, `[{"type":"text","text":"{\n  \"a\": 1\n}"}]`)
	if !converted.IsError {
		t.Fatal("not an error result")
	}
}
