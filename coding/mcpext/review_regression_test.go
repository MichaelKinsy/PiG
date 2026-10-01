package mcpext_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/mcpext"
	"github.com/MichaelKinsy/PiG/mcp"
	"github.com/MichaelKinsy/PiG/mcp/mcptest"
	"github.com/MichaelKinsy/PiG/mcp/oauth"
)

// config.ts readConfigFile reports `${path}: ${error.message}` for a file
// readFileSync cannot read. Node's messages, probed with Node 24.19.0:
//
//	readFileSync("<dir>", "utf8")  -> "EISDIR: illegal operation on a directory, read"
//	readFileSync("<0o000 file>")   -> "EACCES: permission denied, open '<path>'"
func TestLoadMcpConfigReportsReadFailuresWithNodeFSMessages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Node messages were probed on Linux only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	loaded := mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: dir, Cwd: dir, ConfigDirName: ".pi"})
	if want := path + ": EISDIR: illegal operation on a directory, read"; len(loaded.Errors) != 1 || loaded.Errors[0] != want {
		t.Fatalf("errors = %q, want %q", loaded.Errors, want)
	}

	if os.Geteuid() == 0 {
		return
	}
	dir = t.TempDir()
	path = filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o000); err != nil {
		t.Fatal(err)
	}
	loaded = mcpext.LoadMcpConfig(mcpext.LoadOptions{AgentDir: dir, Cwd: dir, ConfigDirName: ".pi"})
	if want := path + ": EACCES: permission denied, open '" + path + "'"; len(loaded.Errors) != 1 || loaded.Errors[0] != want {
		t.Fatalf("errors = %q, want %q", loaded.Errors, want)
	}
	disabled := false
	err := mcpext.UpdateMcpServerConfig(path, "a", mcpext.McpServerConfigPatch{Enabled: &disabled})
	if want := "EACCES: permission denied, open '" + path + "'"; err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

// config.ts editMcpServers writes JSON.stringify(parsed, null, indent), and
// JSON.stringify uses at most 10 characters of a string indent:
// JSON.stringify({a:{b:1}}, null, " ".repeat(12)) indents by 10 spaces.
func TestEditingAConfigFileIndentsByAtMostTenCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte("{\n            \"mcpServers\": {\"a\": {\"command\": \"x\"}}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mcpext.RemoveMcpServerConfig(path, "a"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if want := "{\n          \"mcpServers\": {}\n}\n"; string(data) != want {
		t.Fatalf("file = %q, want %q", data, want)
	}
}

// tools.ts extensionOf takes the WHATWG pathname of the resource URI; for a
// URI without a hierarchical part that is the opaque path:
// new URL("note:readme.pdf?x=1").pathname === "readme.pdf" (Node 24.19.0).
func TestConvertMcpResultNamesASavedBlobByTheExtensionOfAnOpaqueURI(t *testing.T) {
	for uri, want := range map[string]string{
		"note:readme.pdf?x=1": ".pdf",
		"file:///a/b.txt":     ".txt",
		"memo://x/y.png#f":    ".png",
		"rel/a.gz":            ".gz",
		"note:readme":         ".bin",
	} {
		var saved string
		result := decodeResult(t, `{"content":[{"type":"resource","resource":{"uri":`+strconv.Quote(uri)+`,"mimeType":"application/octet-stream","blob":"AAE="}}]}`)
		_, err := mcpext.ConvertMcpResult("docs", "t", result, mcpext.ConvertMcpResultOptions{SaveOutput: func(_ []byte, extension string) (string, error) {
			saved = extension
			return "/tmp/x" + extension, nil
		}})
		if err != nil {
			t.Fatal(err)
		}
		if saved != want {
			t.Errorf("%s: extension = %q, want %q", uri, saved, want)
		}
	}
}

// tools.ts execute reports `Progress ${progress.progress}${total}` with
// JavaScript's String(number): String(1e21) === "1e+21", String(1e-7) === "1e-7".
func TestMcpToolReportsProgressNumbersAsJavaScriptStrings(t *testing.T) {
	definition := mcpext.CreateMcpToolDefinition(mcpext.McpToolOptions{
		Server: "docs", Tool: mcp.Tool{Name: "t", InputSchema: json.RawMessage(`{"type":"object"}`)}, Name: "mcp__docs__t",
		Exposure: extension.McpExposureDirect,
		GetClient: func(context.Context) (mcpext.McpToolCaller, error) {
			return progressCaller{}, nil
		},
	})
	var updates []string
	_, err := definition.Execute(t.Context(), "call", json.RawMessage(`{}`), agent.ToolUpdateCallback(func(partial agent.AgentToolResult) {
		updates = append(updates, partial.Text())
	}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Progress 1e-7/1e+21"}; !slices.Equal(updates, want) {
		t.Fatalf("updates = %q, want %q", updates, want)
	}
}

type progressCaller struct{}

func (progressCaller) CallTool(_ context.Context, _ string, _ any, options mcp.RequestOptions) (*mcp.CallToolResult, error) {
	total := 1e21
	options.OnProgress(mcp.ProgressNotification{Progress: 1e-7, Total: &total})
	return &mcp.CallToolResult{Content: []mcp.ContentBlock{}}, nil
}

// core/mcp-servers.ts toolPatternRegExp joins the parts with `.*`, and a
// JavaScript `.` does not match a line terminator.
func TestGetMcpToolExposurePatternsDoNotMatchAcrossLineTerminators(t *testing.T) {
	config := extension.McpServerConfig{Command: "x", ToolExposure: extension.NewOrderedExposures("a*b", "direct")}
	for name, want := range map[string]extension.McpExposure{
		"axb":      extension.McpExposureDirect,
		"a\nb":     extension.McpExposureCodemode,
		"a\u2028b": extension.McpExposureCodemode,
	} {
		if got := extension.GetMcpToolExposure(config, name); got != want {
			t.Errorf("%q: exposure = %s, want %s", name, got, want)
		}
	}
}

// oauth.ts McpOAuthCredentialStore.write stores JSON.stringify(states, null, 2):
// servers stay in the order they were first saved, and JSON.stringify does
// not escape "&", "<" or ">" as encoding/json does.
func TestMcpOAuthCredentialStoreKeepsServerOrderAndJSONStringifyEscapes(t *testing.T) {
	dir := t.TempDir()
	store := mcpext.NewMcpOAuthCredentialStore(dir)
	for _, url := range []string{"https://z.example/mcp", "https://a.example/mcp?x=1&y=2"} {
		server, err := store.ForServer(url)
		if err != nil {
			t.Fatal(err)
		}
		if err := server.Save(t.Context(), oauth.McpOAuthState{ServerURL: url, Tokens: &oauth.OAuthTokens{AccessToken: "<a&b>", TokenType: "Bearer"}}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "mcp-auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	z, a := strings.Index(text, `"https://z.example/mcp"`), strings.Index(text, `"https://a.example/mcp?x=1&y=2"`)
	if z < 0 || a < 0 || z > a {
		t.Fatalf("servers are not in save order:\n%s", text)
	}
	if strings.Contains(text, `\u00`) || !strings.Contains(text, `"access_token": "<a&b>"`) {
		t.Fatalf("file is not JSON.stringify output:\n%s", text)
	}
	if !strings.HasPrefix(text, "{\n  \"https://z.example/mcp\": {\n    \"serverUrl\"") || !strings.HasSuffix(text, "}\n") {
		t.Fatalf("file is not indented by two spaces:\n%s", text)
	}
}

// runtime.ts connectOnce identifies the client with `version: VERSION`, the
// running release, not a placeholder.
func TestConnectionIdentifiesItselfWithTheRunningVersion(t *testing.T) {
	clientInfo := make(chan json.RawMessage, 1)
	connection, err := mcpext.NewConnection(mcpext.ConnectionOptions{
		Entry: mcpext.McpServerEntry{Name: "docs", Config: extension.McpServerConfig{Command: "x"}},
		Cwd:   t.TempDir(),
		CreateTransport: func(mcpext.McpServerEntry, string, mcp.AuthProvider) (mcp.Transport, error) {
			client, server := mcptest.NewInMemoryTransportPair()
			server.OnMessage(func(message mcp.JSONRPCMessage) {
				if !message.IsRequest() {
					return
				}
				var response mcp.JSONRPCMessage
				switch message.Method {
				case "initialize":
					var params struct {
						ClientInfo json.RawMessage `json:"clientInfo"`
					}
					_ = json.Unmarshal(message.Params, &params)
					clientInfo <- params.ClientInfo
					response = mcp.NewResult(*message.ID, json.RawMessage(`{"protocolVersion":"`+mcp.LatestProtocolVersion+`","capabilities":{},"serverInfo":{"name":"s","version":"1"}}`))
				default:
					response = mcp.NewResult(*message.ID, json.RawMessage(`{}`))
				}
				go func() { _ = server.Send(response) }()
			})
			_ = server.Start()
			return client, nil
		},
		Credentials: mcpext.NewMcpOAuthCredentialStoreWithBackend(&mcpext.InMemoryAuthStorageBackend{}, ""),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.GetClient(t.Context()); err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, <-clientInfo, `{"name":"pig","version":"`+coding.Version+`"}`)
}
