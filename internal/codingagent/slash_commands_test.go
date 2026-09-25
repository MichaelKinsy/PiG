package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func newTestSlashContext() (*SlashContext, *strings.Builder, *bool, *bool) {
	var out strings.Builder
	var cleared, quit bool
	sc := &SlashContext{
		Append:     func(s string) { out.WriteString(s); out.WriteString("\n") },
		AppendText: func(s string) { out.WriteString(s); out.WriteString("\n") },
		Clear:      func() { cleared = true },
		Quit:       func() { quit = true },
		Reset:      func() {},
	}
	return sc, &out, &cleared, &quit
}

func TestParseSlashLine(t *testing.T) {
	cases := []struct {
		in       string
		wantName string
		wantArgs string
	}{
		{"/help", "help", ""},
		{"/help  ", "help", ""},
		{"/agent worker", "agent", "worker"},
		{"  /model  github-copilot/gpt-4o  ", "model", "github-copilot/gpt-4o"},
		{"/save  out.md  more", "save", "out.md  more"},
		{"not-slash", "", ""},
		{"/", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			n, a := parseSlashLine(tc.in)
			if n != tc.wantName || a != tc.wantArgs {
				t.Fatalf("parseSlashLine(%q) = (%q,%q) want (%q,%q)", tc.in, n, a, tc.wantName, tc.wantArgs)
			}
		})
	}
}

func TestSlashRegistryBuiltinsSeeded(t *testing.T) {
	r := NewSlashRegistry()
	want := []string{"quit", "model", "scoped-models", "copy", "session", "hotkeys", "new"}
	for _, n := range want {
		if _, ok := r.Resolve(n); !ok {
			t.Errorf("builtin %q not seeded", n)
		}
	}
	all := BuiltinSlashCommands()
	findDesc := func(name string) string {
		for _, cmd := range all {
			if cmd.Name == name {
				return cmd.Description
			}
		}
		return ""
	}
	if got := findDesc("quit"); got != "Quit "+AppName {
		t.Fatalf("/quit description = %q, want %q", got, "Quit "+AppName)
	}
	if got := findDesc("login"); got != "Configure provider authentication" {
		t.Fatalf("/login description = %q", got)
	}
	if got := findDesc("logout"); got != "Remove stored provider authentication" {
		t.Fatalf("/logout description = %q", got)
	}
}

func TestSlashRegistryResolves(t *testing.T) {
	r := NewSlashRegistry()
	_, ok := r.Resolve("quit")
	if !ok {
		t.Fatal("/quit did not resolve")
	}
	// /exit was removed (no upstream alias). Verify it's unknown.
	_, ok = r.Resolve("exit")
	if ok {
		t.Fatal("/exit should not resolve (removed)")
	}
}

func TestSlashRegistryUnknownCommand(t *testing.T) {
	r := NewSlashRegistry()
	sc, _, _, _ := newTestSlashContext()
	err := r.Dispatch(sc, "/nope", nil)
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !errors.Is(err, ErrUnknownSlashCommand) {
		t.Fatalf("err = %v, want ErrUnknownSlashCommand", err)
	}
}

func TestSlashQuitInvokesQuit(t *testing.T) {
	r := NewSlashRegistry()
	sc, _, _, quit := newTestSlashContext()
	if err := r.Dispatch(sc, "/quit", nil); err != nil {
		t.Fatalf("/quit: %v", err)
	}
	if !*quit {
		t.Fatal("/quit did not invoke Quit sink")
	}
}

func TestSlashRemovedCommandsAreUnknown(t *testing.T) {
	// F/B pass: these commands were removed because they don't exist
	// in upstream pi. Verify they now return "unknown command".
	r := NewSlashRegistry()
	for _, cmd := range []string{"/clear", "/agent", "/agents", "/tools", "/cost", "/save", "/help", "/models"} {
		sc, _, _, _ := newTestSlashContext()
		err := r.Dispatch(sc, cmd, nil)
		if err == nil {
			t.Errorf("%s should be unknown but succeeded", cmd)
		} else if !errors.Is(err, ErrUnknownSlashCommand) {
			t.Errorf("%s error = %q, expected ErrUnknownSlashCommand", cmd, err)
		}
	}
}

func TestSlashCopyWithNoLastMessage(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()
	sc.LastAssistant = func() string { return "" }
	sc.CopyClipboard = func(_ string) error { return nil }
	err := r.Dispatch(sc, "/copy", nil)
	if err == nil || err.Error() != "No agent messages to copy yet." {
		t.Fatalf("/copy with empty last msg: err = %v, output %q; want upstream's showError message", err, out.String())
	}
}

// A clipboard failure reports the clipboard's own message, as upstream's
// handleCopyCommand shows error.message.
func TestSlashCopyReportsClipboardError(t *testing.T) {
	r := NewSlashRegistry()
	sc, _, _, _ := newTestSlashContext()
	sc.LastAssistant = func() string { return "answer" }
	sc.CopyClipboard = func(string) error { return errors.New("Clipboard unavailable") }
	if err := r.Dispatch(sc, "/copy", nil); err == nil || err.Error() != "Clipboard unavailable" {
		t.Fatalf("/copy clipboard failure: err = %v, want the clipboard message", err)
	}
}

func TestSlashHandlerErrorBubbles(t *testing.T) {
	r := NewSlashRegistry()
	r.Register(BuiltinSlashCommand{
		Name:    "boom",
		Handler: func(_ *SlashContext) error { return errors.New("kaboom") },
	})
	sc, _, _, _ := newTestSlashContext()
	err := r.Dispatch(sc, "/boom", nil)
	if err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("err = %v, want kaboom", err)
	}
}

func TestSlashExtensionDispatch(t *testing.T) {
	r := NewSlashRegistry()
	called := false
	r.ReplaceDynamic([]SlashCommand{{
		Name:        "chain",
		Description: "x",
		Handler: func(_ *ExtensionContext, args string) error {
			if args != "spec planner" {
				t.Errorf("args = %q", args)
			}
			called = true
			return nil
		},
	}})
	sc, _, _, _ := newTestSlashContext()
	extCtx := &ExtensionContext{}
	if err := r.Dispatch(sc, "/chain spec planner", extCtx); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("dynamic handler not invoked")
	}
}

func TestReplaceDynamic_StripsLeadingSlash(t *testing.T) {
	r := NewSlashRegistry()
	called := false
	r.ReplaceDynamic([]SlashCommand{{
		Name: "/piglets",
		Handler: func(_ *ExtensionContext, _ string) error {
			called = true
			return nil
		},
	}})
	sc, _, _, _ := newTestSlashContext()
	if err := r.Dispatch(sc, "/piglets", &ExtensionContext{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("handler not invoked for /piglets")
	}
	all := r.All()
	for _, cmd := range all {
		if cmd.Name == "/piglets" {
			t.Fatalf("dynamic command kept leading slash: %#v", cmd)
		}
	}
}

// /model handler: empty args opens picker (when wired),
// args trigger direct switch, no plumbing falls back to print-only.

func TestModelHandlerEmptyArgsOpensPicker(t *testing.T) {
	sc, _, _, _ := newTestSlashContext()
	pickedSpec := "openai/gpt-4o"
	sc.PickModel = func(string) (string, bool) { return pickedSpec, true }
	switched := ""
	sc.SwitchModel = func(spec string) error { switched = spec; return nil }
	var statusMsg string
	sc.ShowStatus = func(s string) { statusMsg = s }

	if err := modelHandler(sc); err != nil {
		t.Fatalf("modelHandler: %v", err)
	}
	if switched != pickedSpec {
		t.Errorf("SwitchModel called with %q, want %q", switched, pickedSpec)
	}
	if statusMsg != "Model: gpt-4o" {
		t.Errorf("expected status 'Model: gpt-4o', got %q", statusMsg)
	}
}

func TestModelHandlerEmptyArgsPickerCancelled(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.PickModel = func(string) (string, bool) { return "", false }
	called := false
	sc.SwitchModel = func(string) error { called = true; return nil }

	if err := modelHandler(sc); err != nil {
		t.Fatalf("modelHandler: %v", err)
	}
	if called {
		t.Errorf("SwitchModel should not be called on cancel")
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancel message, got: %q", out.String())
	}
}

func TestModelHandlerDirectSwitchByArgs(t *testing.T) {
	sc, _, _, _ := newTestSlashContext()
	sc.Args = "github-copilot/gpt-4o-mini"
	sc.ResolveModel = func(input string) (string, bool) { return input, true }
	switched := ""
	sc.SwitchModel = func(spec string) error { switched = spec; return nil }
	var statusMsg string
	sc.ShowStatus = func(s string) { statusMsg = s }

	if err := modelHandler(sc); err != nil {
		t.Fatalf("modelHandler: %v", err)
	}
	if switched != "github-copilot/gpt-4o-mini" {
		t.Errorf("SwitchModel called with %q", switched)
	}
	if statusMsg != "Model: gpt-4o-mini" {
		t.Errorf("expected status 'Model: gpt-4o-mini', got %q", statusMsg)
	}
}

func TestModelHandlerDirectSwitchNoMatchingModels(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.Args = "nonexistent/fake-model-xyz"
	sc.ResolveModel = func(string) (string, bool) { return "", false }
	called := false
	sc.SwitchModel = func(string) error { called = true; return nil }

	if err := modelHandler(sc); err != nil {
		t.Fatalf("modelHandler: %v", err)
	}
	if called {
		t.Fatal("SwitchModel should not be called when no models match")
	}
	if !strings.Contains(out.String(), "No matching models") {
		t.Fatalf("expected no-match message, got: %q", out.String())
	}
}

func TestModelHandlerNoMatchOpensPickerWithQuery(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.Args = "nonexistent/fake-model-xyz"
	sc.ResolveModel = func(string) (string, bool) { return "", false }
	var initialQuery string
	sc.PickModel = func(query string) (string, bool) {
		initialQuery = query
		return "", false
	}
	sc.SwitchModel = func(string) error { t.Fatal("SwitchModel should not run when picker is cancelled"); return nil }

	if err := modelHandler(sc); err != nil {
		t.Fatalf("modelHandler: %v", err)
	}
	if initialQuery != sc.Args {
		t.Fatalf("PickModel initial query = %q, want %q", initialQuery, sc.Args)
	}
	if strings.Contains(out.String(), "No matching models") {
		t.Fatalf("picker-capable no-match should not append bare no-match line, got: %q", out.String())
	}
}

func TestModelHandlerSwitchPropagatesError(t *testing.T) {
	sc, _, _, _ := newTestSlashContext()
	sc.Args = "bogus/model"
	sc.SwitchModel = func(string) error { return errors.New("auth missing") }

	err := modelHandler(sc)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "auth missing") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestModelHandlerHeadlessFallback(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.ModelName = func() string { return "gpt-4o" }
	// no PickModel, no SwitchModel
	if err := modelHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Current model: gpt-4o") {
		t.Errorf("expected current-model line, got: %q", out.String())
	}
	if !strings.Contains(out.String(), "Only showing models from configured providers. Use /login to add providers.") {
		t.Errorf("expected updated hint text, got: %q", out.String())
	}
}

func TestScopedModelsHandler(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()
	if err := r.Dispatch(sc, "/scoped-models", nil); err != nil {
		t.Fatal(err)
	}
	// Without ShowScopedModels wired, should say not available.
	if !strings.Contains(out.String(), "not available") {
		t.Errorf("/scoped-models should say not available without ShowScopedModels, got: %q", out.String())
	}
}

// BuiltinSlashCommands() must be the same registry the
// dispatcher uses, so autocomplete and dispatch never drift.
func TestBuiltinSlashCommands_MatchesRegistry(t *testing.T) {
	got := BuiltinSlashCommands()
	if len(got) == 0 {
		t.Fatal("BuiltinSlashCommands returned empty")
	}
	want := defaultBuiltins()
	if len(got) != len(want) {
		t.Fatalf("BuiltinSlashCommands len=%d, defaultBuiltins len=%d", len(got), len(want))
	}
	for i := range got {
		if got[i].Name != want[i].Name {
			t.Errorf("index %d: %q vs %q", i, got[i].Name, want[i].Name)
		}
		if got[i].Description != want[i].Description {
			t.Errorf("index %d desc mismatch: %q vs %q", i, got[i].Description, want[i].Description)
		}
	}
	// Sanity: must include the base commands after F/B slash cleanup.
	names := make(map[string]bool, len(got))
	for _, c := range got {
		names[c.Name] = true
	}
	for _, want := range []string{"quit", "model", "scoped-models", "copy", "session", "hotkeys", "new", "settings", "export", "import", "login", "logout", "llama"} {
		if !names[want] {
			t.Errorf("BuiltinSlashCommands missing %q", want)
		}
	}
	// Verify removed commands are NOT present.
	for _, removed := range []string{"clear", "agent", "agents", "tools", "cost", "save", "help", "models"} {
		if names[removed] {
			t.Errorf("BuiltinSlashCommands should NOT contain removed command %q", removed)
		}
	}
}

func TestSlashLoginDefaultProvider(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()

	var loginCalled string
	sc.ShowLoginAuthType = func() (string, bool) { return "oauth", true }
	sc.Login = func(_ context.Context, provider string) error {
		loginCalled = provider
		return nil
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		if mode != "login-oauth" {
			t.Errorf("expected mode=login-oauth, got %q", mode)
		}
		return "github-copilot", true
	}

	if err := r.Dispatch(sc, "/login", nil); err != nil {
		t.Fatalf("/login: %v", err)
	}
	if loginCalled != "github-copilot" {
		t.Errorf("expected github-copilot, got %q", loginCalled)
	}
	if out.Len() > 0 {
		t.Errorf("expected no output on success, got %q", out.String())
	}
}

func TestSlashLoginExplicitProvider(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()

	var loginCalled string
	sc.ShowLoginAuthType = func() (string, bool) { return "oauth", true }
	sc.Login = func(_ context.Context, provider string) error {
		loginCalled = provider
		return nil
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		return "anthropic", true
	}

	// With picker-based flow, args are ignored; the picker decides.
	if err := r.Dispatch(sc, "/login github-copilot", nil); err != nil {
		t.Fatalf("/login github-copilot: %v", err)
	}
	// Picker returns anthropic regardless of args.
	if loginCalled != "anthropic" {
		t.Errorf("expected anthropic, got %q", loginCalled)
	}
	_ = out
}

func TestSlashLoginUnsupportedProvider(t *testing.T) {
	// With the picker-based flow, there's no "unsupported" case at the
	// slash level: the picker only shows valid providers. Test that
	// cancelling the picker results in no login call.
	r := NewSlashRegistry()
	sc, _, _, _ := newTestSlashContext()

	sc.Login = func(_ context.Context, provider string) error {
		t.Fatalf("login should not be called on cancel")
		return nil
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		return "", false // user cancelled
	}

	if err := r.Dispatch(sc, "/login bogus-provider", nil); err != nil {
		t.Fatalf("/login bogus-provider: %v", err)
	}
}

// TestSlashLoginAnthropic verifies that picker selecting anthropic is
// wired to the login callback.
func TestSlashLoginAnthropic(t *testing.T) {
	r := NewSlashRegistry()
	sc, _, _, _ := newTestSlashContext()

	var loginCalled string
	sc.ShowLoginAuthType = func() (string, bool) { return "oauth", true }
	sc.Login = func(_ context.Context, provider string) error {
		loginCalled = provider
		return nil
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		return "anthropic", true
	}

	if err := r.Dispatch(sc, "/login", nil); err != nil {
		t.Fatalf("/login: %v", err)
	}
	if loginCalled != "anthropic" {
		t.Errorf("expected anthropic, got %q", loginCalled)
	}
}

func TestSlashLoginNilHandler(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()
	// Login is nil (headless context)

	if err := r.Dispatch(sc, "/login", nil); err != nil {
		t.Fatalf("/login: %v", err)
	}
	if !strings.Contains(out.String(), "not available") {
		t.Errorf("expected 'not available' message, got %q", out.String())
	}
}

func TestSlashLogoutDefault(t *testing.T) {
	r := NewSlashRegistry()
	sc, _, _, _ := newTestSlashContext()

	var logoutCalled string
	sc.Logout = func(provider string) error {
		logoutCalled = provider
		return nil
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		if mode != "logout" {
			t.Errorf("expected mode=logout, got %q", mode)
		}
		return "github-copilot", true
	}

	if err := r.Dispatch(sc, "/logout", nil); err != nil {
		t.Fatalf("/logout: %v", err)
	}
	if logoutCalled != "github-copilot" {
		t.Errorf("expected github-copilot, got %q", logoutCalled)
	}
}

func TestSlashLogoutNilHandler(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()

	if err := r.Dispatch(sc, "/logout", nil); err != nil {
		t.Fatalf("/logout: %v", err)
	}
	if !strings.Contains(out.String(), "not available") {
		t.Errorf("expected 'not available' message, got %q", out.String())
	}
}

func TestSlashLoginAPIKeyFlow(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()
	sc.ShowLoginAuthType = func() (string, bool) { return "api_key", true }
	sc.ShowOAuthSelector = func(mode string) (string, bool) {
		if mode != "login-api-key" {
			t.Fatalf("mode = %q, want login-api-key", mode)
		}
		return "openai", true
	}
	sc.ShowTextInput = func(title, placeholder string) (string, bool) {
		if title == "" || placeholder == "" {
			t.Fatalf("expected title+placeholder for api key input")
		}
		return "sk-test", true
	}
	var setProvider, setValue string
	sc.SetAPIKey = func(provider, value string) error {
		setProvider, setValue = provider, value
		return nil
	}
	if err := r.Dispatch(sc, "/login", nil); err != nil {
		t.Fatalf("/login api key: %v", err)
	}
	if setProvider != "openai" || setValue != "sk-test" {
		t.Fatalf("SetAPIKey = (%q,%q)", setProvider, setValue)
	}
	if out.Len() != 0 {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestSlashLogoutShowsStatusName(t *testing.T) {
	r := NewSlashRegistry()
	sc, out, _, _ := newTestSlashContext()
	sc.Logout = func(provider string) error { return nil }
	sc.LogoutProviderName = func(provider string) string {
		if provider != "github-copilot" {
			t.Fatalf("provider = %q", provider)
		}
		return "GitHub Copilot"
	}
	sc.ShowOAuthSelector = func(mode string) (string, bool) { return "github-copilot", true }
	if err := r.Dispatch(sc, "/logout", nil); err != nil {
		t.Fatalf("/logout: %v", err)
	}
	if !strings.Contains(out.String(), "Logged out of GitHub Copilot") {
		t.Fatalf("expected named logout message, got %q", out.String())
	}
}

func TestSessionHandlerMessageCounting(t *testing.T) {
	makeEntry := func(message agent.AgentMessage) SessionEntry {
		type wireEntry struct {
			SessionEntryBase
			Message agent.AgentMessage `json:"message"`
		}
		e := wireEntry{
			SessionEntryBase: SessionEntryBase{Type: "message", ID: "x"},
			Message:          message,
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		return NewSessionEntry(raw, e.SessionEntryBase)
	}

	// 2 plain user messages.
	userEntries := []SessionEntry{
		makeEntry(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hi"}}}}),
		makeEntry(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}}),
	}
	// 3 assistant messages; 2nd and 3rd each have one tool_use block.
	assistEntries := []SessionEntry{
		makeEntry(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "thinking"}}}}),
		makeEntry(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "ok"},
			ai.ToolCall{ID: "t1", Name: "bash"},
		}}}),
		makeEntry(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "t2", Name: "read"},
		}}}),
	}
	// 3 tool-result messages.
	toolResultEntries := []SessionEntry{
		makeEntry(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "t1", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "out1"}}}}),
		makeEntry(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "t2", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "out2"}}}}),
		makeEntry(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "t3", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "out3"}}}}),
	}

	var allEntries []SessionEntry
	allEntries = append(allEntries, userEntries...)
	allEntries = append(allEntries, assistEntries...)
	allEntries = append(allEntries, toolResultEntries...)

	sess := NewSession("test-id", "/tmp")
	for _, e := range allEntries {
		if err := sess.AppendEntry(e); err != nil {
			t.Fatalf("AppendEntry: %v", err)
		}
	}

	sc, out, _, _ := newTestSlashContext()
	sc.CurrentSession = func() *Session { return sess }

	if err := sessionHandler(sc); err != nil {
		t.Fatalf("sessionHandler: %v", err)
	}

	got := out.String()
	// Strip ANSI escape sequences so we can do plain-string checks.
	ansiRE := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	plain := ansiRE.ReplaceAllString(got, "")
	cases := []struct{ label, want string }{
		{"total count", "Total: 8"},
		{"user count", "User: 2"},
		{"assistant count", "Assistant: 3"},
		{"tools line", "Tools: 2 calls, 3 results"},
	}
	for _, c := range cases {
		if !strings.Contains(plain, c.want) {
			t.Errorf("%s: output does not contain %q\nfull output (plain):\n%s", c.label, c.want, plain)
		}
	}
}

func TestSessionHandlerUsesAllEntryUsage(t *testing.T) {
	sess := NewSession("stats-session", "/tmp")
	appendMessage := func(id string, message agent.AgentMessage) {
		t.Helper()
		entry := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: id, ParentID: sess.LeafID(), Timestamp: "2025-01-01T00:00:00Z"}, Message: message}
		if err := sess.AppendEntry(entry); err != nil {
			t.Fatal(err)
		}
	}
	appendMessage("user", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}})
	appendMessage("assistant", agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: "assistant", Provider: "provider", ModelID: "model", ResponseModel: "resolved-model",
		Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}, ai.ToolCall{ID: "call", Name: "read"}},
		Usage:   &ai.Usage{Input: 10, Output: 5, CacheRead: 20, CacheWrite: 3, Cost: ai.UsageCost{Total: 0.5}},
	}})
	appendMessage("tool", agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call", ToolName: "read", Usage: &ai.Usage{Input: 2, Output: 1, Cost: ai.UsageCost{Total: 0.1}}}})
	if _, err := sess.AppendBashExecution("pwd", "/tmp", nil, false, false, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendCompaction("summary", "user", 1, nil, false, &ai.Usage{Input: 4, Output: 2, Cost: ai.UsageCost{Total: 0.2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendBranchSummary(sess.LeafID(), "branch", nil, false, &ai.Usage{Input: 1, Output: 1, Cost: ai.UsageCost{Total: 0.05}}); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetLeafID(new("user")); err != nil {
		t.Fatal(err)
	}

	sc, out, _, _ := newTestSlashContext()
	sc.CurrentSession = func() *Session { return sess }
	if err := sessionHandler(sc); err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`).ReplaceAllString(out.String(), "")
	for _, want := range []string{
		"Total: 4", "User: 1", "Assistant: 1", "Tools: 1 calls, 1 results",
		"Input: 40", "Cached: 20 (50.0%)", "Uncached: 20 (3 written to cache)",
		"Output: 9", "Total: 49", "Total: $0.850", "provider/resolved-model:", "Tools/summaries:",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("output missing %q:\n%s", want, plain)
		}
	}
}

func TestSessionHandlerRepeatedCallAllocationsDoNotScaleWithEntries(t *testing.T) {
	build := func(entries int) *SlashContext {
		t.Helper()
		sess := NewSession("stats-session", "/tmp")
		for i := range entries {
			entry := MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: fmt.Sprintf("u%d", i), Timestamp: "2025-01-01T00:00:00Z"}, Message: agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: strings.Repeat("x", 1024)}}}}}
			if err := sess.AppendEntry(entry); err != nil {
				t.Fatal(err)
			}
		}
		return &SlashContext{CurrentSession: func() *Session { return sess }, AppendText: func(string) {}}
	}
	measure := func(sc *SlashContext) float64 {
		return testing.AllocsPerRun(20, func() {
			if err := sessionHandler(sc); err != nil {
				t.Fatal(err)
			}
		})
	}
	small := measure(build(10))
	large := measure(build(10_000))
	t.Logf("/session allocations: 10 entries=%.1f, 10,000 entries=%.1f", small, large)
	if large > small+10 {
		t.Fatalf("/session allocations scale with entries: 10 entries=%.1f, 10,000 entries=%.1f", small, large)
	}
}

func TestFormatNumber(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1,000"},
		{49251, "49,251"},
		{256896, "256,896"},
		{1300000, "1,300,000"},
		{-1000, "-1,000"},
		// Edge cases caught in live testing: previously untested.
		{1, "1"},
		{100, "100"},
		{1234567890, "1,234,567,890"},
	}
	for _, tt := range tests {
		got := formatNumber(tt.in)
		if got != tt.want {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The visible builtin order follows BUILTIN_SLASH_COMMANDS in
// packages/coding-agent/src/core/slash-commands.ts (0.87.1 moved tree after
// model). llama is PiG's own command and follows the upstream table.
func TestBuiltinSlashCommandsFollowUpstreamOrder(t *testing.T) {
	want := []string{
		"settings", "model", "tree", "thinking", "scoped-models", "export", "import", "share", "bug",
		"copy", "name", "session", "changelog", "hotkeys", "fork", "clone", "trust", "login", "logout",
		"new", "compact", "resume", "reload", "quit", "llama",
	}
	var got []string
	for _, cmd := range BuiltinSlashCommands() {
		if cmd.Hidden || strings.HasPrefix(cmd.Name, "probe-") {
			continue
		}
		got = append(got, cmd.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("builtin order = %v, want %v", got, want)
	}
}
