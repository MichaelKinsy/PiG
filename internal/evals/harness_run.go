package evals

// Ports packages/evals/src/harness.ts runPiCodingAgent and createPiCodingAgentHarness (harness.ts:272-482).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/evals/bridge"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// PiCodingAgentStepType selects what a step of an input does.
type PiCodingAgentStepType string

const (
	// PiCodingAgentStepPrompt sends Content to the agent.
	PiCodingAgentStepPrompt PiCodingAgentStepType = "prompt"
	// PiCodingAgentStepReload reloads the Session's resources and extensions.
	PiCodingAgentStepReload PiCodingAgentStepType = "reload"
)

// PiCodingAgentStep is one step of a PiCodingAgentInput.
type PiCodingAgentStep struct {
	Type    PiCodingAgentStepType `json:"type"`
	Content string                `json:"content,omitempty"`
}

// PiCodingAgentInput is the steps of one run. A plain string input is PromptInput.
type PiCodingAgentInput []PiCodingAgentStep

// PromptInput is the input that sends one prompt.
func PromptInput(content string) PiCodingAgentInput {
	return PiCodingAgentInput{{Type: PiCodingAgentStepPrompt, Content: content}}
}

// CustomTool is a tool the harness serves to the agent through the bridge extension (harness.ts customTools).
type CustomTool struct {
	bridge.Tool
	// Execute runs the tool; it is called on the harness side with the model's arguments.
	Execute func(ctx context.Context, toolCallID string, params map[string]any) (CustomToolResult, error)
}

// CustomToolResult is a custom tool's result (AgentToolResult content, details and terminate).
type CustomToolResult struct {
	Content   string
	Details   any
	Terminate bool
}

// AgentRun is what a harness output callback inspects after the last step: the run's answer, the system prompt the
// model received, the isolated agent and workspace directories, the final Session messages as RPC JSON, and what
// the extension host reported.
type AgentRun struct {
	Response     string
	SystemPrompt string
	AgentDir     string
	Workspace    string
	// Model and PigPath identify the Session's model and the pig binary, for outputs that start more pig processes.
	Model            PiCodingAgentModelSelection
	PigPath          string
	Messages         []map[string]any
	ExtensionErrors  []json.RawMessage
	SuccessfulTools  map[string]string
	SessionStatistic SessionStatistics
}

// SessionStatistics are the Session totals the agent process reports (get_session_stats).
type SessionStatistics struct {
	SessionFile string
	ToolCalls   float64
	Input       float64
	Output      float64
	CacheRead   float64
	CacheWrite  float64
	Total       float64
	Cost        float64
}

// PiCodingAgentOutput computes a run's structured output (PiCodingAgentHarnessWithOutput.output).
type PiCodingAgentOutput func(ctx context.Context, run AgentRun) (any, error)

// TranscriptEvent is one normalized transcript event.
type TranscriptEvent = map[string]any

// UsageSummary is a run's model usage.
type UsageSummary struct {
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	InputTokens  float64        `json:"inputTokens"`
	OutputTokens float64        `json:"outputTokens"`
	TotalTokens  float64        `json:"totalTokens"`
	ToolCalls    float64        `json:"toolCalls"`
	Metadata     map[string]any `json:"metadata"`
}

// HarnessRun is the normalized result of one run.
type HarnessRun struct {
	// Name is the harness name (createHarness name), "pi-coding-agent" unless the options name it.
	Name      string
	Output    any
	Events    []TranscriptEvent
	Metadata  map[string]any
	Usage     UsageSummary
	TotalMs   float64
	Artifacts map[string]any
	Errors    []error
}

// HarnessRunError is a failed run that still measured something (attachHarnessRunToError).
type HarnessRunError struct {
	Err error
	Run *HarnessRun
}

func (failure *HarnessRunError) Error() string { return failure.Err.Error() }
func (failure *HarnessRunError) Unwrap() error { return failure.Err }

var credentialEnvironmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// contentText is pi-ai contentText over decoded RPC JSON: a string, or the text blocks joined by a newline.
func contentText(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, _ := content.([]any)
	var texts []string
	for _, block := range blocks {
		if record, ok := block.(map[string]any); ok && record["type"] == "text" {
			text, _ := record["text"].(string)
			texts = append(texts, text)
		}
	}
	return strings.Join(texts, "\n")
}

// toTranscriptEvents is harness.ts toTranscriptEvents over the Session's RPC messages.
func toTranscriptEvents(messages []map[string]any) []TranscriptEvent {
	events := []TranscriptEvent{}
	for _, message := range messages {
		switch message["role"] {
		case "user":
			events = append(events, TranscriptEvent{"type": "message", "role": "user", "content": contentText(message["content"])})
		case "assistant":
			if text := contentText(message["content"]); text != "" {
				events = append(events, TranscriptEvent{"type": "message", "role": "assistant", "content": text})
			}
			parts, _ := message["content"].([]any)
			for _, part := range parts {
				record, _ := part.(map[string]any)
				if record["type"] != "toolCall" {
					continue
				}
				arguments, _ := record["arguments"].(map[string]any)
				if arguments == nil {
					arguments = map[string]any{}
				}
				events = append(events, TranscriptEvent{"type": "tool_call", "id": record["id"], "name": record["name"], "arguments": arguments})
			}
		case "toolResult":
			text := contentText(message["content"])
			content := any(text)
			if parts, ok := message["content"].([]any); ok {
				for _, part := range parts {
					if record, ok := part.(map[string]any); !ok || record["type"] != "text" {
						content = message["content"]
						break
					}
				}
			}
			event := TranscriptEvent{"type": "tool_result", "toolCallId": message["toolCallId"], "name": message["toolName"], "content": content}
			if failed, _ := message["isError"].(bool); failed {
				if text == "" {
					text = "Tool failed"
				}
				event["error"] = map[string]any{"message": text}
			}
			events = append(events, event)
		}
	}
	return events
}

// seedWorkspace writes the fixture files into workspace, rejecting absolute and escaping paths.
func seedWorkspace(workspace string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	// Object.entries order is insertion order; Go maps have none, so write in a fixed order.
	slices.SortFunc(names, compareUTF16)
	for _, name := range names {
		if name == "" || filepath.IsAbs(name) {
			return fmt.Errorf("Invalid workspace fixture path: %s", name)
		}
		path := filepath.Join(workspace, name)
		relative, err := filepath.Rel(workspace, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("Workspace fixture escapes the workspace: %s", name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// bridgeServer serves the harness side of the bridge socket.
type bridgeServer struct {
	listener net.Listener
	options  PiCodingAgentHarnessOptions
	// credential is the API key the credential command fetches; empty when the run hands over no API key.
	credential string
	mu         sync.Mutex
	forced     *string
	wg         sync.WaitGroup
	ctx        context.Context
}

func startBridge(ctx context.Context, socket string, options PiCodingAgentHarnessOptions, credential string) (*bridgeServer, error) {
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	server := &bridgeServer{listener: listener, options: options, credential: credential, ctx: ctx}
	server.wg.Go(func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			server.wg.Go(func() { server.serve(connection) })
		}
	})
	return server, nil
}

func (server *bridgeServer) serve(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	var request bridge.Request
	if err := json.NewDecoder(connection).Decode(&request); err != nil {
		return
	}
	response := bridge.Response{}
	switch request.Op {
	case bridge.OpTransform:
		transformed, err := server.options.TransformSystemPrompt(request.SystemPrompt)
		if err != nil {
			response.Error = err.Error()
			break
		}
		server.mu.Lock()
		server.forced = &transformed
		server.mu.Unlock()
		response.SystemPrompt = transformed
	case bridge.OpTool:
		response.Error = "unknown tool " + request.Tool
		for _, tool := range server.options.CustomTools {
			if tool.Name != request.Tool {
				continue
			}
			result, err := tool.Execute(server.ctx, request.ToolCallID, request.Params)
			response = bridge.Response{Content: result.Content, Details: result.Details, Terminate: result.Terminate}
			if err != nil {
				response.Error = err.Error()
			}
		}
	case bridge.OpCredential:
		response.Credential = server.credential
		if response.Credential == "" {
			response.Error = "no API key was handed to this run"
		}
	default:
		response.Error = "unknown operation " + request.Op
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func (server *bridgeServer) forcedSystemPrompt() *string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.forced
}

func (server *bridgeServer) close() {
	_ = server.listener.Close()
	server.wg.Wait()
}

func resolveBinary(configured, environment, fallback string) (string, error) {
	if configured == "" {
		configured = os.Getenv(environment)
	}
	if configured != "" {
		return configured, nil
	}
	return exec.LookPath(fallback)
}

// shellQuote quotes value as one word of the shell that runs a "!" command: cmd.exe on Windows (a path cannot contain
// a double quote), a POSIX shell elsewhere.
func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		return `"` + value + `"`
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// apiKeyHandOff returns the credential entry the agent's auth.json receives and the API key the bridge serves. Pi keeps
// the credential in the model runtime's memory (harness.ts:312-326). An API key stays in the harness: the entry's key is
// a "!" command that fetches it from the bridge socket, which the agent's file tools cannot open. Any other credential
// (OAuth) is handed over as stored.
func apiKeyHandOff(stored json.RawMessage, apiKey, extensionPath string) (entry json.RawMessage, served string, err error) {
	fields := map[string]any{}
	if stored != nil {
		if err := json.Unmarshal(stored, &fields); err != nil {
			return nil, "", err
		}
	}
	if (stored != nil && fields["type"] != "api_key") || apiKey == "" {
		return stored, "", nil
	}
	if extensionPath == "" {
		return nil, "", errors.New("Set PI_EVAL_EXTENSION to the pig-eval-extension binary.")
	}
	fields["type"] = "api_key"
	fields["key"] = "!" + shellQuote(extensionPath) + " " + bridge.CredentialCommand
	entry, err = json.Marshal(fields)
	return entry, apiKey, err
}

func tokenCount(stats map[string]any, path ...string) float64 {
	var value any = stats
	for _, key := range path {
		record, _ := value.(map[string]any)
		value = record[key]
	}
	number, _ := value.(float64)
	return number
}

// RunPiCodingAgent runs input against a fresh PiG Session in an isolated root and normalizes the result. The root,
// home, agent directory, workspace and Session directory are new for each run and removed afterwards.
func RunPiCodingAgent(ctx context.Context, input PiCodingAgentInput, options PiCodingAgentHarnessOptions) (result *HarnessRun, failure error) {
	startedAt := time.Now()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selection, err := ResolveModelSelection(options.Model)
	if err != nil {
		return nil, err
	}
	hostAgentDir := hostAgentDirectory()
	// ApplyIsolatedEnvironment removes the PI_EVAL_* variables, so the binaries are named before it runs.
	pigPath, err := resolveBinary(options.PigPath, "PI_EVAL_PIG", "pig")
	if err != nil {
		return nil, err
	}
	// A missing extension binary matters only to a run that needs the bridge.
	extensionPath, _ := resolveBinary(options.ExtensionPath, "PI_EVAL_EXTENSION", bridge.ExtensionName)
	sandbox, err := resolveSandboxIdentity()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp("", "pi-eval-")
	if err != nil {
		return nil, err
	}
	workspace := filepath.Join(root, "workspace")
	isolatedHome := filepath.Join(root, "home")
	agentDir := filepath.Join(isolatedHome, ".pig", "agent")

	var (
		process          *agentProcess
		server           *bridgeServer
		runDiagnostics   *HarnessRun
		runError         error
		cleanupErrors    []error
		hiddenCredential *[2]string
		sessionPath      string
		artifacts        = map[string]any{}
	)
	restoreEnvironment := ApplyIsolatedEnvironment(isolatedHome, agentDir)
	defer func() {
		if process != nil {
			if err := process.close(); err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
		}
		if server != nil {
			server.close()
		}
		if err := os.RemoveAll(root); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
		restoreEnvironment()
		if hiddenCredential != nil {
			_ = os.Setenv(hiddenCredential[0], hiddenCredential[1])
		}
		failure = combineFailures(runError, cleanupErrors)
		if failure != nil {
			result = nil
			if runDiagnostics != nil {
				runDiagnostics.Errors = []error{failure}
				runDiagnostics.TotalMs = float64(time.Since(startedAt)) / float64(time.Millisecond)
				failure = &HarnessRunError{Err: failure, Run: runDiagnostics}
			}
		}
	}()

	runError = func() error {
		authPath := filepath.Join(hostAgentDir, "auth.json")
		credentials := icodingagent.ReadStoredCredentialEntry(selection.Provider, authPath)
		if err := os.MkdirAll(workspace, 0o777); err != nil {
			return err
		}
		if err := os.MkdirAll(agentDir, 0o777); err != nil {
			return err
		}
		if err := seedWorkspace(workspace, options.WorkspaceFiles); err != nil {
			return err
		}
		if err := seedWorkspace(agentDir, options.agentFiles); err != nil {
			return err
		}
		model, auth, err := resolveEvalModel(ctx, authPath, agentDir, selection)
		if err != nil {
			return err
		}
		credentials, servedKey, err := apiKeyHandOff(credentials, auth.Auth.APIKey, extensionPath)
		if err != nil {
			return err
		}
		if credentials != nil {
			// pig divergence (D98): an OAuth credential is written to the auth.json the pig process reads; Pi keeps it
			// in memory. An API key entry holds only the command that fetches the key from the bridge.
			encoded, _ := json.Marshal(map[string]json.RawMessage{selection.Provider: credentials})
			if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), encoded, 0o600); err != nil {
				return err
			}
		}
		if sandbox != nil {
			// The runner's own auth.json is removed before the agent runs, as Pi does, so the sandboxed agent cannot read
			// the host credentials.
			if err := os.Remove(authPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if auth.Source != "" && credentialEnvironmentName.MatchString(auth.Source) {
				if value := os.Getenv(auth.Source); value != "" {
					hiddenCredential = &[2]string{auth.Source, value}
					_ = os.Unsetenv(auth.Source)
				}
			}
		}

		spec := bridge.Spec{TransformSystemPrompt: options.TransformSystemPrompt != nil}
		for _, tool := range options.CustomTools {
			spec.Tools = append(spec.Tools, tool.Tool)
		}
		useExtension := spec.TransformSystemPrompt || len(spec.Tools) > 0
		if useExtension {
			if extensionPath == "" {
				return errors.New("Set PI_EVAL_EXTENSION to the pig-eval-extension binary.")
			}
			specPath := filepath.Join(root, "bridge.json")
			encoded, _ := json.Marshal(spec)
			if err := os.WriteFile(specPath, encoded, 0o644); err != nil {
				return err
			}
		}
		if useExtension || servedKey != "" {
			if server, err = startBridge(ctx, filepath.Join(root, "bridge.sock"), options, servedKey); err != nil {
				return err
			}
		}
		launch := func(sessionFile string) (*exec.Cmd, error) {
			args := []string{"--mode", "rpc", "--session-dir", filepath.Join(root, "sessions"), "--model", selection.Provider + "/" + selection.ID + ":off"}
			switch {
			case options.Tools != nil:
				args = append(args, "--tools", strings.Join(options.Tools, ","))
			case options.NoTools == "all":
				args = append(args, "--no-tools")
			case options.NoTools == "builtin":
				args = append(args, "--no-builtin-tools")
			}
			if useExtension {
				args = append(args, "--extension", extensionPath)
			}
			if sessionFile != "" {
				args = append(args, "--session", sessionFile)
			}
			command := exec.CommandContext(context.WithoutCancel(ctx), pigPath, args...)
			command.Dir = workspace
			command.Env = os.Environ()
			if server != nil {
				command.Env = append(command.Env, bridge.EnvSocket+"="+filepath.Join(root, "bridge.sock"))
			}
			if useExtension {
				command.Env = append(command.Env, bridge.EnvSpec+"="+filepath.Join(root, "bridge.json"))
			}
			return command, enterToolSandbox(command, root, sandbox)
		}

		var response string
		var transcript, agentEndTranscript []json.RawMessage
		var extensionErrors []json.RawMessage
		successfulTools := map[string]string{}
		previousMessageCount := 0
		checkedExtensions := false
		ensureProcess := func() error {
			if process != nil {
				return nil
			}
			command, err := launch(sessionPath)
			if err != nil {
				return err
			}
			if process, err = startAgentProcess(command); err != nil {
				return err
			}
			process.onEvent = func(line rpcLine) {
				if line.typ == "extension_error" {
					extensionErrors = append(extensionErrors, line.raw)
				}
			}
			// The Session ID names the run (setArtifact("runId", sessionManager.getSessionId())); a reload keeps the
			// Session file and therefore its ID.
			stateData, err := process.request(ctx, map[string]any{"type": "get_state"})
			if err != nil {
				return err
			}
			if _, recorded := artifacts["runId"]; !recorded {
				var state struct {
					SessionID string `json:"sessionId"`
				}
				if err := json.Unmarshal(stateData, &state); err != nil {
					return err
				}
				artifacts["runId"] = state.SessionID
			}
			if !checkedExtensions {
				checkedExtensions = true
				if err := verifyLoadedExtensions(ctx, process, extensionPath, useExtension); err != nil {
					return err
				}
			}
			// pig materializes its embedded documentation under the home's config root when it starts. Pi's
			// without_docs image has no coding-agent docs on disk (README.md, CHANGELOG.md, docs/, examples/ are removed),
			// so this variant removes them once startup has finished.
			if options.ExpectedPiDocumentation != nil && !*options.ExpectedPiDocumentation {
				return removePigDocumentation(filepath.Join(isolatedHome, ".pig"))
			}
			return nil
		}
		sessionFile := func() error {
			data, err := process.request(ctx, map[string]any{"type": "get_session_stats"})
			if err != nil {
				return err
			}
			var stats struct {
				SessionFile string `json:"sessionFile"`
			}
			_ = json.Unmarshal(data, &stats)
			sessionPath = stats.SessionFile
			return nil
		}
		// A decoded map loses the sections' order, so the raw messages are kept for the system prompt.
		messagesNow := func() ([]map[string]any, error) {
			data, err := process.request(ctx, map[string]any{"type": "get_messages"})
			if err != nil {
				return nil, err
			}
			var decoded struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				return nil, err
			}
			messages := make([]map[string]any, len(decoded.Messages))
			for i, raw := range decoded.Messages {
				if err := json.Unmarshal(raw, &messages[i]); err != nil {
					return nil, err
				}
			}
			transcript = decoded.Messages
			return messages, nil
		}

		for _, step := range input {
			if err := ctx.Err(); err != nil {
				return err
			}
			if step.Type == PiCodingAgentStepReload {
				if process != nil {
					if err := sessionFile(); err != nil {
						return err
					}
					if err := process.close(); err != nil {
						return err
					}
					process = nil
				}
				continue
			}
			if err := ensureProcess(); err != nil {
				return err
			}
			if err := sessionFile(); err != nil {
				return err
			}
			before, err := messagesNow()
			if err != nil {
				return err
			}
			previousMessageCount = len(before)
			agentEnd, err := process.prompt(ctx, step.Content)
			if err != nil {
				return err
			}
			after, err := messagesNow()
			if err != nil {
				return err
			}
			if response, err = settledResponse(ctx, process, after, previousMessageCount); err != nil {
				return err
			}
			if !hasSystemMessage(transcript) {
				agentEndTranscript = agentEnd
			}
			if err := recordSuccessfulTools(after, successfulTools); err != nil {
				return err
			}
		}
		if process == nil {
			if len(input) == 0 {
				return errors.New("Pi eval input must include at least one prompt step.")
			}
			if err := ensureProcess(); err != nil {
				return err
			}
		}
		hasPrompt := false
		for _, step := range input {
			hasPrompt = hasPrompt || step.Type == PiCodingAgentStepPrompt
		}
		if !hasPrompt {
			return errors.New("Pi eval input must include at least one prompt step.")
		}
		if err := sessionFile(); err != nil {
			return err
		}
		messages, err := messagesNow()
		if err != nil {
			return err
		}
		if !hasSystemMessage(transcript) {
			transcript = agentEndTranscript
		}
		systemPrompt, err := currentSystemPrompt(transcript)
		if err != nil {
			return err
		}
		if server != nil {
			if forced := server.forcedSystemPrompt(); forced != nil {
				systemPrompt = *forced
			}
		}
		statsData, err := process.request(ctx, map[string]any{"type": "get_session_stats"})
		if err != nil {
			return err
		}
		var stats map[string]any
		_ = json.Unmarshal(statsData, &stats)
		statistics := SessionStatistics{
			SessionFile: sessionPath, ToolCalls: tokenCount(stats, "toolCalls"), Input: tokenCount(stats, "tokens", "input"),
			Output: tokenCount(stats, "tokens", "output"), CacheRead: tokenCount(stats, "tokens", "cacheRead"),
			CacheWrite: tokenCount(stats, "tokens", "cacheWrite"), Total: tokenCount(stats, "tokens", "total"), Cost: tokenCount(stats, "cost"),
		}
		digest := sha256.Sum256([]byte(systemPrompt))
		usageMetadata := map[string]any{"cacheReadTokens": statistics.CacheRead, "cacheWriteTokens": statistics.CacheWrite}
		if modelHasPricing(model) {
			usageMetadata["estimatedCostUsd"] = statistics.Cost
		}
		runDiagnostics = &HarnessRun{
			Name:     harnessName(options),
			Events:   toTranscriptEvents(messages),
			Metadata: map[string]any{"systemPromptSha256": hex.EncodeToString(digest[:])},
			Usage: UsageSummary{Provider: model.ProviderID(), Model: model.ID, InputTokens: statistics.Input, OutputTokens: statistics.Output,
				TotalTokens: statistics.Total, ToolCalls: statistics.ToolCalls, Metadata: usageMetadata},
			Artifacts: artifacts,
		}
		if _, err := VerifySystemPrompt(systemPrompt, options); err != nil {
			return err
		}
		var output any = response
		if options.Output != nil {
			if output, err = options.Output(ctx, AgentRun{Response: response, SystemPrompt: systemPrompt, AgentDir: agentDir, Workspace: workspace, Model: selection, PigPath: pigPath,
				Messages: messages, ExtensionErrors: extensionErrors, SuccessfulTools: successfulTools, SessionStatistic: statistics}); err != nil {
				return err
			}
		}
		runDiagnostics.Output = output
		return nil
	}()

	if sessionPath != "" {
		if data, err := os.ReadFile(sessionPath); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("Pi eval produced no session file."))
		} else {
			artifacts[PiSessionSnapshotArtifact] = string(data)
		}
	} else if runError == nil || process != nil {
		cleanupErrors = append(cleanupErrors, errors.New("Pi eval produced no session file."))
	}
	if runError != nil || len(cleanupErrors) > 0 {
		return nil, nil
	}
	runDiagnostics.TotalMs = float64(time.Since(startedAt)) / float64(time.Millisecond)
	return runDiagnostics, nil
}

func combineFailures(runError error, cleanupErrors []error) error {
	switch {
	case runError != nil && len(cleanupErrors) > 0:
		return fmt.Errorf("Agent run failed and cleanup also failed.: %w", errors.Join(append([]error{runError}, cleanupErrors...)...))
	case runError != nil:
		return runError
	case len(cleanupErrors) == 1:
		return cleanupErrors[0]
	case len(cleanupErrors) > 1:
		return fmt.Errorf("Agent cleanup failed.: %w", errors.Join(cleanupErrors...))
	}
	return nil
}

// settledResponse is promptAgent's checks over the messages the prompt added: the last assistant message must have
// stopped or called a tool, and the run must have produced text.
func settledResponse(ctx context.Context, process *agentProcess, messages []map[string]any, previousMessageCount int) (string, error) {
	var assistant map[string]any
	if previousMessageCount <= len(messages) {
		for _, message := range messages[previousMessageCount:] {
			if message["role"] == "assistant" {
				assistant = message
			}
		}
	}
	if assistant == nil {
		return "", errors.New("Agent run completed without an assistant message.")
	}
	stopReason, _ := assistant["stopReason"].(string)
	if stopReason != "stop" && stopReason != "toolUse" {
		// harness.ts promptAgent: `assistant.errorMessage ?? fallback` keeps an empty message; only an absent or null one falls back.
		if message, ok := assistant["errorMessage"].(string); ok {
			return "", errors.New(message)
		}
		return "", fmt.Errorf("Agent run ended with unexpected stop reason: %s.", stopReason)
	}
	data, err := process.request(ctx, map[string]any{"type": "get_last_assistant_text"})
	if err != nil {
		return "", err
	}
	var last struct {
		Text *string `json:"text"`
	}
	_ = json.Unmarshal(data, &last)
	output := ""
	if last.Text != nil {
		output = *last.Text
	}
	if output == "" && stopReason == "stop" {
		return "", errors.New("Agent run produced no assistant text.")
	}
	return output, nil
}

// recordSuccessfulTools notes each tool that returned a non-error result.
func recordSuccessfulTools(messages []map[string]any, into map[string]string) error {
	for _, message := range messages {
		if message["role"] != "toolResult" {
			continue
		}
		if failed, _ := message["isError"].(bool); failed {
			continue
		}
		if name, ok := message["toolName"].(string); ok {
			into[name] = contentText(message["content"])
		}
	}
	return nil
}

// systemRole reads a raw message's role.
func systemRole(raw json.RawMessage) bool {
	var message struct {
		Role string `json:"role"`
	}
	return json.Unmarshal(raw, &message) == nil && message.Role == "system"
}

func hasSystemMessage(messages []json.RawMessage) bool {
	return slices.ContainsFunc(messages, systemRole)
}

// currentSystemPrompt replays every system message of the Session's RPC messages into the current prompt
// (getCurrentSystemPrompt): later content is appended and sections are patched by name.
func currentSystemPrompt(messages []json.RawMessage) (string, error) {
	var system []ai.Message
	for _, raw := range messages {
		if !systemRole(raw) {
			continue
		}
		var message agent.AgentMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return "", err
		}
		if message.System != nil {
			system = append(system, *message.System)
		}
	}
	return ai.GetCurrentSystemPrompt(system), nil
}

func modelHasPricing(model *ai.Model) bool {
	cost := model.CostRates()
	if cost.Input > 0 || cost.Output > 0 || cost.CacheRead > 0 || cost.CacheWrite > 0 {
		return true
	}
	for _, tier := range cost.Tiers {
		if tier.InputCostPer1M > 0 || tier.OutputCostPer1M > 0 || tier.CacheReadCostPer1M > 0 || tier.CacheWriteCostPer1M > 0 {
			return true
		}
	}
	return false
}

// resolveEvalModel finds the selected model in the isolated agent directory's configuration and resolves its auth
// with the host's credentials, as ModelRuntime.create({ credentials }) does under the isolated environment.
func resolveEvalModel(ctx context.Context, hostAuthPath, agentDir string, selection PiCodingAgentModelSelection) (*ai.Model, *ai.AuthResult, error) {
	modelsPath := new(filepath.Join(agentDir, "models.json"))
	runtime, err := coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{
		AuthPath:        hostAuthPath,
		ModelsPath:      &modelsPath,
		ModelsStorePath: filepath.Join(agentDir, "models-store.json"),
	})
	if err != nil {
		return nil, nil, err
	}
	defer runtime.Close()
	model := runtime.GetModel(selection.Provider, selection.ID)
	if model == nil {
		return nil, nil, fmt.Errorf("Eval model not found: %s/%s", selection.Provider, selection.ID)
	}
	auth, err := runtime.GetModelAuth(ctx, model)
	if err != nil || auth == nil {
		return nil, nil, fmt.Errorf("Eval model has no configured authentication: %s/%s", selection.Provider, selection.ID)
	}
	return model, auth, nil
}

func harnessName(options PiCodingAgentHarnessOptions) string {
	if options.Name != "" {
		return options.Name
	}
	return "pi-coding-agent"
}

// builtinExtensionPrefix starts the path of each extension pig always loads (mcp, codemode and the others).
const builtinExtensionPrefix = "builtin:"

// verifyLoadedExtensions is harness.ts:357-363: the isolated Session must load no extension except the harness's own.
// pig lists its loaded extensions through the get_extensions RPC command; the built-in extensions are PiG's stock
// set and the bridge extension is the harness's inline extension.
func verifyLoadedExtensions(ctx context.Context, process *agentProcess, bridgePath string, expectBridge bool) error {
	data, err := process.request(ctx, map[string]any{"type": "get_extensions"})
	if err != nil {
		return err
	}
	var listed struct {
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(data, &listed); err != nil {
		return err
	}
	resolve := func(path string) string {
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			return resolved
		}
		return path
	}
	var unexpected []string
	for _, path := range listed.Paths {
		if strings.HasPrefix(path, builtinExtensionPrefix) || (expectBridge && resolve(path) == resolve(bridgePath)) {
			continue
		}
		unexpected = append(unexpected, path)
	}
	if len(unexpected) > 0 {
		return fmt.Errorf("Isolated eval loaded unexpected extensions: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

// removePigDocumentation is the without_docs variant's removal of the coding-agent documentation from disk
// (entrypoint.ts:48-52 asserts README.md, CHANGELOG.md, docs and examples are gone). pig materializes only docs/ under
// its config root, and its prompt points at GitHub for examples (D22), so the other names are removed only if present.
func removePigDocumentation(configRoot string) error {
	for _, path := range []string{pigdocs.DocsDir(configRoot), filepath.Join(configRoot, "README.md"), filepath.Join(configRoot, "CHANGELOG.md"), filepath.Join(configRoot, "examples")} {
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}
