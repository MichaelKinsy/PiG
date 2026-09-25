package codingagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// /reload recompiles out-of-tree subprocess extensions, and it is the command
// reached for after rebuilding pig itself: exactly when the staged SDK is
// older than the running binary. Rebuilding against that stale copy reproduces
// what the startup stage exists to prevent: an extension carrying a defect the
// SDK already fixed, with nothing in the session to explain it.
//
// The order is the contract. Staging after the rebuild would be no better than
// not staging at all for that reload.
func TestReloadStagesTheSDKBeforeRebuildingExtensions(t *testing.T) {
	var order []string
	host := &orderRecordingHost{onReload: func() { order = append(order, "rebuild") }}

	m := reloadTestMode(InteractiveOptions{
		SubprocessHost:     host,
		StageExtensionSDKs: func() error { order = append(order, "stage"); return nil },
	})
	m.buildSlashContext(context.Background()).Reload()

	if len(order) != 2 || order[0] != "stage" || order[1] != "rebuild" {
		t.Errorf("reload ran %v; want [stage rebuild]. Staging after the rebuild "+
			"leaves that reload compiling against the stale SDK.", order)
	}
}

// A staging failure must not abort the reload: the user asked to pick up their
// changes, and the rebuild is still worth attempting. It has to be visible
// though, because its symptom is otherwise untraceable from the session.
func TestReloadContinuesAndReportsWhenStagingFails(t *testing.T) {
	rebuilt := false
	host := &orderRecordingHost{onReload: func() { rebuilt = true }}

	m := reloadTestMode(InteractiveOptions{
		SubprocessHost:     host,
		StageExtensionSDKs: func() error { return errors.New("disk full") },
	})
	m.buildSlashContext(context.Background()).Reload()

	if !rebuilt {
		t.Error("a staging failure aborted the reload; the user's other changes were dropped")
	}
	var reported bool
	for _, issue := range m.reloadIssues {
		if strings.Contains(issue, "disk full") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the staging failure was swallowed; reloadIssues = %v", m.reloadIssues)
	}
}

// Reload must not require the hook: headless and test paths leave it nil.
func TestReloadWithoutAStagingHookStillRebuilds(t *testing.T) {
	rebuilt := false
	host := &orderRecordingHost{onReload: func() { rebuilt = true }}

	m := reloadTestMode(InteractiveOptions{SubprocessHost: host})
	m.buildSlashContext(context.Background()).Reload()

	if !rebuilt {
		t.Error("reload skipped the rebuild when no staging hook was configured")
	}
}

// reloadTestMode builds the minimum InteractiveMode the Reload closure touches.
// The editor is real because the closure rebuilds autocomplete on it, which the
// production path always has by construction.
func reloadTestMode(opts InteractiveOptions) *InteractiveMode {
	m := &InteractiveMode{runCtx: context.Background(), opts: opts}
	m.editor = tui.NewEditor()
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 80, 24)
	m.agent = agent.NewAgent(agent.AgentOptions{})
	return m
}

// orderRecordingHost is a SubprocessHost that records when its rebuild ran.
type orderRecordingHost struct {
	onReload   func()
	err        error
	extensions []extension.Extension
}

func (h *orderRecordingHost) Reload(context.Context) ([]extension.Extension, error) {
	if h.onReload != nil {
		h.onReload()
	}
	return append([]extension.Extension(nil), h.extensions...), h.err
}
func (h *orderRecordingHost) ExtensionCount() int                        { return 0 }
func (h *orderRecordingHost) LastReloadReport() *subprocess.ReloadReport { return nil }
func (h *orderRecordingHost) LoadErrors() []string                       { return nil }
func (h *orderRecordingHost) SetWidthFunc(func() int)                    {}
func (h *orderRecordingHost) SetHeightFunc(func() int)                   {}
func (h *orderRecordingHost) NotifyWidth(int)                            {}
func (h *orderRecordingHost) NotifyHeight(int)                           {}
func (h *orderRecordingHost) SetCrashHandler(func(string, time.Duration, bool, string)) {
}
func (h *orderRecordingHost) IsShuttingDown() bool { return false }

// reportingHost is an orderRecordingHost whose last reload report is fixed.
type reportingHost struct {
	orderRecordingHost
	report *subprocess.ReloadReport
}

func (h *reportingHost) LastReloadReport() *subprocess.ReloadReport { return h.report }

// Upstream showLoadedResources lists every extension load error under
// [Extension issues] after /reload; reload itself does not fail.
func TestReloadDiagnosticsListUnresolvedExtensions(t *testing.T) {
	issue := "/pkg/extensions/bad: Failed to load extension: no factory"
	host := &reportingHost{report: &subprocess.ReloadReport{Issues: []string{issue}}}
	m := reloadTestMode(InteractiveOptions{SubprocessHost: host})
	slash := m.buildSlashContext(context.Background())
	slash.Reload()
	diag := slash.ReloadDiagnostics()
	count := 0
	for _, line := range diag.Diagnostics {
		if line == "[extension] "+issue {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("diagnostics = %q, want the unresolved extension listed once", diag.Diagnostics)
	}
}

// Upstream's /reload status is exactly "Reloaded keybindings, extensions,
// skills, prompts, themes, and context files": it prints no resource counts,
// so it never shows an unpluralized "(1 extensions)".
func TestReloadSummaryMatchesUpstreamStatus(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.Reload = func() {}
	sc.ReloadDiagnostics = func() ReloadDiag { return ReloadDiag{Extensions: 1, Themes: 2, Skills: 3, Prompts: 1, ContextFiles: 1} }
	if err := reloadHandler(sc); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Reloaded keybindings, extensions, skills, prompts, themes, and context files\n" {
		t.Fatalf("reload summary = %q", got)
	}
}

// Upstream rebuilds the chat from persisted Session entries before it emits
// session_start and the completion status. Repeating /reload must therefore
// install a fresh status component instead of coalescing the acknowledgement
// into the prior reload's component, which produces no new terminal evidence.
func TestRepeatedReloadRebuildsChatBeforeCompletionStatus(t *testing.T) {
	m := reloadTestMode(InteractiveOptions{})

	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	firstStatus := m.lastStatusText
	if firstStatus == nil {
		t.Fatal("first reload did not install its completion status")
	}

	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	if m.lastStatusText == firstStatus {
		t.Fatal("second reload coalesced into the first completion status; want chat rebuilt before status")
	}
}

// Pi 0.87.1 shows [Prompt conflicts] after /reload (showLoadedResources with
// showDiagnosticsWhenQuiet, interactive-mode.ts:1864-1871, 6232), outside the
// chat that restoreChatBeforeSessionStart rebuilds. The block must survive the
// reload's chat rebuild.
func TestReloadKeepsPromptConflictsAfterChatRebuild(t *testing.T) {
	m := reloadTestMode(InteractiveOptions{})
	path := filepath.Join(t.TempDir(), "broken.md")
	if err := os.WriteFile(path, []byte("---\ndescription: [unterminated\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.PromptPaths = []string{path}
	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	out := stripANSITest(strings.Join(m.chatContainer.Render(300), "\n"))
	if got := strings.Count(out, "[Prompt conflicts]"); got != 1 {
		t.Fatalf("[Prompt conflicts] shown %d times after /reload, want 1:\n%s", got, out)
	}
}

// A prompt contributed through resources_discover joins the one post-reload
// diagnostics block rather than printing a second [Prompt conflicts] block.
func TestReloadShowsExtensionPromptConflictsOnce(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "local.md")
	dynamic := filepath.Join(dir, "dynamic.md")
	for _, path := range []string{local, dynamic} {
		if err := os.WriteFile(path, []byte("---\ndescription: [unterminated\n---\nBody"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := reloadTestMode(InteractiveOptions{CWD: dir, PromptPaths: []string{local}})
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Path: filepath.Join(dir, "dynamic-ext.ts"),
		Handlers: map[string][]extension.HandlerFn{
			EventResourcesDiscover: {func(...any) (any, error) {
				return &extension.ResourcesDiscoverResult{PromptPaths: []string{dynamic}}, nil
			}},
		},
	}}, dir)
	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	out := stripANSITest(strings.Join(m.chatContainer.Render(300), "\n"))
	if got := strings.Count(out, "[Prompt conflicts]"); got != 1 {
		t.Fatalf("[Prompt conflicts] shown %d times after /reload, want 1:\n%s", got, out)
	}
	if !strings.Contains(out, local) || !strings.Contains(out, dynamic) {
		t.Fatalf("post-reload diagnostics miss a prompt path:\n%s", out)
	}
}

// Pi re-reads hideThinkingBlock and outputPad from settings just before it
// rebuilds the chat on reload (interactive-mode.ts:6214-6216), so a settings
// file edited outside the session takes effect in the rebuilt transcript.
func TestReloadRebuildsChatWithReloadedDisplaySettings(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	if err := sm.SetHideThinkingBlock(false); err != nil {
		t.Fatal(err)
	}
	if err := sm.SetOutputPad(1); err != nil {
		t.Fatal(err)
	}
	session := NewSession("reload-display", dir)
	reply := &agent.AssistantMessage{
		Role: "assistant", Provider: "test", ModelID: "model", Timestamp: 1,
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "visible-answer-marker"}},
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: reply}); err != nil {
		t.Fatal(err)
	}
	m := reloadTestMode(InteractiveOptions{
		SettingsManager: sm,
		Settings:        sm.Get(),
		SessionHandle:   &recordingCompactHandle{inner: session},
	})
	m.hideThinking = false
	m.outputPad = 1

	// Another process edits the global settings file.
	external := NewSettingsManager(dir, dir)
	if err := external.SetHideThinkingBlock(true); err != nil {
		t.Fatal(err)
	}
	if err := external.SetOutputPad(0); err != nil {
		t.Fatal(err)
	}

	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	if !m.hideThinking || m.outputPad != 0 {
		t.Fatalf("hideThinking=%v outputPad=%d after reload, want true and 0", m.hideThinking, m.outputPad)
	}
	if len(m.assistantBlocks) != 1 {
		t.Fatalf("rebuilt assistant blocks = %d, want 1", len(m.assistantBlocks))
	}
	block := m.assistantBlocks[0]
	lines := strings.Split(stripANSITest(strings.Join(block.Render(100), "\n")), "\n")
	if !slices.Contains(lines, "visible-answer-marker") {
		t.Fatalf("rebuilt block kept the pre-reload output padding:\n%s", strings.Join(lines, "\n"))
	}
	block.SetThinkingDelta("private-plan-marker")
	out := stripANSITest(strings.Join(block.Render(100), "\n"))
	if strings.Contains(out, "private-plan-marker") || !strings.Contains(out, "Thinking...") {
		t.Fatalf("rebuilt block ignores the reloaded hideThinkingBlock=true:\n%s", out)
	}
}

// Pi persists the Ctrl+T thinking visibility toggle (interactive-mode.ts:4431),
// so the reload that re-reads hideThinkingBlock keeps the toggled value.
func TestThinkingToggleSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	sm := NewSettingsManager(dir, dir)
	m := reloadTestMode(InteractiveOptions{SettingsManager: sm, Settings: sm.Get()})
	m.hideThinking = sm.Get().HideThinkingBlock

	m.toggleThinkingVisibility()
	want := m.hideThinking
	if err := reloadHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	if m.hideThinking != want {
		t.Fatalf("hideThinking = %v after reload, want toggled value %v", m.hideThinking, want)
	}
}

// One extension that fails on /reload is listed once under Extension issues,
// not once as a failed reload and again as its raw error.
func TestReloadListsAFailingExtensionOnce(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	// subprocess.NewHost falls back to the process's real $HOME/.pig for its
	// build cache and SDK transaction lock when PIG_HOME is unset. Pin it to
	// an ephemeral dir so this test never writes under the operator's real
	// ~/.pig/cache.
	t.Setenv("PIG_HOME", filepath.Join(t.TempDir(), "pig-home"))
	broken := filepath.Join(t.TempDir(), "broken.mjs")
	if err := os.WriteFile(broken, []byte("export default function () { throw new Error(\"register boom\"); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
		return []subprocess.ExtConfig{{Name: "broken", Source: broken, Enabled: true}}, nil
	})
	m := reloadTestMode(InteractiveOptions{SubprocessHost: host})
	slash := m.buildSlashContext(context.Background())
	slash.Reload()
	diag := slash.ReloadDiagnostics()
	count := 0
	for _, line := range diag.Diagnostics {
		if strings.Contains(line, broken) || strings.Contains(line, "register boom") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("diagnostics = %q, want the failing extension listed once", diag.Diagnostics)
	}
}

func TestDetectExtensionConflictsMatchesUpstream(t *testing.T) {
	tool := func(name string) map[string]extension.RegisteredTool {
		return map[string]extension.RegisteredTool{name: {}}
	}
	flag := func(name string) map[string]extension.ExtensionFlag {
		return map[string]extension.ExtensionFlag{name: {}}
	}
	conflicts := DetectExtensionConflicts([]extension.Extension{
		{Name: "a", Path: "/a.ts", Tools: tool("shared"), Flags: flag("mode")},
		{Name: "b", Path: "/b.ts", Tools: tool("shared"), Flags: flag("mode")},
		{Name: "c", Path: "/c.ts", Tools: tool("own")},
	})
	want := []ExtensionConflict{
		{Path: "/b.ts", Message: `Tool "shared" conflicts with /a.ts`},
		{Path: "/b.ts", Message: `Flag "--mode" conflicts with /a.ts`},
	}
	if !slices.Equal(conflicts, want) {
		t.Fatalf("conflicts = %#v, want %#v", conflicts, want)
	}
}

func TestReloadIncludesBuiltinExtensionsInLoadOrderAndConflicts(t *testing.T) {
	tool := func(name string) map[string]extension.RegisteredTool {
		return map[string]extension.RegisteredTool{name: {}}
	}
	flag := func(name string) map[string]extension.ExtensionFlag {
		return map[string]extension.ExtensionFlag{name: {}}
	}
	host := &orderRecordingHost{extensions: []extension.Extension{{
		Name: "subprocess", Path: "/configured.ts", Tools: tool("shared"), Flags: flag("mode"),
	}}}
	builtin := extension.Extension{
		Name: "piglet", Path: "builtin:piglet", Tools: tool("shared"), Flags: flag("mode"),
	}
	m := reloadTestMode(InteractiveOptions{
		CWD:               t.TempDir(),
		SubprocessHost:    host,
		BuiltinExtensions: []extension.Extension{builtin},
	})
	m.buildSlashContext(context.Background()).Reload()

	wantIssues := []string{
		`[extension] builtin:piglet: Tool "shared" conflicts with /configured.ts`,
		`[extension] builtin:piglet: Flag "--mode" conflicts with /configured.ts`,
	}
	for _, want := range wantIssues {
		if !slices.Contains(m.reloadIssues, want) {
			t.Errorf("reload issues = %q, want %q", m.reloadIssues, want)
		}
	}
	if got := m.newRunner.ExtensionNames(); !slices.Equal(got, []string{"subprocess", "piglet"}) {
		t.Fatalf("runner extension order = %v, want configured then builtin", got)
	}
}

func TestReloadReconstructsBuiltinFactories(t *testing.T) {
	for _, withHost := range []bool{false, true} {
		t.Run(fmt.Sprintf("subprocess-host-%t", withHost), func(t *testing.T) {
			generation := 0
			var host SubprocessHost
			if withHost {
				host = &orderRecordingHost{}
			}
			m := reloadTestMode(InteractiveOptions{
				CWD:               t.TempDir(),
				SubprocessHost:    host,
				BuiltinExtensions: []extension.Extension{{Name: "builtin-0"}},
				ReloadBuiltinExtensions: func() []extension.Extension {
					generation++
					return []extension.Extension{{Name: fmt.Sprintf("builtin-%d", generation)}}
				},
			})
			slash := m.buildSlashContext(context.Background())
			for want := 1; want <= 2; want++ {
				slash.Reload()
				if generation != want {
					t.Fatalf("reload %d factory calls = %d", want, generation)
				}
				if got := m.newRunner.ExtensionNames(); !slices.Equal(got, []string{fmt.Sprintf("builtin-%d", want)}) {
					t.Fatalf("reload %d builtins = %v", want, got)
				}
			}
		})
	}
}

// Upstream lists tool and flag conflicts under [Extension issues] after
// /reload; both extensions stay loaded.
func TestReloadListsExtensionToolConflicts(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the extension fixture: %v", err)
	}
	// See the comment in TestReloadListsAFailingExtensionOnce: pin PIG_HOME so
	// subprocess.NewHost never falls back to the operator's real ~/.pig/cache.
	t.Setenv("PIG_HOME", filepath.Join(t.TempDir(), "pig-home"))
	dir := t.TempDir()
	source := "export default function (pi) { pi.registerTool({ name: \"ask_user\", label: \"Ask\", description: \"ask\", parameters: { type: \"object\", properties: {} }, execute: async () => ({ content: [{ type: \"text\", text: \"ok\" }] }) }); }\n"
	var paths []string
	for _, name := range []string{"one", "two"} {
		path := filepath.Join(dir, name, "ask.mjs")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
		return []subprocess.ExtConfig{{Name: "ask", Source: paths[0], Enabled: true}, {Name: "ask", Source: paths[1], Enabled: true}}, nil
	})
	m := reloadTestMode(InteractiveOptions{SubprocessHost: host})
	slash := m.buildSlashContext(context.Background())
	slash.Reload()
	want := "[extension] " + paths[1] + `: Tool "ask_user" conflicts with ` + paths[0]
	if diag := slash.ReloadDiagnostics(); !slices.Contains(diag.Diagnostics, want) {
		t.Fatalf("diagnostics = %q, want %q", diag.Diagnostics, want)
	}
	if host.ExtensionCount() != 2 {
		t.Fatalf("loaded extensions = %d, want both copies", host.ExtensionCount())
	}
}
