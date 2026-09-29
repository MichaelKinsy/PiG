package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

func registryTool(name, label, description, snippet string, guidelines ...string) extension.ToolDefinition {
	return extension.ToolDefinition{Name: name, Label: label, Description: description, PromptSnippet: snippet, PromptGuidelines: guidelines, Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{}}, nil
	}}
}
func dynamicRegistryTool() extension.ToolDefinition {
	return registryTool("dynamic_tool", "Dynamic Tool", "Tool registered from session_start", "Run dynamic test behavior")
}
func registerPortTool(registry map[string]extension.RegisteredTool, definition extension.ToolDefinition) {
	registry[definition.Name] = extension.RegisteredTool{Definition: definition}
}

func newRegistryPortSession(t *testing.T, defaults []string, opts SessionOptions, static []extension.ToolDefinition, start func(*Session, map[string]extension.RegisteredTool)) *Session {
	t.Helper()
	home, dir := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{}
	if defaults != nil {
		settings["defaultTools"] = defaults
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: dir, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	registered := make(map[string]extension.RegisteredTool)
	for _, definition := range static {
		registerPortTool(registered, definition)
	}
	var session *Session
	if start != nil || len(static) > 0 {
		opts.Runner = inproc.NewRunner([]extension.Extension{{Path: "<inline:1>", SourceInfo: icodingagent.PiSourceInfo{Path: "<inline:1>", Source: "inline", Scope: "temporary", Origin: "top-level"}, Tools: registered, Handlers: map[string][]extension.HandlerFn{"session_start": {func(...any) (any, error) {
			if start != nil {
				start(session, registered)
			}
			return nil, nil
		}}}}}, dir)
	}
	if opts.Model == nil {
		opts.Model = fakeModel()
	}
	opts.SessionDir = filepath.Join(agentDir, "sessions")
	session, err = NewSession(services, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	return session
}
func bindRegistryPort(t *testing.T, session *Session) {
	t.Helper()
	if err := session.BindExtensions(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func allRegistryNames(session *Session) []string {
	names := []string{}
	for _, tool := range session.GetAllTools() {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}
func assertRegistryNames(t *testing.T, session *Session, all, active []string) {
	t.Helper()
	if got := allRegistryNames(session); !slices.Equal(got, all) {
		t.Fatalf("all = %q, want %q", got, all)
	}
	if got := session.ActiveToolNames(); !slices.Equal(got, active) {
		t.Fatalf("active = %q, want %q", got, active)
	}
}
func assertRegistryPrompt(t *testing.T, session *Session, present, absent []string) {
	t.Helper()
	prompt := session.systemPrompt()
	for _, part := range present {
		if !strings.Contains(prompt, part) {
			t.Errorf("prompt lacks %q: %s", part, prompt)
		}
	}
	for _, part := range absent {
		if strings.Contains(prompt, part) {
			t.Errorf("prompt contains %q: %s", part, prompt)
		}
	}
}
func printRegistryPort(t *testing.T, name string, session *Session) {
	t.Helper()
	lines := []string{}
	for line := range strings.SplitSeq(session.systemPrompt(), "\n") {
		if strings.HasPrefix(line, "- ") && (strings.Contains(line, "dynamic_tool:") || strings.Contains(line, "grep:") || strings.Contains(line, "powershell:") || strings.Contains(line, "read:") || strings.Contains(line, "bash:") || strings.Contains(line, "edit:") || strings.Contains(line, "write:") || strings.Contains(line, "find:") || strings.Contains(line, "ls:")) {
			lines = append(lines, line)
		}
	}
	data, err := json.Marshal([]any{allRegistryNames(session), session.ActiveToolNames(), lines})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("REGISTRY %s %s\n", name, data)
}

func TestDefaultToolsInitialSelectionPort(t *testing.T) {
	for _, tc := range []struct {
		name            string
		defaults        []string
		present, absent []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/default-tools-setting.test.ts:58
		{"uses the configured list as the initial built-in selection", []string{"grep", "find"}, []string{"- grep:"}, []string{"- read:"}},
		// .upstream/v0.87.1/packages/coding-agent/test/default-tools-setting.test.ts:73
		{"can select powershell instead of bash", []string{"read", "powershell", "edit", "write"}, []string{"- powershell: Execute PowerShell commands"}, []string{"- bash:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, tc.defaults, SessionOptions{}, nil, nil)
			assertRegistryNames(t, session, []string{"bash", "edit", "find", "grep", "ls", "powershell", "read", "write"}, tc.defaults)
			assertRegistryPrompt(t, session, tc.present, tc.absent)
			printRegistryPort(t, tc.defaults[0], session)
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/default-tools-setting.test.ts:82
	t.Run("keeps extension and SDK custom tools enabled", func(t *testing.T) {
		sdk := registryTool("sdk_tool", "SDK Tool", "SDK custom tool", "")
		static := registryTool("static_tool", "Static Tool", "Statically registered extension tool", "")
		session := newRegistryPortSession(t, []string{"grep"}, SessionOptions{CustomTools: []extension.ToolDefinition{sdk}}, []extension.ToolDefinition{static}, func(_ *Session, registered map[string]extension.RegisteredTool) {
			registerPortTool(registered, registryTool("dynamic_tool", "Dynamic Tool", "Dynamically registered extension tool", ""))
		})
		bindRegistryPort(t, session)
		active := session.ActiveToolNames()
		slices.Sort(active)
		if !slices.Equal(active, []string{"dynamic_tool", "grep", "sdk_tool", "static_tool"}) {
			t.Fatal(active)
		}
		for _, name := range []string{"read", "dynamic_tool", "sdk_tool", "static_tool"} {
			if !slices.Contains(allRegistryNames(session), name) {
				t.Fatal("missing " + name)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/default-tools-setting.test.ts:126
	t.Run("preserves explicit tool option precedence", func(t *testing.T) {
		allowed := newRegistryPortSession(t, []string{"grep"}, SessionOptions{AllowedTools: map[string]struct{}{"read": {}}}, nil, nil)
		if !slices.Equal(allowed.ActiveToolNames(), []string{"read"}) {
			t.Fatal(allowed.ActiveToolNames())
		}
		excluded := newRegistryPortSession(t, []string{"read", "grep"}, SessionOptions{ExcludedTools: map[string]struct{}{"read": {}}}, nil, nil)
		if !slices.Equal(excluded.ActiveToolNames(), []string{"grep"}) {
			t.Fatal(excluded.ActiveToolNames())
		}
		none := newRegistryPortSession(t, []string{"read"}, SessionOptions{NoTools: "all"}, nil, nil)
		assertRegistryNames(t, none, []string{}, []string{})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/default-tools-setting.test.ts:141
	t.Run("applies through service-based session creation", func(t *testing.T) {
		session := newRegistryPortSession(t, []string{"ls"}, SessionOptions{}, nil, nil)
		assertRegistryNames(t, session, []string{"bash", "edit", "find", "grep", "ls", "powershell", "read", "write"}, []string{"ls"})
	})
}

func TestToolAllowlistExtensionPort(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		allowed                      map[string]struct{}
		all, active, present, absent []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/2835-tools-allowlist-filters-extension-tools.test.ts:68
		{"allows only explicitly listed built-in and extension tools", map[string]struct{}{"read": {}, "dynamic_tool": {}}, []string{"dynamic_tool", "read"}, []string{"read", "dynamic_tool"}, []string{"- read: Read file contents", "- dynamic_tool: Run dynamic test behavior"}, []string{"- bash:", "- edit:"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/2835-tools-allowlist-filters-extension-tools.test.ts:85
		{"disables all tools when the allowlist is empty", map[string]struct{}{}, []string{}, []string{}, []string{"<tools>\n(none)\n"}, []string{"dynamic_tool"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: tc.allowed}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
				registerPortTool(registered, dynamicRegistryTool())
			})
			bindRegistryPort(t, session)
			assertRegistryNames(t, session, tc.all, tc.active)
			assertRegistryPrompt(t, session, tc.present, tc.absent)
			printRegistryPort(t, "allow", session)
		})
	}
}

func TestNoBuiltinToolsExtensionPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3592-no-builtin-tools-keeps-extension-tools.test.ts:73
	t.Run("keeps extension tools active when built-in defaults are disabled", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin"}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
			registerPortTool(registered, dynamicRegistryTool())
		})
		bindRegistryPort(t, session)
		assertRegistryNames(t, session, []string{"bash", "dynamic_tool", "edit", "find", "grep", "ls", "powershell", "read", "write"}, []string{"dynamic_tool"})
		assertRegistryPrompt(t, session, []string{"- dynamic_tool: Run dynamic test behavior"}, []string{"- read:", "- bash:"})
		printRegistryPort(t, "no-builtin", session)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3592-no-builtin-tools-keeps-extension-tools.test.ts:89
	t.Run("still disables all tools when noTools is all", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{NoTools: "all"}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
			registerPortTool(registered, dynamicRegistryTool())
		})
		bindRegistryPort(t, session)
		assertRegistryNames(t, session, []string{}, []string{})
		assertRegistryPrompt(t, session, []string{"<tools>\n(none)\n"}, nil)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/3592-no-builtin-tools-keeps-extension-tools.test.ts:98
	t.Run("propagates noTools through service-based session creation", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin"}, nil, nil)
		if len(session.ActiveToolNames()) != 0 {
			t.Fatal(session.ActiveToolNames())
		}
		assertRegistryPrompt(t, session, []string{"<tools>\n(none)\n"}, []string{"- read:"})
	})
}

func TestExcludeToolsExtensionPort(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed map[string]struct{}
		want    []string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5109-exclude-tools.test.ts:40
		{"filters built-in and extension tools from available and active tools", nil, []string{"bash", "dynamic_tool", "edit", "write"}},
		// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5109-exclude-tools.test.ts:62
		{"lets excluded tools override the allowlist", map[string]struct{}{"read": {}, "bash": {}, "ask_question": {}}, []string{"bash"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: tc.allowed, ExcludedTools: map[string]struct{}{"read": {}, "ask_question": {}}}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
				registerPortTool(registered, registryTool("ask_question", "Ask Question", "Ask a question", "Ask a question"))
				registerPortTool(registered, dynamicRegistryTool())
			})
			bindRegistryPort(t, session)
			all := allRegistryNames(session)
			active := session.ActiveToolNames()
			slices.Sort(active)
			if !slices.Equal(active, tc.want) || slices.Contains(all, "read") || slices.Contains(all, "ask_question") {
				t.Fatalf("all %v, active %v", all, active)
			}
			if tc.allowed == nil {
				if !slices.Contains(all, "bash") || !slices.Contains(all, "dynamic_tool") {
					t.Fatal(all)
				}
				assertRegistryPrompt(t, session, []string{"- dynamic_tool: Run dynamic test behavior"}, []string{"- read:", "ask_question"})
			} else {
				if !slices.Equal(all, []string{"bash"}) {
					t.Fatal(all)
				}
				assertRegistryPrompt(t, session, []string{"- bash:"}, []string{"- read:", "ask_question"})
			}
			printRegistryPort(t, "exclude", session)
		})
	}
}

type registryPortOperations func(context.Context, string, string, extension.BashOperationsExecOptions) (extension.BashOperationsResult, error)

func (f registryPortOperations) Exec(ctx context.Context, command, cwd string, opts extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	return f(ctx, command, cwd, opts)
}

type registryPortProvider struct{ fakeProvider }

func (registryPortProvider) ID() string { return "anthropic" }

func TestAgentSessionDynamicToolsPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:29
	t.Run("exposes session state before custom bash spawn hooks and supports opting out", func(t *testing.T) {
		var exposed, hidden []string
		makeBash := func(hide bool) extension.ToolDefinition {
			bash := &tools.BashTool{CWD: t.TempDir(), HideSessionEnvironment: hide, Operations: registryPortOperations(func(ctx context.Context, command, cwd string, opts extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
				if hide {
					hidden = slices.Clone(opts.Env)
				} else {
					exposed = slices.Clone(opts.Env)
				}
				return tools.NewLocalBashOperations(nil, "").Exec(ctx, command, cwd, opts)
			})}
			definition, err := toolDefinition(bash)
			if err != nil {
				t.Fatal(err)
			}
			if hide {
				definition.Name = "bash_without_session_env"
				definition.Label = "bash without session env"
			}
			return definition
		}
		model := &ai.Model{ID: "claude-sonnet-4-5", Provider: registryPortProvider{}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
		session := newRegistryPortSession(t, nil, SessionOptions{SessionID: "bash-env-test", Model: model}, []extension.ToolDefinition{makeBash(false), makeBash(true)}, nil)
		if err := session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
			t.Fatal(err)
		}
		assertRegistryPrompt(t, session, []string{"You can inspect PI_* environment variables for current model and session details."}, nil)
		for _, tool := range session.Tools() {
			if tool.Name() == "bash" || tool.Name() == "bash_without_session_env" {
				result, err := tool.Execute(t.Context(), "bash-env", json.RawMessage(`{"command":"printf ok"}`), nil)
				if err != nil || result.IsError || result.Text() != "ok" {
					t.Fatalf("result %+v, %v", result, err)
				}
			}
		}
		for key, want := range map[string]string{"PI_SESSION_ID": session.ID(), "PI_SESSION_FILE": session.Path(), "PI_PROVIDER": "anthropic", "PI_MODEL": model.ID, "PI_REASONING_LEVEL": string(session.ThinkingLevel())} {
			if !slices.Contains(exposed, key+"="+want) {
				t.Fatalf("missing %s=%s from exposed session metadata", key, want)
			}
			for _, value := range hidden {
				if strings.HasPrefix(value, key+"=") {
					t.Fatal("opt-out exposed " + key)
				}
			}
		}
		fmt.Println("REGISTRY env validated")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:99
	t.Run("refreshes tool registry when tools are registered after initialization", func(t *testing.T) {
		guideline := "Use dynamic_tool when the user asks for dynamic behavior tests."
		session := newRegistryPortSession(t, nil, SessionOptions{}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
			definition := dynamicRegistryTool()
			definition.PromptGuidelines = []string{guideline}
			registerPortTool(registered, definition)
		})
		if slices.Contains(allRegistryNames(session), "dynamic_tool") {
			t.Fatal("dynamic tool registered before session_start")
		}
		bindRegistryPort(t, session)
		var dynamic, read extension.ToolInfo
		for _, info := range session.GetAllTools() {
			if info.Name == "dynamic_tool" {
				dynamic = info
			}
			if info.Name == "read" {
				read = info
			}
		}
		if dynamic.Name == "" || !slices.Equal(dynamic.PromptGuidelines, []string{guideline}) || !reflect.DeepEqual(dynamic.SourceInfo, icodingagent.PiSourceInfo{Path: "<inline:1>", Source: "inline", Scope: "temporary", Origin: "top-level"}) {
			t.Fatalf("dynamic = %+v", dynamic)
		}
		if !reflect.DeepEqual(read.SourceInfo, syntheticToolSource("read", "builtin")) {
			t.Fatalf("read = %+v", read)
		}
		if !slices.Contains(session.ActiveToolNames(), "dynamic_tool") {
			t.Fatal(session.ActiveToolNames())
		}
		assertRegistryPrompt(t, session, []string{"- dynamic_tool: Run dynamic test behavior", "- " + guideline}, nil)
		printRegistryPort(t, "dynamic", session)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:168
	t.Run("returns source metadata for SDK custom tools", func(t *testing.T) {
		definition := registryTool("sdk_tool", "SDK Tool", "Tool registered through createAgentSession", "")
		session := newRegistryPortSession(t, nil, SessionOptions{CustomTools: []extension.ToolDefinition{definition}}, nil, nil)
		var sdk extension.ToolInfo
		for _, info := range session.GetAllTools() {
			if info.Name == "sdk_tool" {
				sdk = info
			}
		}
		if !reflect.DeepEqual(sdk.SourceInfo, syntheticToolSource("sdk_tool", "sdk")) || !slices.Contains(session.ActiveToolNames(), "sdk_tool") {
			t.Fatalf("tool = %+v, active = %v", sdk, session.ActiveToolNames())
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:211
	t.Run("keeps custom tools active but omits them from available tools when promptSnippet is not provided", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
			registerPortTool(registered, registryTool("hidden_tool", "Hidden Tool", "Description should not appear in available tools", ""))
		})
		bindRegistryPort(t, session)
		if !slices.Contains(allRegistryNames(session), "hidden_tool") || !slices.Contains(session.ActiveToolNames(), "hidden_tool") {
			t.Fatal("hidden tool missing from registry or active set")
		}
		assertRegistryPrompt(t, session, nil, []string{"hidden_tool", "Description should not appear in available tools"})
	})
}
