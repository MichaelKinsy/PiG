package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func servicesWithDefaultTools(t *testing.T, defaultTools []string) *AgentSessionServices {
	t.Helper()
	cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if defaultTools != nil {
		raw, err := json.Marshal(map[string]any{"defaultTools": defaultTools})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

func sessionFromServices(t *testing.T, defaultTools []string, options CreateAgentSessionFromServicesOptions) (*Session, error) {
	t.Helper()
	options.Services = servicesWithDefaultTools(t, defaultTools)
	if options.Model == nil {
		options.Model = fakeModel()
	}
	result, err := CreateAgentSessionFromServices(options)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	return result.Session, nil
}

func sortedToolNames(session *Session) []string {
	var names []string
	for _, tool := range session.GetAllTools() {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// default-tools-setting.test.ts "applies through service-based session creation": the defaultTools setting reaches the session created from services.
func TestCreateAgentSessionFromServicesAppliesTheDefaultToolsSetting(t *testing.T) {
	session, err := sessionFromServices(t, []string{"ls"}, CreateAgentSessionFromServicesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.ActiveToolNames(); !slices.Equal(got, []string{"ls"}) {
		t.Fatalf("active tools = %v, want [ls]", got)
	}
	all := sortedToolNames(session)
	for _, name := range []string{"bash", "edit", "find", "grep", "ls", "read", "write"} {
		if !slices.Contains(all, name) {
			t.Errorf("tool %s is not registered: %v", name, all)
		}
	}
}

// default-tools-setting.test.ts "preserves explicit tool option precedence".
func TestCreateAgentSessionFromServicesToolOptionPrecedence(t *testing.T) {
	allowlisted, err := sessionFromServices(t, []string{"grep"}, CreateAgentSessionFromServicesOptions{Tools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := allowlisted.ActiveToolNames(); !slices.Equal(got, []string{"read"}) {
		t.Errorf("allowlist: active tools = %v, want [read]", got)
	}
	excluded, err := sessionFromServices(t, []string{"read", "grep"}, CreateAgentSessionFromServicesOptions{ExcludeTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := excluded.ActiveToolNames(); !slices.Equal(got, []string{"grep"}) {
		t.Errorf("denylist: active tools = %v, want [grep]", got)
	}
	toolLess, err := sessionFromServices(t, []string{"read"}, CreateAgentSessionFromServicesOptions{NoTools: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if all, active := toolLess.GetAllTools(), toolLess.ActiveToolNames(); len(all) != 0 || len(active) != 0 {
		t.Errorf("noTools all: tools %v, active %v, want none", all, active)
	}
}

// default-tools-setting.test.ts:159-202 "applies +name and -name tool options to the default selection": one tool registered inactive and one active,
// both supplied as SDK custom tools (the Go counterpart of Pi's inline extension).
func TestCreateAgentSessionFromServicesAppliesToolModifiers(t *testing.T) {
	tools := func() []extension.ToolDefinition {
		inactive := registryTool("inactive_tool", "Inactive Tool", "Extension tool registered inactive", "")
		defaultActive := false
		inactive.DefaultActive = &defaultActive
		return []extension.ToolDefinition{inactive, registryTool("active_tool", "Active Tool", "Extension tool registered active", "")}
	}
	session, err := sessionFromServices(t, []string{"+grep"}, CreateAgentSessionFromServicesOptions{Tools: []string{"+inactive_tool", "-write"}, CustomTools: tools()})
	if err != nil {
		t.Fatal(err)
	}
	got := session.ActiveToolNames()
	slices.Sort(got)
	if want := []string{"active_tool", "bash", "edit", "grep", "inactive_tool", "read"}; !slices.Equal(got, want) {
		t.Errorf("active tools = %v, want %v", got, want)
	}
	toolLess, err := sessionFromServices(t, []string{"read"}, CreateAgentSessionFromServicesOptions{NoTools: "all", Tools: []string{"+inactive_tool"}, CustomTools: tools()})
	if err != nil {
		t.Fatal(err)
	}
	if got := toolLess.ActiveToolNames(); !slices.Equal(got, []string{"inactive_tool"}) {
		t.Errorf("noTools all with +inactive_tool: active tools = %v, want [inactive_tool]", got)
	}
}

// default-tools-setting.test.ts "rejects invalid tool modifier options".
func TestCreateAgentSessionFromServicesRejectsInvalidToolOptions(t *testing.T) {
	for _, tc := range []struct {
		tools []string
		want  string
	}{
		{[]string{"read", "+grep"}, "Invalid tools option: tool names cannot be mixed with +name or -name entries"},
		{[]string{"-gr*"}, "Invalid tools option: +name and -name entries take exact tool names, not patterns: -gr*"},
	} {
		if _, err := sessionFromServices(t, nil, CreateAgentSessionFromServicesOptions{Tools: tc.tools}); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("tools %v: error %v, want %q", tc.tools, err, tc.want)
		}
	}
}

// sdk.ts:190-232: without a model the initial model is selected from the models that have auth, and with none the no-models message is returned.
func TestCreateAgentSessionFromServicesSelectsTheInitialModelOrReportsNone(t *testing.T) {
	services := servicesWithDefaultTools(t, nil)
	available := services.ModelRuntime().GetAvailableSnapshot()
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if len(available) == 0 {
		if result.ModelFallbackMessage != icodingagent.FormatNoModelsAvailableMessage() {
			t.Fatalf("no model has auth, fallback message %q", result.ModelFallbackMessage)
		}
		return
	}
	if result.ModelFallbackMessage != "" || result.Session.Model() == nil {
		t.Fatalf("models with auth exist: fallback %q, model %v", result.ModelFallbackMessage, result.Session.Model())
	}
	if !slices.ContainsFunc(available, func(m *ai.Model) bool { return m.ID == result.Session.Model().ID }) {
		t.Fatalf("selected %s is not an available model", result.Session.Model().ID)
	}
}

// sdk.ts:259-276: allowedToolNames is the `tools` list, else an empty allowlist for noTools "all", else no restriction; with +name/-name entries it is the
// modified selection only under noTools "all".
func TestResolveSDKToolSelectionAllowlist(t *testing.T) {
	settings := servicesWithDefaultTools(t, []string{"read", "bash"}).SettingsManager()
	if got := resolveSDKToolSelection(settings, nil, nil, "all"); got.allowed == nil || len(got.allowed) != 0 || len(got.initialActive) != 0 {
		t.Errorf("noTools all: allowed %v, active %v, want an empty allowlist and no active tools", got.allowed, got.initialActive)
	}
	if got := resolveSDKToolSelection(settings, nil, nil, "builtin"); got.allowed != nil || len(got.initialActive) != 0 {
		t.Errorf("noTools builtin: allowed %v, active %v, want no allowlist and no default tools", got.allowed, got.initialActive)
	}
	if got := resolveSDKToolSelection(settings, nil, nil, ""); got.allowed != nil || !slices.Equal(got.initialActive, []string{"read", "bash"}) {
		t.Errorf("defaults: allowed %v, active %v", got.allowed, got.initialActive)
	}
	if got := resolveSDKToolSelection(settings, []string{"+grep"}, nil, ""); got.allowed != nil || !slices.Equal(got.initialActive, []string{"read", "bash", "grep"}) || !slices.Equal(got.modifiers, []string{"+grep"}) {
		t.Errorf("modifiers: %+v", got)
	}
}

type promptLoader struct {
	*staticResourceLoader
	prompt string
}

func (l promptLoader) GetSystemPrompt() (string, bool) { return l.prompt, true }

// sdk.ts createAgentSession (Pi 1.1.0): the options name the cwd, the agent directory and the loader; the services are built from them, the
// defaultTools setting under that agent directory selects the tools (default-tools-setting.test.ts), and the resourceLoader option is the one the
// session builds its system prompt from.
func TestCreateAgentSessionBuildsItsServicesFromTheOptions(t *testing.T) {
	cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultTools":["ls"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD: cwd, AgentDir: agentDir, Model: fakeModel(),
		ResourceLoader: promptLoader{staticResourceLoader: &staticResourceLoader{}, prompt: "prompt from the resourceLoader option"},
	})
	if err != nil {
		t.Fatal(err)
	}
	session := result.Session
	if got := session.ActiveToolNames(); !slices.Equal(got, []string{"ls"}) {
		t.Errorf("active tools = %v, want [ls] from the agent directory's defaultTools", got)
	}
	if got := session.SystemPrompt(); !strings.Contains(got, "prompt from the resourceLoader option") {
		t.Errorf("system prompt = %q, want the resourceLoader option's prompt", got)
	}
	if session.ownedServices == nil || session.ownedServices.CWD() != cwd {
		t.Fatalf("the session owns services for cwd %q, got %+v", cwd, session.ownedServices)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

// A model runtime the caller supplies is the services' runtime and stays the caller's: the session built over it shares it.
func TestCreateAgentSessionSharesACallerModelRuntime(t *testing.T) {
	shared := servicesWithDefaultTools(t, nil)
	runtime := shared.ModelRuntime()
	result, err := CreateAgentSession(CreateAgentSessionOptions{CWD: shared.CWD(), AgentDir: shared.AgentDir(), ModelRuntime: runtime, Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Session.Close() }()
	if result.Session.ownedServices.ModelRuntime() != runtime {
		t.Fatal("the session built its own model runtime instead of the one supplied")
	}
}

type extensionsLoader struct {
	*staticResourceLoader
	loaded LoadExtensionsResult
}

func (l extensionsLoader) GetExtensions() LoadExtensionsResult { return l.loaded }

// sdk.ts:480-486: createAgentSession returns resourceLoader.getExtensions() as its result's extensionsResult.
func TestCreateAgentSessionReturnsTheLoadersExtensionsResult(t *testing.T) {
	services := servicesWithDefaultTools(t, nil)
	loaded := LoadExtensionsResult{Errors: []ExtensionLoadError{{Path: "broken.ts", Error: "boom"}}, Warnings: []ExtensionLoadWarning{{Path: "old.ts", Warning: "deprecated"}}}
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD: services.CWD(), AgentDir: services.AgentDir(), ModelRuntime: services.ModelRuntime(), Model: fakeModel(),
		ResourceLoader: extensionsLoader{staticResourceLoader: &staticResourceLoader{}, loaded: loaded},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	got := result.ExtensionsResult
	if len(got.Errors) != 1 || got.Errors[0] != loaded.Errors[0] || len(got.Warnings) != 1 || got.Warnings[0] != loaded.Warnings[0] {
		t.Fatalf("extensionsResult = %+v, want the resource loader's %+v", got, loaded)
	}
}

// agent-session-services.ts:214-233: createAgentSessionFromServices forwards sessionManager, thinkingLevel, scopedModels, excludeTools and sessionStartEvent to createAgentSession, which builds the session on the given log (sdk.ts:201), uses the given thinking level before clamping it to the model (sdk.ts:253-276), keeps the scope (agent-session.ts:489), removes the excluded tools (sdk.ts:292-294) and starts the extensions with the given session_start event (agent-session.ts:509; agent-session-runtime-events.test.ts:87-92 passes sessionStartEvent the same way).
func TestCreateAgentSessionFromServicesForwardsTheSessionInputs(t *testing.T) {
	services := servicesWithDefaultTools(t, nil)
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	model := fakeModel()
	model.ProviderMeta.Reasoning = true
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	scoped := []ScopedModel{{Model: model}}
	start := &extension.SessionStartEvent{Type: "session_start", Reason: "resume", PreviousSessionFile: "/sessions/previous.jsonl"}
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{
		Services: services, SessionManager: manager, Model: model, ThinkingLevel: ai.ThinkingLow, ScopedModels: scoped,
		ExcludeTools: []string{"bash"}, SessionStartEvent: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := result.Session
	t.Cleanup(func() { _ = session.Close() })
	if session.SessionManager() != manager {
		t.Error("the session is not built on the given session manager")
	}
	if got := session.ThinkingLevel(); got != ai.ThinkingLow {
		t.Errorf("thinking level = %q, want the given %q", got, ai.ThinkingLow)
	}
	if got := session.ScopedModels(); len(got) != 1 || got[0].Model != model {
		t.Errorf("scoped models = %v, want the given scope", got)
	}
	if got := session.ActiveToolNames(); slices.Contains(got, "bash") || !slices.Contains(got, "read") {
		t.Errorf("active tools = %v, want the defaults without the excluded bash", got)
	}
	if got := session.StartEvent(); got != *start {
		t.Errorf("session_start event = %+v, want the given %+v", got, *start)
	}

	// Without them the session creates its own log, uses the default thinking level, has no scope, keeps bash and starts with reason startup.
	plain, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plain.Session.Close() })
	if plain.Session.SessionManager() == manager || plain.Session.ThinkingLevel() == ai.ThinkingLow || len(plain.Session.ScopedModels()) != 0 ||
		!slices.Contains(plain.Session.ActiveToolNames(), "bash") || plain.Session.StartEvent().Reason != "startup" {
		t.Errorf("the defaults are indistinguishable from the given inputs: thinking %q, scope %v, tools %v, start %+v", plain.Session.ThinkingLevel(),
			plain.Session.ScopedModels(), plain.Session.ActiveToolNames(), plain.Session.StartEvent())
	}
}

// sdk.ts:191-201: createAgentSession builds its services over the given settingsManager and its session on the given sessionManager; sdk.ts:253-276 uses the given thinkingLevel before clamping it; sdk.ts:279-294 selects the tools from tools, excludeTools and noTools; sdk.ts:461-477 passes scopedModels, customTools and sessionStartEvent to the AgentSession.
func TestCreateAgentSessionForwardsTheSessionOptions(t *testing.T) {
	cwd := t.TempDir()
	settings := NewInMemorySettingsManager(Settings{DefaultTools: []string{"find"}})
	manager, err := NewInMemorySessionManager(cwd)
	if err != nil {
		t.Fatal(err)
	}
	model := fakeModel()
	model.ProviderMeta.Reasoning = true
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	scoped := []ScopedModel{{Model: model}}
	start := &extension.SessionStartEvent{Type: "session_start", Reason: "fork", PreviousSessionFile: "/sessions/parent.jsonl"}
	custom := registryTool("custom_tool", "Custom Tool", "An SDK custom tool", "")
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD: cwd, AgentDir: filepath.Join(t.TempDir(), "agent"), SettingsManager: settings, SessionManager: manager, Model: model,
		ThinkingLevel: ai.ThinkingLow, ScopedModels: scoped, Tools: []string{"read", "grep", "custom_tool"}, ExcludeTools: []string{"grep"},
		CustomTools: []extension.ToolDefinition{custom}, SessionStartEvent: start,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := result.Session
	t.Cleanup(func() { _ = session.Close() })
	if session.ownedServices.SettingsManager() != settings {
		t.Error("the services were not built over the given settings manager")
	}
	if session.SessionManager() != manager {
		t.Error("the session is not built on the given session manager")
	}
	if got := session.ThinkingLevel(); got != ai.ThinkingLow {
		t.Errorf("thinking level = %q, want the given %q", got, ai.ThinkingLow)
	}
	if got := session.ScopedModels(); len(got) != 1 || got[0].Model != model {
		t.Errorf("scoped models = %v, want the given scope", got)
	}
	if got := session.ActiveToolNames(); !slices.Equal(got, []string{"read", "custom_tool"}) {
		t.Errorf("active tools = %v, want the tools list without the excluded grep and with the custom tool [read custom_tool]", got)
	}
	if got := session.StartEvent(); got != *start {
		t.Errorf("session_start event = %+v, want the given %+v", got, *start)
	}

	// Without a tools list the given settings manager's defaultTools select the tools; noTools "builtin" drops them and keeps the custom tool.
	defaults, err := CreateAgentSession(CreateAgentSessionOptions{CWD: cwd, AgentDir: filepath.Join(t.TempDir(), "agent"), SettingsManager: settings, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = defaults.Session.Close() })
	if got := defaults.Session.ActiveToolNames(); !slices.Equal(got, []string{"find"}) {
		t.Errorf("active tools = %v, want the given settings manager's defaultTools [find]", got)
	}
	if defaults.Session.SessionManager() == manager || defaults.Session.ThinkingLevel() == ai.ThinkingLow || len(defaults.Session.ScopedModels()) != 0 ||
		defaults.Session.StartEvent().Reason != "startup" {
		t.Errorf("the defaults are indistinguishable from the given options: thinking %q, scope %v, start %+v", defaults.Session.ThinkingLevel(),
			defaults.Session.ScopedModels(), defaults.Session.StartEvent())
	}
	builtinLess, err := CreateAgentSession(CreateAgentSessionOptions{CWD: cwd, AgentDir: filepath.Join(t.TempDir(), "agent"), SettingsManager: settings, Model: model,
		NoTools: "builtin", CustomTools: []extension.ToolDefinition{custom}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = builtinLess.Session.Close() })
	if got := builtinLess.Session.ActiveToolNames(); !slices.Equal(got, []string{"custom_tool"}) {
		t.Errorf("noTools builtin: active tools = %v, want only the custom tool", got)
	}
}
