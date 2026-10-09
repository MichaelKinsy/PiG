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
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: agentDir})
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
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
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
		// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:58
		{"uses the configured list as the initial built-in selection", []string{"grep", "find"}, []string{"- grep:"}, []string{"- read:"}},
		// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:73
		{"can select powershell instead of bash", []string{"read", "powershell", "edit", "write"}, []string{"- powershell: Execute PowerShell commands"}, []string{"- bash:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := newRegistryPortSession(t, tc.defaults, SessionOptions{}, nil, nil)
			assertRegistryNames(t, session, []string{"bash", "edit", "find", "grep", "ls", "powershell", "read", "write"}, tc.defaults)
			assertRegistryPrompt(t, session, tc.present, tc.absent)
			printRegistryPort(t, tc.defaults[0], session)
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:82
	// .upstream/v0.99.2/packages/coding-agent/test/default-tools-setting.test.ts:82-98: the tool registers with `defaultActive: false`, so only the `+name` entry activates it.
	t.Run("activates an inactive extension tool with +name", func(t *testing.T) {
		inactive := registryTool("inactive_tool", "Inactive Tool", "Extension tool registered inactive", "")
		defaultActive := false
		inactive.DefaultActive = &defaultActive
		session := newRegistryPortSession(t, []string{"+inactive_tool", "-write"}, SessionOptions{}, []extension.ToolDefinition{inactive}, nil)
		bindRegistryPort(t, session)
		active := session.ActiveToolNames()
		slices.Sort(active)
		if !slices.Equal(active, []string{"bash", "edit", "inactive_tool", "read"}) {
			t.Fatal(active)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:100
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
	// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:144
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
	// .upstream/v0.99.1/packages/coding-agent/test/default-tools-setting.test.ts:159
	t.Run("applies through service-based session creation", func(t *testing.T) {
		session := newRegistryPortSession(t, []string{"ls"}, SessionOptions{}, nil, nil)
		assertRegistryNames(t, session, []string{"bash", "edit", "find", "grep", "ls", "powershell", "read", "write"}, []string{"ls"})
	})
}

// writeRegistryPortSettings replaces the session's global settings file, as the reload tests of default-tools-setting.test.ts do.
func writeRegistryPortSettings(t *testing.T, session *Session, settings map[string]any) {
	t.Helper()
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session.services.AgentDir(), "settings.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// reloadRegistryPort is upstream session.reload() (agent-session.ts:3591-3603) without resources: the settings reload, then the runtime rebuild that activates tools newly added to defaultTools. Interactive mode interleaves resource and extension reload between the two steps.
func reloadRegistryPort(t *testing.T, session *Session) {
	t.Helper()
	session.ReloadSettings()
	if err := session.RefreshTools(); err != nil {
		t.Fatal(err)
	}
}

func inactiveRegistryTool() extension.ToolDefinition {
	inactive := registryTool("inactive_tool", "Inactive Tool", "Extension tool registered inactive", "")
	inactive.DefaultActive = new(false)
	return inactive
}

func sortedActiveNames(session *Session) []string {
	active := session.ActiveToolNames()
	slices.Sort(active)
	return active
}

// .upstream/v0.99.2/packages/coding-agent/test/default-tools-setting.test.ts:159-235 (#10245): /reload enables tools newly added to the defaultTools setting.
func TestDefaultToolsReloadPort(t *testing.T) {
	// default-tools-setting.test.ts:193-207
	t.Run("activates only tools newly added to defaultTools", func(t *testing.T) {
		session := newRegistryPortSession(t, nil, SessionOptions{}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
		bindRegistryPort(t, session)
		if got := session.ActiveToolNames(); !slices.Equal(got, []string{"read", "bash", "edit", "write"}) {
			t.Fatalf("initial active = %q", got)
		}
		session.SetActiveToolsByName([]string{"read", "edit", "write"})

		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"+inactive_tool", "+grep"}})
		reloadRegistryPort(t, session)
		// bash was disabled during the session and is not newly added, so it stays off.
		if got, want := sortedActiveNames(session), []string{"edit", "grep", "inactive_tool", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active after adding = %q, want %q", got, want)
		}

		// Removing tools from the setting does not disable them.
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"-read"}})
		reloadRegistryPort(t, session)
		if got, want := sortedActiveNames(session), []string{"edit", "grep", "inactive_tool", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("active after removing = %q, want %q", got, want)
		}
	})
	// Pi 1.1.0 default-tools-setting.test.ts:258-267: a tool a `--tools -name` entry removed stays removed when a reload brings it back through the setting.
	t.Run("keeps tools removed by -name tool options removed on reload", func(t *testing.T) {
		session := newRegistryPortSession(t, []string{"read"}, SessionOptions{
			DefaultToolModifiers:   []string{"-bash", "+grep"},
			InitialActiveToolNames: []string{"read", "grep"},
		}, []extension.ToolDefinition{inactiveRegistryTool()}, nil)
		bindRegistryPort(t, session)
		if got := session.ActiveToolNames(); !slices.Equal(got, []string{"read", "grep"}) {
			t.Fatalf("initial active = %q", got)
		}
		writeRegistryPortSettings(t, session, map[string]any{"defaultTools": []string{"read", "bash", "inactive_tool"}})
		reloadRegistryPort(t, session)
		if got, want := sortedActiveNames(session), []string{"grep", "inactive_tool", "read"}; !slices.Equal(got, want) {
			t.Fatalf("active after reload = %q, want %q", got, want)
		}
	})
	// default-tools-setting.test.ts:209-233
	t.Run("keeps explicit tool options on reload", func(t *testing.T) {
		static := []extension.ToolDefinition{inactiveRegistryTool()}
		allowlisted := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: map[string]struct{}{"read": {}}}, static, nil)
		bindRegistryPort(t, allowlisted)
		writeRegistryPortSettings(t, allowlisted, map[string]any{"defaultTools": []string{"+grep"}})
		reloadRegistryPort(t, allowlisted)
		if got := allowlisted.ActiveToolNames(); !slices.Equal(got, []string{"read"}) {
			t.Fatalf("allowlisted active = %q", got)
		}

		builtinless := newRegistryPortSession(t, nil, SessionOptions{NoTools: "builtin"}, static, nil)
		bindRegistryPort(t, builtinless)
		writeRegistryPortSettings(t, builtinless, map[string]any{"defaultTools": []string{"+grep"}})
		reloadRegistryPort(t, builtinless)
		if got := builtinless.ActiveToolNames(); len(got) != 0 {
			t.Fatalf("no-builtin active = %q", got)
		}

		excluded := newRegistryPortSession(t, nil, SessionOptions{ExcludedTools: map[string]struct{}{"grep": {}}}, static, nil)
		bindRegistryPort(t, excluded)
		writeRegistryPortSettings(t, excluded, map[string]any{"defaultTools": []string{"+grep", "+inactive_tool"}})
		reloadRegistryPort(t, excluded)
		if got, want := sortedActiveNames(excluded), []string{"bash", "edit", "inactive_tool", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("excluded active = %q, want %q", got, want)
		}
	})
	// agent-session.ts:3591-3603: the CLI selects the initial built-ins itself (InitialActiveToolNames, --no-builtin-tools as SkipBuiltinTools), which sdk.ts:448 treats as the default selection or a noTools override.
	t.Run("treats the CLI's resolved defaults as default tools and --no-builtin-tools as an override", func(t *testing.T) {
		static := []extension.ToolDefinition{inactiveRegistryTool()}
		defaults := newRegistryPortSession(t, nil, SessionOptions{InitialActiveToolNames: []string{"read", "bash", "edit", "write"}}, static, nil)
		bindRegistryPort(t, defaults)
		writeRegistryPortSettings(t, defaults, map[string]any{"defaultTools": []string{"+grep"}})
		reloadRegistryPort(t, defaults)
		if got, want := sortedActiveNames(defaults), []string{"bash", "edit", "grep", "read", "write"}; !slices.Equal(got, want) {
			t.Fatalf("CLI defaults active = %q, want %q", got, want)
		}

		skipped := newRegistryPortSession(t, nil, SessionOptions{SkipBuiltinTools: true}, static, nil)
		bindRegistryPort(t, skipped)
		writeRegistryPortSettings(t, skipped, map[string]any{"defaultTools": []string{"+grep"}})
		reloadRegistryPort(t, skipped)
		if got := sortedActiveNames(skipped); slices.Contains(got, "grep") {
			t.Fatalf("--no-builtin-tools active = %q, want no grep", got)
		}
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

// Pi's tools option starts the named tools active in the given order, built-in and extension tools alike, each once at
// its first position: sdk.ts:271-276 passes options.tools as both the allowlist and initialActiveToolNames, and
// agent-session.ts:3552-3562 activates them before the allowlisted registry tools and dedupes with a Set. A name no tool
// has is skipped.
func TestAllowedToolsStartActiveInTheirOrder(t *testing.T) {
	tools := []string{"grep", "dynamic_tool", "missing", "read", "grep"}
	allowed := map[string]struct{}{}
	for _, name := range tools {
		allowed[name] = struct{}{}
	}
	session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: allowed, InitialActiveToolNames: tools}, []extension.ToolDefinition{dynamicRegistryTool()}, nil)
	if got, want := session.ActiveToolNames(), []string{"grep", "dynamic_tool", "read"}; !slices.Equal(got, want) {
		t.Fatalf("--tools [grep dynamic_tool missing read grep] activated %v, want %v", got, want)
	}
}

// Pi's reload rebuilds the tool registry with the active tools first, in their order: agent-session.ts:3666-3669 passes
// [...getActiveToolNames(), ...addedDefaultTools] as activeToolNames, and _refreshToolRegistry (3552-3562, 3578) puts
// them before the registered tools the allowlist names and keeps each once at its first position. The provider request
// cannot show this order after a reload, because the transcript's initial tool declaration fixes it, so the Session's
// active names are the observable that pins it.
func TestReloadKeepsTheActiveToolOrder(t *testing.T) {
	tools := []string{"grep", "dynamic_tool", "read"}
	allowed := map[string]struct{}{}
	for _, name := range tools {
		allowed[name] = struct{}{}
	}
	session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: allowed, InitialActiveToolNames: tools}, []extension.ToolDefinition{dynamicRegistryTool()}, nil)
	if err := session.RefreshToolsAfterReload(); err != nil {
		t.Fatal(err)
	}
	if got := session.ActiveToolNames(); !slices.Equal(got, tools) {
		t.Fatalf("after the reload rebuild the active tools are %v, want %v", got, tools)
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

// .upstream/v1.0.4/packages/coding-agent/test/suite/regressions/5109-exclude-tools.test.ts:81 ("matches allowlist and
// denylist patterns"): patterns that match a tool activate it like its name, and the denylist patterns win.
func TestExcludeToolsPatternsExtensionPort(t *testing.T) {
	allowed := map[string]struct{}{"*_tool": {}, "ask_*": {}, "re*": {}}
	session := newRegistryPortSession(t, nil, SessionOptions{AllowedTools: allowed, ExcludedTools: map[string]struct{}{"ask*": {}}}, nil, func(_ *Session, registered map[string]extension.RegisteredTool) {
		registerPortTool(registered, registryTool("ask_question", "Ask Question", "Ask a question", "Ask a question"))
		registerPortTool(registered, dynamicRegistryTool())
	})
	bindRegistryPort(t, session)
	if all := allRegistryNames(session); !slices.Equal(all, []string{"dynamic_tool", "read"}) {
		t.Fatalf("all = %q", all)
	}
	active := session.ActiveToolNames()
	slices.Sort(active)
	if !slices.Equal(active, []string{"dynamic_tool", "read"}) {
		t.Fatalf("active = %q", active)
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
				return tools.CreateLocalBashOperations(nil).Exec(ctx, command, cwd, opts)
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
		model := &ai.Model{ID: "claude-sonnet-4-5", Provider: registryPortProvider{}, Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh}}
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
	// .upstream/v0.99.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:99
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
		// .upstream/v0.99.1/packages/coding-agent/test/agent-session-dynamic-tools.test.ts:156-161 (path "builtin:read"; 0.87.1 had "<builtin:read>")
		if !reflect.DeepEqual(read.SourceInfo, icodingagent.PiSourceInfo{Path: "builtin:read", Source: "builtin", Scope: "temporary", Origin: "top-level"}) {
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
