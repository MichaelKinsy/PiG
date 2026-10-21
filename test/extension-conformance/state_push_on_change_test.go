package extensionconformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's extension context reads the live Session at every call: `ctx.model` is `getModel()` (runner.ts:906), `pi.getSettings()` reads the settings manager (loader.ts:411-414), and `pi.getSessionName()` and `pi.getThinkingLevel()` read the Session (agent-session.ts). `pi.getMcpServers()` reads the runtime's registry (loader.ts:495-497) and `ctx.ui.theme` is the global theme (interactive-mode.ts:2665). The SDKs and the Node runtime replicate that state, so a change the host makes with no extension call and no event to the extension, a user's model switch, a settings edit, the /name command, a theme selection, an MCP server another extension registers, reaches an extension that reads from a timer, a thread or a goroutine between requests only if the host pushes it when it changes.
// The host state is a real Session over a faux provider, bound to the extension host as the binary binds it (Session.ObserveReplicatedState to UIBridge.HostStateChanged). The Session has no extension runner, so no event is emitted to the extension: nothing but the host's own push can bring the new value to the reader. Each fixture's state-push-probe command arms a reader on one accessor, the test then changes the Session, and the reader reports the first value that differs from the one it armed on, or the value it still holds after five seconds. The values are not any SDK's default: an SDK that never reads the replicated state fails as one that reads a stale copy does.
func TestHostStateChangeReachesABackgroundReaderAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(allHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("state push conformance complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no replicated state; there is no runtime to push it to")
			}
			rig := newStatePushRig(t, h)
			for _, kind := range rig.kinds(statePushLanguage(tc.name)) {
				t.Run(kind.name, func(t *testing.T) {
					rig.run(t, h, kind)
				})
			}
		})
	}
}

// statePushLanguage is the SDK a harness case loads.
func statePushLanguage(caseName string) string {
	for _, language := range []string{"node", "rust", "python", "go"} {
		if strings.Contains(caseName, language) {
			return language
		}
	}
	return ""
}

// statePushKind is one replicated accessor: the argument of the fixtures' state-push-probe command, what the host does to the Session to change it, and the values the reader armed on and reads after the change.
type statePushKind struct {
	name string
	// setup puts the host in the state the reader arms on.
	setup func(t *testing.T)
	// change is the host's own change: no extension call and no event.
	change func(t *testing.T)
	// armed and read are the values the reader reports before and after the change.
	armed, read func(string) bool
}

type statePushRig struct {
	session      *coding.Session
	services     *coding.AgentSessionServices
	runtime      *extension.ExtensionRuntime
	small, large *ai.Model
}

// newStatePushRig builds the Session the harness's host state comes from and binds the host's replicated accessors and change reports to it.
func newStatePushRig(t *testing.T, h *harness) *statePushRig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	configureFauxAuth(t, services)
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{
		{ID: "state-push-small", Name: "State Push Small", Reasoning: true},
		{ID: "state-push-large", Name: "State Push Large", Reasoning: true},
	}})
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	small, large := runtime.GetModel("faux", "state-push-small"), runtime.GetModel("faux", "state-push-large")
	if small == nil || large == nil {
		t.Fatalf("faux models %v %v", small, large)
	}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: small, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	session.DiscardEvents()
	t.Cleanup(func() { _ = session.Close() })

	// The host's accessors read the Session, as the binary's do (internal/codingagent/interactive_extensions.go, coding/cli).
	codingagent.WireModelOperations(h.bridge, codingagent.ModelOperationBindings{CurrentModel: session.Model})
	h.bridge.SetHostAction("getSessionName", session.SessionName)
	h.bridge.SetHostAction("getSettingsJSON", services.SettingsManager().ExtensionSettingsJSON)
	h.bridge.SetHostAction("getThinkingLevel", func() string { return string(session.Agent().ThinkingLevel()) })
	// The report the binary's modes install (coding/cli watchHostState, internal/codingagent watchReplicatedState).
	stop := session.ObserveReplicatedState(h.bridge.HostStateChanged)
	t.Cleanup(stop)
	// The theme the binary hands its extensions (coding/cli/extensions.go), and the report of its changes.
	h.bridge.SetThemeFunc(codingagent.ActiveExtensionThemeWire)
	stopTheme := tui.ObserveActiveTheme(h.bridge.HostStateChanged)
	t.Cleanup(stopTheme)
	return &statePushRig{session: session, services: services, runtime: h.host.Runtime(), small: small, large: large}
}

// kinds are the accessors language's SDK replicates. Every SDK replicates the session name; the Go, Python and Rust thinking level is a host call, which is live, and only the Node runtime answers it from the replicated state.
func (r *statePushRig) kinds(language string) []statePushKind {
	identity := func(model *ai.Model) func(string) bool {
		return func(got string) bool { return got == model.ID || got == model.DisplayName }
	}
	equal := func(want string) func(string) bool { return func(got string) bool { return got == want } }
	model := statePushKind{
		name:   "model",
		change: func(t *testing.T) { mustSucceed(t, r.session.SetModel(r.large)) },
		armed:  identity(r.small), read: identity(r.large),
	}
	settings := statePushKind{
		name:   "settings:editorPaddingX",
		change: func(t *testing.T) { mustSucceed(t, r.services.SettingsManager().SetEditorPaddingX(2)) },
		armed:  equal("null"), read: equal("2"),
	}
	sessionName := statePushKind{
		name:   "session-name",
		change: func(t *testing.T) { mustSucceed(t, r.session.SetSessionName("pushed-name")) },
		armed:  equal(""), read: equal("pushed-name"),
	}
	thinking := statePushKind{
		name:   "thinking-level",
		setup:  func(t *testing.T) { mustSucceed(t, r.session.SetThinkingLevel(ai.ThinkingHigh)) },
		change: func(t *testing.T) { mustSucceed(t, r.session.SetThinkingLevel(ai.ThinkingLow)) },
		armed:  equal("high"), read: equal("low"),
	}
	// A server another extension, or a native one, registers in the runtime's registry: this extension made no call and handles no mcp_servers_change.
	const mcpServer = "state-push-mcp"
	mcp := statePushKind{
		name: "mcp",
		change: func(t *testing.T) {
			config := json.RawMessage(`{"type":"http","url":"https://mcp.example.com/state-push"}`)
			mustSucceed(t, r.runtime.RegisterMcpServer("state-push-other-extension", mcpServer, config))
			t.Cleanup(func() { r.runtime.UnregisterMcpServer("state-push-other-extension", mcpServer) })
		},
		armed: equal(""), read: equal(mcpServer),
	}
	// The user selects another theme, as /settings does; the process-wide theme is restored afterwards.
	theme := statePushKind{
		name: "theme",
		setup: func(t *testing.T) {
			previous := tui.ActiveTheme()
			t.Cleanup(func() { tui.SetThemeInstance(previous) })
			tui.SetTheme("dark")
		},
		change: func(*testing.T) { tui.SetTheme("light") },
		armed:  equal("dark"), read: equal("light"),
	}
	if language == "node" {
		return []statePushKind{model, settings, sessionName, thinking, mcp, theme}
	}
	return []statePushKind{model, settings, sessionName, mcp, theme}
}

func mustSucceed(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// run arms the reader, changes the host, and checks what the reader saw.
func (r *statePushRig) run(t *testing.T, h *harness, kind statePushKind) {
	t.Helper()
	if kind.setup != nil {
		kind.setup(t)
	}
	command, ok := findCommand(h.runner, "state-push-probe")
	if !ok {
		t.Fatal("state-push-probe command missing")
	}
	dir := t.TempDir()
	args, err := json.Marshal(map[string]string{"kind": kind.name, "dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	// The command returns once the reader is armed: the state the extension holds now is what the host pushed before it dispatched the command.
	if err := command.Handler(h.runner.DispatchContext(t.Context()), string(args)); err != nil {
		t.Fatal(err)
	}
	kind.change(t)
	report := filepath.Join(dir, "state-push-"+strings.ReplaceAll(kind.name, ":", "_"))
	pollUntilConformance(t, "the reader never reported", func() bool {
		_, err := os.Stat(report)
		return err == nil
	})
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	armed, read, ok := strings.Cut(string(body), "=>")
	if !ok {
		t.Fatalf("report %q is not <armed>=><read>", body)
	}
	if !kind.armed(armed) {
		t.Errorf("the reader armed on %q, which is not the state the host held before the change", armed)
	}
	if !kind.read(read) {
		t.Errorf("after the host changed its own state with no call and no event to the extension, a reader between requests still reads %q (armed on %q): the host never pushed the change", read, armed)
	}
}
