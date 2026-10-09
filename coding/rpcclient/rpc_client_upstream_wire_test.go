package rpcclient

// pi: packages/coding-agent/src/modes/rpc/rpc-client.ts

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// scriptEnv carries the scripted child's answers: a JSON object mapping a command type to the record its response adds ("data" or "success":false and "error"), and an "events" list of records the child writes after answering "prompt".
const scriptEnv = "PIG_RPCCLIENT_TEST_SCRIPT"

// runScriptedChild logs argv, the working directory and every command line, then answers each command with the scripted response, as rpc-mode.ts does on the wire.
func runScriptedChild() int {
	logFile, err := os.OpenFile(os.Getenv("PIG_RPCCLIENT_TEST_LOG"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 3
	}
	defer func() { _ = logFile.Close() }()
	cwd, _ := os.Getwd()
	header, _ := json.Marshal(map[string]any{"argv": os.Args[1:], "cwd": cwd, "marker": os.Getenv("PIG_RPCCLIENT_TEST_MARKER")})
	_, _ = fmt.Fprintln(logFile, string(header))
	var script struct {
		Responses map[string]json.RawMessage `json:"responses"`
		Events    []json.RawMessage          `json:"events"`
		Stderr    string                     `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(os.Getenv(scriptEnv)), &script); err != nil {
		return 4
	}
	if script.Stderr != "" {
		_, _ = fmt.Fprint(os.Stderr, script.Stderr)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = fmt.Fprintln(logFile, line)
		var cmd struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(line), &cmd)
		response := map[string]any{"id": cmd.ID, "type": "response", "command": cmd.Type, "success": true}
		if extra, ok := script.Responses[cmd.Type]; ok {
			var fields map[string]any
			if err := json.Unmarshal(extra, &fields); err != nil {
				return 5
			}
			maps.Copy(response, fields)
		}
		out, _ := json.Marshal(response)
		fmt.Println(string(out))
		if cmd.Type == "prompt" {
			for _, event := range script.Events {
				fmt.Println(string(event))
			}
		}
	}
	return 0
}

func scriptedClient(t *testing.T, options RpcClientOptions, script map[string]any) (*RpcClient, string) {
	t.Helper()
	encoded, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "commands.log")
	options.CliPath = os.Args[0]
	env := map[string]string{childModeEnv: "scripted", "PIG_RPCCLIENT_TEST_LOG": logPath, scriptEnv: string(encoded)}
	maps.Copy(env, options.Env)
	options.Env = env
	client := NewRpcClient(options)
	t.Cleanup(client.Stop)
	return client, logPath
}

// upstream: packages/coding-agent/src/modes/rpc/rpc-client.ts:198-505 (every command method). Each Go method sends exactly the command its Pi counterpart sends, JSON.stringify member order (type, the arguments, then id), an undefined optional argument absent, and returns what Pi's getData(...) selects from the response.
// mutation-checked: zeroing the results of RpcClient.Clone, RpcClient.CycleModel, RpcClient.SetModel fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:403 (clone)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:267 (cycleModel)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:259 (setModel)
// mutation-checked: zeroing the results of RpcClient.NewSession, RpcClient.Prompt fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:243 (newSession)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:198 (prompt)
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:198 (RpcClient.prompt); packages/coding-agent/src/modes/rpc/rpc-client.ts:210 (RpcClient.steer); packages/coding-agent/src/modes/rpc/rpc-client.ts:218 (RpcClient.followUp); packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (RpcClient.abort); packages/coding-agent/src/modes/rpc/rpc-client.ts:233 (RpcClient.clearQueue); packages/coding-agent/src/modes/rpc/rpc-client.ts:243 (RpcClient.newSession); packages/coding-agent/src/modes/rpc/rpc-client.ts:251 (RpcClient.getState); packages/coding-agent/src/modes/rpc/rpc-client.ts:259 (RpcClient.setModel); packages/coding-agent/src/modes/rpc/rpc-client.ts:267 (RpcClient.cycleModel); packages/coding-agent/src/modes/rpc/rpc-client.ts:279 (RpcClient.getAvailableModels); packages/coding-agent/src/modes/rpc/rpc-client.ts:287 (RpcClient.setThinkingLevel); packages/coding-agent/src/modes/rpc/rpc-client.ts:294 (RpcClient.cycleThinkingLevel); packages/coding-agent/src/modes/rpc/rpc-client.ts:302 (RpcClient.getAvailableThinkingLevels); packages/coding-agent/src/modes/rpc/rpc-client.ts:310 (RpcClient.setSteeringMode); packages/coding-agent/src/modes/rpc/rpc-client.ts:317 (RpcClient.setFollowUpMode); packages/coding-agent/src/modes/rpc/rpc-client.ts:324 (RpcClient.compact); packages/coding-agent/src/modes/rpc/rpc-client.ts:332 (RpcClient.setAutoCompaction); packages/coding-agent/src/modes/rpc/rpc-client.ts:339 (RpcClient.setAutoRetry); packages/coding-agent/src/modes/rpc/rpc-client.ts:346 (RpcClient.abortRetry); packages/coding-agent/src/modes/rpc/rpc-client.ts:353 (RpcClient.bash); packages/coding-agent/src/modes/rpc/rpc-client.ts:361 (RpcClient.abortBash); packages/coding-agent/src/modes/rpc/rpc-client.ts:368 (RpcClient.getSessionStats); packages/coding-agent/src/modes/rpc/rpc-client.ts:376 (RpcClient.exportHtml); packages/coding-agent/src/modes/rpc/rpc-client.ts:385 (RpcClient.switchSession); packages/coding-agent/src/modes/rpc/rpc-client.ts:394 (RpcClient.fork); packages/coding-agent/src/modes/rpc/rpc-client.ts:403 (RpcClient.clone); packages/coding-agent/src/modes/rpc/rpc-client.ts:411 (RpcClient.getForkMessages); packages/coding-agent/src/modes/rpc/rpc-client.ts:419 (RpcClient.getEntries); packages/coding-agent/src/modes/rpc/rpc-client.ts:427 (RpcClient.getTree); packages/coding-agent/src/modes/rpc/rpc-client.ts:435 (RpcClient.getLastAssistantText); packages/coding-agent/src/modes/rpc/rpc-client.ts:443 (RpcClient.setSessionName); packages/coding-agent/src/modes/rpc/rpc-client.ts:450 (RpcClient.getMessages); packages/coding-agent/src/modes/rpc/rpc-client.ts:458 (RpcClient.getCommands).
func TestRpcClientCommandMethodsMatchPiWireAndResults(t *testing.T) {
	str := func(value string) *string { return &value }
	exitZero := 0
	percent := 12.5
	tokens := 1000
	after := 100
	script := map[string]any{"responses": map[string]any{
		"prompt":                        map[string]any{"data": map[string]any{"disposition": "started"}},
		"steer":                         map[string]any{"data": map[string]any{"disposition": "queued"}},
		"follow_up":                     map[string]any{"data": map[string]any{"disposition": "handled"}},
		"clear_queue":                   map[string]any{"data": map[string]any{"steering": []string{"a"}, "followUp": []string{"b", "c"}}},
		"new_session":                   map[string]any{"data": map[string]any{"cancelled": true}},
		"get_state":                     map[string]any{"data": map[string]any{"model": map[string]any{"id": "m1", "name": "M1", "api": "faux", "provider": "p", "baseUrl": "http://x", "reasoning": true, "input": []string{"text"}, "contextWindow": 8000, "maxTokens": 400}, "thinkingLevel": "high", "isStreaming": true, "isCompacting": false, "steeringMode": "all", "followUpMode": "one-at-a-time", "sessionFile": "/s.jsonl", "sessionId": "sid", "sessionName": "named", "autoCompactionEnabled": true, "messageCount": 3, "pendingMessageCount": 1}},
		"set_model":                     map[string]any{"data": map[string]any{"provider": "p", "id": "m2"}},
		"cycle_model":                   map[string]any{"data": map[string]any{"model": map[string]any{"provider": "p", "id": "m3"}, "thinkingLevel": "low", "isScoped": true}},
		"get_available_models":          map[string]any{"data": map[string]any{"models": []map[string]any{{"provider": "p", "id": "m1", "contextWindow": 128000, "reasoning": true}, {"provider": "q", "id": "m2", "contextWindow": 4096, "reasoning": false}}}},
		"cycle_thinking_level":          map[string]any{"data": map[string]any{"level": "medium"}},
		"get_available_thinking_levels": map[string]any{"data": map[string]any{"levels": []string{"off", "low", "high"}}},
		"compact":                       map[string]any{"data": map[string]any{"summary": "S", "firstKeptEntryId": "e1", "tokensBefore": 900, "estimatedTokensAfter": 100}},
		"bash":                          map[string]any{"data": map[string]any{"output": "hi\n", "exitCode": 0, "cancelled": false, "truncated": true, "fullOutputPath": "/tmp/full"}},
		"get_session_stats":             map[string]any{"data": map[string]any{"sessionFile": "/s.jsonl", "sessionId": "sid", "userMessages": 1, "assistantMessages": 2, "toolCalls": 3, "toolResults": 4, "totalMessages": 7, "tokens": map[string]any{"input": 1, "output": 2, "cacheRead": 3, "cacheWrite": 4, "total": 10}, "cost": 0.25, "contextUsage": map[string]any{"tokens": 1000, "contextWindow": 8000, "percent": 12.5}}},
		"export_html":                   map[string]any{"data": map[string]any{"path": "/out.html"}},
		"switch_session":                map[string]any{"data": map[string]any{"cancelled": false}},
		"fork":                          map[string]any{"data": map[string]any{"text": "forked text", "cancelled": false}},
		"clone":                         map[string]any{"data": map[string]any{"cancelled": true}},
		"get_fork_messages":             map[string]any{"data": map[string]any{"messages": []map[string]any{{"entryId": "e1", "text": "one"}, {"entryId": "e2", "text": "two"}}}},
		"get_entries":                   map[string]any{"data": map[string]any{"entries": []map[string]any{{"type": "message", "id": "e1", "parentId": nil, "timestamp": "t1"}}, "leafId": "e1"}},
		"get_tree":                      map[string]any{"data": map[string]any{"tree": []map[string]any{{"entry": map[string]any{"type": "message", "id": "e1", "parentId": nil, "timestamp": "t1"}, "children": []any{}, "label": "L", "labelTimestamp": "t2"}}, "leafId": nil}},
		"get_last_assistant_text":       map[string]any{"data": map[string]any{"text": "last words"}},
		"get_messages":                  map[string]any{"data": map[string]any{"messages": []map[string]any{{"role": "user", "content": "hi"}, {"role": "assistant"}}}},
		"get_commands":                  map[string]any{"data": map[string]any{"commands": []map[string]any{{"name": "skill:x", "description": "d", "source": "skill", "sourceInfo": map[string]any{"path": "/p", "source": "local", "scope": "project", "origin": "top-level", "baseDir": "/b"}}}}},
	}}
	client, logPath := scriptedClient(t, RpcClientOptions{}, script)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	image := []ImageContent{{Data: "AAAA", MimeType: "image/png"}}
	behavior := StreamingBehaviorFollowUp
	type row struct {
		name string
		call func() (any, error)
		wire string
		want any
	}
	rows := []row{
		{"prompt with images and behavior", func() (any, error) { return client.Prompt("hello", image, &behavior) }, `{"type":"prompt","message":"hello","images":[{"type":"image","data":"AAAA","mimeType":"image/png"}],"streamingBehavior":"followUp"`, PromptDispositionStarted},
		{"prompt bare", func() (any, error) { return client.Prompt("hello", nil, nil) }, `{"type":"prompt","message":"hello"`, PromptDispositionStarted},
		{"steer", func() (any, error) { return client.Steer("go left", image) }, `{"type":"steer","message":"go left","images":[{"type":"image","data":"AAAA","mimeType":"image/png"}]`, QueuedInputDispositionQueued},
		{"follow_up", func() (any, error) { return client.FollowUp("then this", nil) }, `{"type":"follow_up","message":"then this"`, QueuedInputDispositionHandled},
		{"abort", func() (any, error) { return nil, client.Abort() }, `{"type":"abort"`, nil},
		{"clear_queue", func() (any, error) { return client.ClearQueue() }, `{"type":"clear_queue"`, ClearQueueResult{Steering: []string{"a"}, FollowUp: []string{"b", "c"}}},
		{"new_session with parent", func() (any, error) { return client.NewSession(str("/parent.jsonl")) }, `{"type":"new_session","parentSession":"/parent.jsonl"`, CancelledResult{Cancelled: true}},
		{"new_session bare", func() (any, error) { return client.NewSession(nil) }, `{"type":"new_session"`, CancelledResult{Cancelled: true}},
		{"get_state", func() (any, error) { return client.GetState() }, `{"type":"get_state"`, RpcSessionState{
			Model:         &Model{ID: "m1", Name: "M1", API: "faux", Provider: "p", BaseURL: "http://x", Reasoning: true, Input: []string{"text"}, ContextWindow: 8000, MaxTokens: 400, Raw: json.RawMessage(`{"api":"faux","baseUrl":"http://x","contextWindow":8000,"id":"m1","input":["text"],"maxTokens":400,"name":"M1","provider":"p","reasoning":true}`)},
			ThinkingLevel: "high", IsStreaming: true, SteeringMode: "all", FollowUpMode: "one-at-a-time", SessionFile: str("/s.jsonl"), SessionID: "sid", SessionName: str("named"), AutoCompactionEnabled: true, MessageCount: 3, PendingMessageCount: 1}},
		{"set_model", func() (any, error) { return client.SetModel("p", "m2") }, `{"type":"set_model","provider":"p","modelId":"m2"`, ModelReference{Provider: "p", ID: "m2"}},
		{"cycle_model", func() (any, error) { return client.CycleModel() }, `{"type":"cycle_model"`, &CycleModelResult{Model: ModelReference{Provider: "p", ID: "m3"}, ThinkingLevel: "low", IsScoped: true}},
		{"get_available_models", func() (any, error) { return client.GetAvailableModels() }, `{"type":"get_available_models"`, []ModelInfo{{Provider: "p", ID: "m1", ContextWindow: 128000, Reasoning: true}, {Provider: "q", ID: "m2", ContextWindow: 4096}}},
		{"set_thinking_level", func() (any, error) { return nil, client.SetThinkingLevel("xhigh") }, `{"type":"set_thinking_level","level":"xhigh"`, nil},
		{"cycle_thinking_level", func() (any, error) { return client.CycleThinkingLevel() }, `{"type":"cycle_thinking_level"`, &CycleThinkingLevelResult{Level: "medium"}},
		{"get_available_thinking_levels", func() (any, error) { return client.GetAvailableThinkingLevels() }, `{"type":"get_available_thinking_levels"`, []ThinkingLevel{"off", "low", "high"}},
		{"set_steering_mode", func() (any, error) { return nil, client.SetSteeringMode("one-at-a-time") }, `{"type":"set_steering_mode","mode":"one-at-a-time"`, nil},
		{"set_follow_up_mode", func() (any, error) { return nil, client.SetFollowUpMode("all") }, `{"type":"set_follow_up_mode","mode":"all"`, nil},
		{"compact with instructions", func() (any, error) { return client.Compact(str("be brief")) }, `{"type":"compact","customInstructions":"be brief"`, CompactionResult{Summary: "S", FirstKeptEntryID: "e1", TokensBefore: 900, EstimatedTokensAfter: &after}},
		{"compact bare", func() (any, error) { return client.Compact(nil) }, `{"type":"compact"`, CompactionResult{Summary: "S", FirstKeptEntryID: "e1", TokensBefore: 900, EstimatedTokensAfter: &after}},
		{"set_auto_compaction", func() (any, error) { return nil, client.SetAutoCompaction(false) }, `{"type":"set_auto_compaction","enabled":false`, nil},
		{"set_auto_retry", func() (any, error) { return nil, client.SetAutoRetry(true) }, `{"type":"set_auto_retry","enabled":true`, nil},
		{"abort_retry", func() (any, error) { return nil, client.AbortRetry() }, `{"type":"abort_retry"`, nil},
		{"bash", func() (any, error) { return client.Bash("echo hi") }, `{"type":"bash","command":"echo hi"`, BashResult{Output: "hi\n", ExitCode: &exitZero, Truncated: true, FullOutputPath: "/tmp/full"}},
		{"abort_bash", func() (any, error) { return nil, client.AbortBash() }, `{"type":"abort_bash"`, nil},
		{"get_session_stats", func() (any, error) { return client.GetSessionStats() }, `{"type":"get_session_stats"`, SessionStats{SessionFile: str("/s.jsonl"), SessionID: "sid", UserMessages: 1, AssistantMessages: 2, ToolCalls: 3, ToolResults: 4, TotalMessages: 7, Tokens: SessionStatsTokens{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10}, Cost: 0.25, ContextUsage: &ContextUsage{Tokens: &tokens, ContextWindow: 8000, Percent: &percent}}},
		{"export_html with path", func() (any, error) { return client.ExportHtml(str("/out.html")) }, `{"type":"export_html","outputPath":"/out.html"`, ExportHtmlResult{Path: "/out.html"}},
		{"export_html bare", func() (any, error) { return client.ExportHtml(nil) }, `{"type":"export_html"`, ExportHtmlResult{Path: "/out.html"}},
		{"switch_session", func() (any, error) { return client.SwitchSession("/other.jsonl") }, `{"type":"switch_session","sessionPath":"/other.jsonl"`, CancelledResult{}},
		{"fork", func() (any, error) { return client.Fork("e9") }, `{"type":"fork","entryId":"e9"`, ForkResult{Text: "forked text"}},
		{"clone", func() (any, error) { return client.Clone() }, `{"type":"clone"`, CancelledResult{Cancelled: true}},
		{"get_fork_messages", func() (any, error) { return client.GetForkMessages() }, `{"type":"get_fork_messages"`, []ForkMessage{{EntryID: "e1", Text: "one"}, {EntryID: "e2", Text: "two"}}},
		{"get_entries since", func() (any, error) { return client.GetEntries(str("e0")) }, `{"type":"get_entries","since":"e0"`, GetEntriesResult{Entries: []SessionEntry{{Type: "message", ID: "e1", Timestamp: "t1", Raw: json.RawMessage(`{"id":"e1","parentId":null,"timestamp":"t1","type":"message"}`)}}, LeafID: str("e1")}},
		{"get_tree", func() (any, error) { return client.GetTree() }, `{"type":"get_tree"`, GetTreeResult{Tree: []SessionTreeNode{{Entry: SessionEntry{Type: "message", ID: "e1", Timestamp: "t1", Raw: json.RawMessage(`{"id":"e1","parentId":null,"timestamp":"t1","type":"message"}`)}, Children: []SessionTreeNode{}, Label: str("L"), LabelTimestamp: str("t2")}}}},
		{"get_last_assistant_text", func() (any, error) { return client.GetLastAssistantText() }, `{"type":"get_last_assistant_text"`, str("last words")},
		{"set_session_name", func() (any, error) { return nil, client.SetSessionName("renamed") }, `{"type":"set_session_name","name":"renamed"`, nil},
		{"get_messages", func() (any, error) { return client.GetMessages() }, `{"type":"get_messages"`, []AgentMessage{{Role: "user", Raw: json.RawMessage(`{"content":"hi","role":"user"}`)}, {Role: "assistant", Raw: json.RawMessage(`{"role":"assistant"}`)}}},
		{"get_commands", func() (any, error) { return client.GetCommands() }, `{"type":"get_commands"`, []RpcSlashCommand{{Name: "skill:x", Description: "d", Source: "skill", SourceInfo: RpcSourceInfo{Path: "/p", Source: "local", Scope: "project", Origin: "top-level", BaseDir: "/b"}}}},
	}
	for index, r := range rows {
		got, err := r.call()
		if err != nil {
			t.Fatalf("%s: %v", r.name, err)
		}
		if !reflect.DeepEqual(got, r.want) {
			t.Errorf("%s: result\n got %#v\nwant %#v", r.name, got, r.want)
		}
		logged := loggedCommands(t, logPath)
		if len(logged) != index+2 {
			t.Fatalf("%s: %d logged lines, want %d", r.name, len(logged), index+2)
		}
		if want := fmt.Sprintf(`%s,"id":"req_%d"}`, r.wire, index+1); logged[index+1] != want {
			t.Errorf("%s: wire\n got %s\nwant %s", r.name, logged[index+1], want)
		}
	}
}

// upstream: rpc-client.ts:223-233,248-255,257-264,360-368 (cycleModel, cycleThinkingLevel and getLastAssistantText types `| null`), 631-641 (getData). A null result is Pi's null, and a failed response throws its error text.
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:251 (RpcClient.getState); packages/coding-agent/src/modes/rpc/rpc-client.ts:267 (RpcClient.cycleModel); packages/coding-agent/src/modes/rpc/rpc-client.ts:294 (RpcClient.cycleThinkingLevel); packages/coding-agent/src/modes/rpc/rpc-client.ts:353 (RpcClient.bash); packages/coding-agent/src/modes/rpc/rpc-client.ts:435 (RpcClient.getLastAssistantText).
func TestRpcClientNullResultsAndErrorResponses(t *testing.T) {
	client, _ := scriptedClient(t, RpcClientOptions{}, map[string]any{"responses": map[string]any{
		"cycle_model":             map[string]any{"data": nil},
		"cycle_thinking_level":    map[string]any{"data": nil},
		"get_last_assistant_text": map[string]any{"data": map[string]any{"text": nil}},
		"get_state":               map[string]any{"success": false, "error": "no session"},
		"bash":                    map[string]any{"success": false, "error": "Bash is already running"},
	}})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if got, err := client.CycleModel(); err != nil || got != nil {
		t.Fatalf("CycleModel = %v, %v; want nil", got, err)
	}
	if got, err := client.CycleThinkingLevel(); err != nil || got != nil {
		t.Fatalf("CycleThinkingLevel = %v, %v; want nil", got, err)
	}
	if got, err := client.GetLastAssistantText(); err != nil || got != nil {
		t.Fatalf("GetLastAssistantText = %v, %v; want nil", got, err)
	}
	if _, err := client.GetState(); err == nil || err.Error() != "no session" {
		t.Fatalf("GetState error = %v", err)
	}
	if _, err := client.Bash("sleep 1"); err == nil || err.Error() != "Bash is already running" {
		t.Fatalf("Bash error = %v", err)
	}
}

// upstream: rpc-client.ts:19-33,60-87 (RpcClientOptions, start): the child runs `<cli> --mode rpc [--provider p] [--model m] ...args` in cwd with the process environment plus env.
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (RpcClient.abort).
func TestRpcClientOptionsReachTheSpawnedProcess(t *testing.T) {
	cwd := t.TempDir()
	client, logPath := scriptedClient(t, RpcClientOptions{Cwd: cwd, Provider: "prov", Model: "mod", Args: []string{"--no-session", "--x", "y"}, Env: map[string]string{"PIG_RPCCLIENT_TEST_MARKER": "from-options"}}, map[string]any{})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := client.Abort(); err != nil {
		t.Fatal(err)
	}
	var header struct {
		Argv   []string `json:"argv"`
		Cwd    string   `json:"cwd"`
		Marker string   `json:"marker"`
	}
	if err := json.Unmarshal([]byte(loggedCommands(t, logPath)[0]), &header); err != nil {
		t.Fatal(err)
	}
	if want := []string{"--mode", "rpc", "--provider", "prov", "--model", "mod", "--no-session", "--x", "y"}; !slices.Equal(header.Argv, want) {
		t.Fatalf("argv = %q, want %q", header.Argv, want)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err != nil || header.Cwd != resolved {
		t.Fatalf("cwd = %q, want %q (%v)", header.Cwd, resolved, err)
	}
	if header.Marker != "from-options" {
		t.Fatalf("env marker = %q", header.Marker)
	}
	// A bare client passes only --mode rpc.
	bare, bareLog := scriptedClient(t, RpcClientOptions{}, map[string]any{})
	if err := bare.Start(); err != nil {
		t.Fatal(err)
	}
	if err := bare.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(loggedCommands(t, bareLog)[0]), &header); err != nil || !slices.Equal(header.Argv, []string{"--mode", "rpc"}) {
		t.Fatalf("bare argv = %q, %v", header.Argv, err)
	}
}

// upstream: rpc-client.ts:111-124 (stderr collection), 133-150 (stop), 154-165 (onEvent unsubscribe), 488-492 (promptAndWait). The events come from the child after the prompt response, so collection must already be running.
// mutation-checked: zeroing the results of RpcClient.GetStderr fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:185 (getStderr)
// mutation-checked: zeroing the results of RpcClient.Abort, RpcClient.OnEvent fails it
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (abort)
// Pi: packages/coding-agent/src/modes/rpc/rpc-client.ts:172 (onEvent)
// packages/coding-agent/src/modes/rpc/rpc-client.ts:74 (RpcClient.start); packages/coding-agent/src/modes/rpc/rpc-client.ts:145 (RpcClient.stop); packages/coding-agent/src/modes/rpc/rpc-client.ts:172 (RpcClient.onEvent); packages/coding-agent/src/modes/rpc/rpc-client.ts:185 (RpcClient.getStderr); packages/coding-agent/src/modes/rpc/rpc-client.ts:226 (RpcClient.abort); packages/coding-agent/src/modes/rpc/rpc-client.ts:513 (RpcClient.promptAndWait).
func TestRpcClientEventHelpersStderrAndStop(t *testing.T) {
	events := []map[string]any{{"type": "agent_start"}, {"type": "turn_start"}, {"type": "agent_settled"}}
	encoded := make([]json.RawMessage, len(events))
	for i, event := range events {
		encoded[i], _ = json.Marshal(event)
	}
	client, _ := scriptedClient(t, RpcClientOptions{}, map[string]any{"stderr": "warming up\n", "events": encoded, "responses": map[string]any{"prompt": map[string]any{"data": map[string]any{"disposition": "started"}}}})
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(client.GetStderr(), "warming up") {
		if time.Now().After(deadline) {
			t.Fatalf("stderr = %q", client.GetStderr())
		}
		time.Sleep(10 * time.Millisecond)
	}
	got, err := client.PromptAndWait("go", nil, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(got))
	for i, event := range got {
		types[i] = event.Type
	}
	if want := []string{"agent_start", "turn_start", "agent_settled"}; !slices.Equal(types, want) {
		t.Fatalf("PromptAndWait events = %q, want %q", types, want)
	}
	if string(got[1].Raw) != `{"type":"turn_start"}` {
		t.Fatalf("raw record = %s", got[1].Raw)
	}
	// An unsubscribed listener hears nothing more; a subscribed one hears every record in order.
	var mu sync.Mutex
	var heard, kept []string
	unsubscribe := client.OnEvent(func(event JsonAgentSessionEvent) { mu.Lock(); heard = append(heard, event.Type); mu.Unlock() })
	unsubscribe()
	client.OnEvent(func(event JsonAgentSessionEvent) { mu.Lock(); kept = append(kept, event.Type); mu.Unlock() })
	if _, err := client.PromptAndWait("again", nil, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heard) != 0 || !slices.Equal(kept, []string{"agent_start", "turn_start", "agent_settled"}) {
		t.Fatalf("unsubscribed listener heard %q, subscribed listener heard %q", heard, kept)
	}
	client.Stop()
	if err := client.Abort(); err == nil || err.Error() != "Client not started" {
		t.Fatalf("command after Stop: %v", err)
	}
}
