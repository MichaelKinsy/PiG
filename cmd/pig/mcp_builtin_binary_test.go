//go:build !pig_strip_mcp

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// The built-in MCP extension in the real binary: `builtin:mcp` is loaded in every mode (print, JSON, RPC) the way Pi's
// extensions/index.ts registers it, so the servers of `mcp.json` connect in the Session, their tools reach codemode and
// tool_search per exposure, the `mcp_servers` prompt section lists them, and a server that is slow to start does not hold
// the first prompt. The model is a local OpenAI-compatible endpoint the test scripts; the MCP server is a real stdio
// process (testdata/mcp-stdio-server.go).

// chatRequest is one request the binary sent to the scripted model.
type chatRequest struct {
	System   string
	Tools    []string
	Messages []map[string]any
	// Results are the contents of the tool messages.
	Results []string
}

type chatStep func(request chatRequest) chatReply

// chatReply is a model answer: text, or tool calls (name, JSON arguments).
type chatReply struct {
	Text  string
	Calls [][2]string
}

func chatText(text string) chatStep {
	return func(chatRequest) chatReply { return chatReply{Text: text} }
}

func chatCall(name string, arguments map[string]any) chatStep {
	raw, _ := json.Marshal(arguments)
	return func(chatRequest) chatReply { return chatReply{Calls: [][2]string{{name, string(raw)}}} }
}

// scriptedChatServer serves /v1/chat/completions, answering request n with steps[n] and recording the requests.
type scriptedChatServer struct {
	URL string

	mu       sync.Mutex
	steps    []chatStep
	requests []chatRequest
}

func (s *scriptedChatServer) recorded() []chatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func startScriptedChatServer(t *testing.T, steps ...chatStep) *scriptedChatServer {
	t.Helper()
	s := &scriptedChatServer{steps: steps}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		request := chatRequest{Messages: body.Messages}
		for _, tool := range body.Tools {
			request.Tools = append(request.Tools, tool.Function.Name)
		}
		for _, message := range body.Messages {
			content, _ := message["content"].(string)
			switch message["role"] {
			case "system", "developer":
				request.System += content
			case "tool":
				request.Results = append(request.Results, content)
			}
		}
		s.mu.Lock()
		index := len(s.requests)
		s.requests = append(s.requests, request)
		s.mu.Unlock()
		reply := chatReply{Text: "no scripted answer"}
		if index < len(s.steps) {
			reply = s.steps[index](request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta map[string]any, finish any) {
			data, _ := json.Marshal(map[string]any{"id": "chatcmpl-1", "object": "chat.completion.chunk", "created": 0, "model": "test", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		if len(reply.Calls) > 0 {
			for i, call := range reply.Calls {
				chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": i, "id": fmt.Sprintf("call_%d_%d", index, i), "type": "function", "function": map[string]any{"name": call[0], "arguments": call[1]}}}}, nil)
			}
			chunk(map[string]any{}, "tool_calls")
		} else {
			chunk(map[string]any{"role": "assistant", "content": reply.Text}, nil)
			chunk(map[string]any{}, "stop")
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	s.URL = server.URL + "/v1"
	return s
}

type mcpBinaryServer struct {
	name   string
	config map[string]any
	// env is the environment of the server process.
	env map[string]string
}

type mcpBinaryRun struct {
	mode    string
	servers []mcpBinaryServer
	steps   []chatStep
	// prompt is the user message. In RPC mode, prompts are sent one after the other, each after the agent settled.
	prompt  string
	prompts []string
	// extensions are loaded with -e; each gets MCP_REGISTER_COMMAND (the fixture server) and MCP_REGISTER_LOG in its environment.
	extensions []string
}

type mcpBinaryResult struct {
	chat   *scriptedChatServer
	output string
	log    []string
}

// runPigWithMCP runs the real binary in mode against a scripted model, with the servers in the global `mcp.json`.
func runPigWithMCP(t *testing.T, run mcpBinaryRun) mcpBinaryResult {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
	server := testExecutable(filepath.Join(t.TempDir(), "mcp-stdio-server"))
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", server, "./testdata/mcp-stdio-server.go").CombinedOutput(); err != nil {
		t.Fatalf("build the MCP fixture server: %v\n%s", err, out)
	}
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "pig")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	seedFirstRunDone(t, agentDir)
	chat := startScriptedChatServer(t, run.steps...)
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
		"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
	}}})
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "mcp-calls.log")
	servers := map[string]any{}
	for _, s := range run.servers {
		config := map[string]any{"command": server, "args": []string{s.name}, "env": map[string]string{"MCP_FIXTURE_LOG": log}}
		maps.Copy(config["env"].(map[string]string), s.env)
		maps.Copy(config, s.config)
		servers[s.name] = config
	}
	mcpJSON, _ := json.Marshal(map[string]any{"mcpServers": servers})
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), mcpJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--session-dir", t.TempDir()}
	for _, path := range run.extensions {
		absolute, err := filepath.Abs(path)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", absolute)
	}
	ctx := t.Context()
	var cmd *exec.Cmd
	switch run.mode {
	case "print":
		cmd = exec.CommandContext(ctx, binary, append(args, "--print", run.prompt)...)
	case "json":
		cmd = exec.CommandContext(ctx, binary, append(args, "--mode", "json", run.prompt)...)
	case "rpc":
		cmd = exec.CommandContext(ctx, binary, append(args, "--mode", "rpc")...)
	case "interactive":
		cmd = exec.CommandContext(ctx, binary, args...)
	default:
		t.Fatalf("unknown mode %q", run.mode)
	}
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "MCP_REGISTER_COMMAND="+server, "MCP_REGISTER_LOG="+log)
	var output strings.Builder
	prompts := run.prompts
	if prompts == nil {
		prompts = []string{run.prompt}
	}
	switch run.mode {
	case "interactive":
		driveInteractivePrompts(t, cmd, prompts, func() int { return len(chat.recorded()) }, len(run.steps), &output)
	case "rpc":
		driveRPCPrompts(t, cmd, prompts, &output)
	default:
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		output.Write(out)
		if err != nil {
			t.Fatalf("%s mode: %v\n%s", run.mode, err, out)
		}
	}
	result := mcpBinaryResult{chat: chat, output: output.String()}
	if data, err := os.ReadFile(log); err == nil {
		result.log = strings.Split(strings.TrimSpace(string(data)), "\n")
	}
	return result
}

// driveRPCPrompts starts cmd in RPC mode and sends each prompt after the agent settled from the one before, then closes
// stdin so the runtime disposes as it does at input end (rpc-mode.ts).
func driveRPCPrompts(t *testing.T, cmd *exec.Cmd, prompts []string, output *strings.Builder) {
	t.Helper()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	settled := make(chan struct{}, len(prompts))
	var mu sync.Mutex
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(nil, 16<<20)
		for scanner.Scan() {
			mu.Lock()
			output.WriteString(scanner.Text() + "\n")
			mu.Unlock()
			var event struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Type == "agent_settled" {
				settled <- struct{}{}
			}
		}
	}()
	for i, prompt := range prompts {
		command, _ := json.Marshal(map[string]any{"id": fmt.Sprint(i), "type": "prompt", "message": prompt})
		if _, err := fmt.Fprintf(stdin, "%s\n", command); err != nil {
			t.Fatal(err)
		}
		select {
		case <-settled:
		case <-time.After(testbudget.Wait(t)):
			_ = cmd.Process.Kill()
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("the agent did not settle after prompt %d\nstdout:\n%s\nstderr:\n%s", i, output.String(), stderr.String())
		}
	}
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("rpc mode: %v\n%s", err, stderr.String())
	}
}

// The tool of the `docs` server, called as a codemode script calls it.
const docsSearchScript = `return (await tools.mcp__docs__search({ query: "q" })).content[0].text;`

// With a `codemode` server (the default exposure) codemode is active and MCP tools are never declared to the model; the
// `mcp_servers` section of the system prompt lists the server; a script finds and calls the tool; and a server that never
// answers `initialize` neither holds the first prompt nor keeps a script that does not name it waiting. The expectations
// follow Pi 0.99.2: extensions/mcp/index.ts (before_agent_start, tool_call, waitForDirectServers), mcp-servers.ts
// (renderServersSection) and the extensions list in extensions/index.ts.
func TestBuiltinMCPExtensionServesCodemodeServersInEveryMode(t *testing.T) {
	for _, mode := range []string{"print", "json", "rpc", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			result := runPigWithMCP(t, mcpBinaryRun{
				mode:   mode,
				prompt: "go",
				servers: []mcpBinaryServer{
					{name: "docs", env: map[string]string{"MCP_FIXTURE_INSTRUCTIONS": "Docs search.\nLong guidance."}},
					{name: "slow", env: map[string]string{"MCP_FIXTURE_INITIALIZE_DELAY_MS": "-1"}},
				},
				steps: []chatStep{chatCall("codemode", map[string]any{"code": docsSearchScript}), chatText("done")},
			})
			requests := result.chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
			}
			first := requests[0]
			if !slices.Contains(first.Tools, "codemode") {
				t.Errorf("tools = %v, want codemode declared: codemode servers activate it", first.Tools)
			}
			for _, tool := range first.Tools {
				if strings.HasPrefix(tool, "mcp__") {
					t.Errorf("tools = %v, want no MCP tool declared to the model", first.Tools)
				}
			}
			// The slow server is listed by name: its instructions are not known until it connects. The `docs` server connects
			// within the wait for servers that a script names, and is listed with the first line of its instructions.
			if !strings.Contains(first.System, "- mcp__slow (codemode)\n") {
				t.Errorf("system prompt lacks the slow server:\n%s", first.System)
			}
			if !strings.Contains(first.System, "mcp__docs (codemode)") {
				t.Errorf("system prompt lacks the docs server:\n%s", first.System)
			}
			if len(requests[1].Results) != 1 || !strings.Contains(requests[1].Results[0], "q guide") {
				t.Errorf("tool results = %q, want the codemode script's result %q", requests[1].Results, "q guide")
			}
			if !slices.Equal(result.log, []string{`docs:search:{"query":"q"}`}) {
				t.Errorf("server calls = %q", result.log)
			}
		})
	}
}

// With a `deferred` server tool_search is active, waits for the servers, and loads the matching tool, which the model
// calls on its next request (extensions/mcp/index.ts tool_call, tool-search/tool.ts).
func TestBuiltinMCPExtensionServesDeferredServersThroughToolSearch(t *testing.T) {
	for _, mode := range []string{"print", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			result := runPigWithMCP(t, mcpBinaryRun{
				mode:    mode,
				prompt:  "go",
				servers: []mcpBinaryServer{{name: "docs", config: map[string]any{"exposure": "deferred"}}},
				steps: []chatStep{
					chatCall("tool_search", map[string]any{"query": "search the docs", "limit": 1}),
					chatCall("mcp__docs__search", map[string]any{"query": "loaded"}),
					chatText("done"),
				},
			})
			requests := result.chat.recorded()
			if len(requests) != 3 {
				t.Fatalf("requests = %d, want 3\n%s", len(requests), result.output)
			}
			if !slices.Contains(requests[0].Tools, "tool_search") || slices.Contains(requests[0].Tools, "mcp__docs__search") {
				t.Errorf("first request tools = %v, want tool_search without the deferred tool", requests[0].Tools)
			}
			if !slices.Contains(requests[1].Tools, "mcp__docs__search") {
				t.Errorf("second request tools = %v, want the loaded tool declared", requests[1].Tools)
			}
			if !slices.Equal(result.log, []string{`docs:search:{"query":"loaded"}`}) {
				t.Errorf("server calls = %q", result.log)
			}
			if mode == "print" && !strings.HasSuffix(strings.TrimSpace(result.output), "done") {
				t.Errorf("output = %q", result.output)
			}
		})
	}
}

// A `direct` server's tools are declared to the model on the first request: the first prompt waits for servers with
// direct tools (extensions/mcp/index.ts waitForDirectServers).
func TestBuiltinMCPExtensionDeclaresDirectServerToolsOnTheFirstRequest(t *testing.T) {
	for _, mode := range []string{"print", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			result := runPigWithMCP(t, mcpBinaryRun{
				mode:    mode,
				prompt:  "go",
				servers: []mcpBinaryServer{{name: "docs", config: map[string]any{"exposure": "direct"}}},
				steps:   []chatStep{chatCall("mcp__docs__search", map[string]any{"query": "direct"}), chatText("done")},
			})
			requests := result.chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
			}
			if !slices.Contains(requests[0].Tools, "mcp__docs__search") || slices.Contains(requests[0].Tools, "codemode") {
				t.Errorf("first request tools = %v, want the direct tool and no codemode", requests[0].Tools)
			}
			if strings.Contains(requests[0].System, "- mcp__docs (") {
				t.Errorf("a server with only direct tools is not listed in the servers section:\n%s", requests[0].System)
			}
			if len(requests[1].Results) != 1 || requests[1].Results[0] != "direct guide" {
				t.Errorf("tool results = %q", requests[1].Results)
			}
		})
	}
}

// The `mcp_servers` section is listed again with a server's summary once it connects: the next prompt appends the patch
// (extensions/mcp/index.ts before_agent_start). The first script names `docs`, so the server has connected by then.
func TestBuiltinMCPExtensionUpdatesTheServersSectionWithTheNextPrompt(t *testing.T) {
	result := runPigWithMCP(t, mcpBinaryRun{
		mode:    "rpc",
		prompts: []string{"first", "second"},
		servers: []mcpBinaryServer{{name: "docs", env: map[string]string{"MCP_FIXTURE_INSTRUCTIONS": "Docs search.\nLong guidance."}}},
		steps:   []chatStep{chatCall("codemode", map[string]any{"code": docsSearchScript}), chatText("one"), chatText("two")},
	})
	requests := result.chat.recorded()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3\n%s", len(requests), result.output)
	}
	if !strings.Contains(requests[0].System, "mcp__docs (codemode)") {
		t.Errorf("first system prompt lacks the docs server:\n%s", requests[0].System)
	}
	if strings.Contains(requests[1].System, "Docs search.") {
		t.Errorf("the summary arrives with the next prompt, not within the turn:\n%s", requests[1].System)
	}
	if !strings.Contains(requests[2].System, "- mcp__docs (codemode): Docs search.") || strings.Contains(requests[2].System, "Long guidance") {
		t.Errorf("second prompt's system messages lack the server summary:\n%s", requests[2].System)
	}
}

// A server a loaded extension registers reaches the built-in MCP extension in every mode: the Session's runner shares the
// extension host's registrations, so mcp_servers_change fires for it and a codemode script calls its tool (Pi's runner and
// the extension runtime are one object, runner.ts registerMcpServer / emit mcp_servers_change).
func TestBuiltinMCPExtensionConnectsServersRegisteredByExtensions(t *testing.T) {
	for _, mode := range []string{"print", "json", "rpc", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			result := runPigWithMCP(t, mcpBinaryRun{
				mode:       mode,
				prompt:     "go",
				extensions: []string{"testdata/mcp/register-stdio.mjs"},
				steps: []chatStep{
					chatCall("codemode", map[string]any{"code": `return (await tools.mcp__bundled__search({ query: "q" })).content[0].text;`}),
					chatText("done"),
				},
			})
			requests := result.chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), result.output)
			}
			if !strings.Contains(requests[0].System, "mcp__bundled (codemode)") {
				t.Errorf("system prompt lacks the registered server:\n%s", requests[0].System)
			}
			if len(requests[1].Results) != 1 || !strings.Contains(requests[1].Results[0], "q guide") {
				t.Errorf("tool results = %q, want the registered server's result", requests[1].Results)
			}
			if !slices.Equal(result.log, []string{`bundled:search:{"query":"q"}`}) {
				t.Errorf("server calls = %q", result.log)
			}
		})
	}
}
