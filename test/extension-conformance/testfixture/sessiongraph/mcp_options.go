package sessiongraph

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// mcpLog is what the functions of the script were asked, per server, in order.
type mcpLog struct {
	mu          sync.Mutex
	demo        []string
	remote      []string
	credentials []string
	loadConfig  []any
}

func (l *mcpLog) server(name string) *[]string {
	if name == "remote" {
		return &l.remote
	}
	return &l.demo
}

func (l *mcpLog) add(list *[]string, entry string) {
	l.mu.Lock()
	*list = append(*list, entry)
	l.mu.Unlock()
}

// mcpPeerReply answers one message of the MCP handshake, the tool list and a tool call, as testdata/session-graph.mjs mcpPeerReply does.
func mcpPeerReply(raw json.RawMessage) json.RawMessage {
	var message struct {
		ID     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
		Params struct {
			ProtocolVersion string `json:"protocolVersion"`
			Arguments       struct {
				Text string `json:"text"`
			} `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(raw, &message); err != nil || message.ID == nil {
		return nil
	}
	reply := func(result any) json.RawMessage {
		encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
		if err != nil {
			panic(err)
		}
		return encoded
	}
	switch message.Method {
	case "initialize":
		return reply(map[string]any{"protocolVersion": message.Params.ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "fake", "version": "1"}})
	case "tools/list":
		return reply(map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "Echo text", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []any{"text"}}}}})
	case "tools/call":
		return reply(map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo:" + message.Params.Arguments.Text}}})
	case "ping":
		return reply(map[string]any{})
	}
	encoded, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": message.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	return encoded
}

// mcpScriptTransport is the in-process peer behind createTransport.
type mcpScriptTransport struct {
	sdk.McpTransportEvents
	log    *mcpLog
	events *[]string
	auth   *sdk.McpAuthProvider
}

func (t *mcpScriptTransport) Start() error {
	t.log.add(t.events, "start")
	if t.auth != nil {
		token := ok(t.auth.Token())
		if token == nil {
			t.log.add(t.events, "token undefined")
		} else {
			t.log.add(t.events, "token "+*token)
		}
	}
	return nil
}

func (t *mcpScriptTransport) Send(_ context.Context, message json.RawMessage) error {
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(message, &probe)
	if probe.Method == "" {
		probe.Method = "response"
	}
	t.log.add(t.events, "send "+probe.Method)
	if reply := mcpPeerReply(message); reply != nil {
		t.EmitMessage(reply)
	}
	return nil
}

func (t *mcpScriptTransport) Close() error {
	t.log.add(t.events, "close")
	t.EmitClose()
	return nil
}

type mcpScriptServerStore struct {
	log   *mcpLog
	name  string
	state json.RawMessage
}

func (s mcpScriptServerStore) Load() (sdk.McpOAuthState, error) {
	s.log.add(&s.log.credentials, "load "+s.name)
	return s.state, nil
}

func (s mcpScriptServerStore) Save(sdk.McpOAuthState) error {
	s.log.add(&s.log.credentials, "save "+s.name)
	return nil
}

func (s mcpScriptServerStore) WithRefreshLock(fn func() error) error {
	s.log.add(&s.log.credentials, "lock "+s.name)
	return fn()
}

type mcpScriptCredentials struct {
	log   *mcpLog
	state json.RawMessage
}

func (c mcpScriptCredentials) ForServer(name, serverURL string) (sdk.McpOAuthServerStore, error) {
	c.log.add(&c.log.credentials, "forServer "+name+" "+serverURL)
	return mcpScriptServerStore{log: c.log, name: name, state: c.state}, nil
}

func (c mcpScriptCredentials) Tokens(name, serverURL string) (*sdk.McpOAuthTokens, error) {
	c.log.add(&c.log.credentials, "tokens "+name+" "+serverURL)
	return &sdk.McpOAuthTokens{AccessToken: "tok-1", TokenType: "Bearer"}, nil
}

func (c mcpScriptCredentials) Remove(name, serverURL string) (bool, error) {
	c.log.add(&c.log.credentials, "remove "+name+" "+serverURL)
	return true, nil
}

// mcpOptionsScript passes loadConfig, createTransport and credentials to CreateMcpExtension, as testdata/session-graph.mjs mcpOptionsScript does with Pi's factory; the report is Pi's own run's.
func mcpOptionsScript(ctx sdk.Context, args map[string]any) map[string]any {
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir := str("cwd"), str("agentDir")
	models := str("mrToolModels")
	log := &mcpLog{}
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: str("mrStore")}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
		"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-tool", "defaultModel": "m1",
	}, nil))
	state := json.RawMessage(`{"serverUrl":"http://127.0.0.1:9/mcp","tokens":{"access_token":"tok-1","token_type":"Bearer"}}`)
	loader := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
		Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		AdditionalExtensionPaths: []string{str("mcpProbe")},
		ExtensionFactories: []any{sdk.CreateMcpExtension(sdk.McpExtensionOptions{
			LoadConfig: func(c sdk.McpLoadConfigContext) (sdk.LoadedMcpConfig, error) {
				log.mu.Lock()
				log.loadConfig = append(log.loadConfig, map[string]any{"cwd": c.Cwd, "isProjectTrusted": c.IsProjectTrusted})
				log.mu.Unlock()
				return sdk.LoadedMcpConfig{Servers: []sdk.McpServerEntry{
					{Name: "demo", Config: sdk.McpServerConfig{Command: "unused", Exposure: sdk.McpExposureDirect}, Source: "<config>", Scope: "global"},
					{Name: "remote", Config: sdk.McpServerConfig{URL: "http://127.0.0.1:9/mcp", Exposure: sdk.McpExposureDirect}, Source: "<config>", Scope: "global"},
				}, Errors: []string{}}, nil
			},
			CreateTransport: func(entry sdk.McpServerEntry, transportCwd string, auth *sdk.McpAuthProvider) (sdk.McpTransport, error) {
				events := log.server(entry.Name)
				log.add(events, fmt.Sprintf("create %s cwd=%s auth=%t", entry.Name, transportCwd, auth != nil))
				return &mcpScriptTransport{log: log, events: events, auth: auth}, nil
			},
			Credentials:  mcpScriptCredentials{log: log, state: state},
			OpenURL:      func(string) {},
			UpdateConfig: func(sdk.McpServerEntry, sdk.McpServerConfigPatch) error { return nil },
			LogPath:      filepath.Join(agentDir, "mcp.log"),
		})},
	}))
	if err := loader.Reload(nil); err != nil {
		panic(err)
	}
	manager := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
	session := ok(ctx.CreateAgentSession(sdk.CreateAgentSessionOptions{SessionManager: &manager, SettingsManager: &settings, ModelRuntime: rt, ResourceLoader: loader}))
	if err := session.BindExtensions(); err != nil {
		panic(err)
	}
	for _, prompt := range []string{"call the tool", "/mcp reconnect demo", "/mcp logout remote"} {
		if err := session.Prompt(context.Background(), prompt); err != nil {
			panic(err)
		}
	}
	messages := []any{}
	for _, message := range ok(session.Messages()) {
		if message["role"] != "toolResult" {
			continue
		}
		var text strings.Builder
		for _, part := range message["content"].([]any) {
			text.WriteString(part.(map[string]any)["text"].(string))
		}
		messages = append(messages, text.String())
	}
	seen := []any{}
	for _, entry := range ok(manager.GetEntries()) {
		if entry["type"] == "custom" && entry["customType"] == "mcp_seen" {
			seen = append(seen, entry["data"])
		}
	}
	log.mu.Lock()
	report := map[string]any{
		"messages": messages, "seen": seen,
		"log": map[string]any{"demo": nonNil(log.demo), "remote": nonNil(log.remote), "credentials": nonNil(log.credentials), "loadConfig": log.loadConfig},
	}
	log.mu.Unlock()
	session.Dispose()
	return report
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}
