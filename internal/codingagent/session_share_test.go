package codingagent

// pi: packages/coding-agent/src/modes/interactive/session-share.ts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mirrors upstream test/export-jsonl-share.test.ts "adds presentation data
// without changing conversation IDs or links": the share export appends one
// pi.share entry after the unchanged branch, and the file still imports.
func TestShareExportAddsPresentationDataWithoutChangingConversationLinks(t *testing.T) {
	dir := t.TempDir()
	session := NewSession("share-session", dir)
	userID := exportTestUser(t, session, "hello")
	assistantID, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role:       "assistant",
		Content:    []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "share_tool", Arguments: ai.JsonObject{"value": "example"}}},
		StopReason: ai.StopReasonToolUse,
		Timestamp:  2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	resultID, err := session.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{
		Role: "toolResult", ToolCallID: "call-1", ToolName: "share_tool",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Timestamp: 3,
	}})
	if err != nil {
		t.Fatal(err)
	}
	originalIDs := []any{userID, assistantID, resultID}
	state := ShareState{SystemPrompt: "SYSTEM <prompt>", Tools: []ShareTool{{
		Name: "share_tool", Description: "Render a value for sharing",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}}}

	normalPath := filepath.Join(dir, "normal.jsonl")
	if _, err := ExportSessionToJsonl(session, normalPath, nil); err != nil {
		t.Fatal(err)
	}
	normal, _ := os.ReadFile(normalPath)
	if strings.Contains(string(normal), "pi.share") {
		t.Fatal("a normal export carries the pi.share entry")
	}

	sharePath := filepath.Join(dir, "share.jsonl")
	if _, err := ExportSessionForShare(sharePath, session, state); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(sharePath)
	records := parseJSONL(t, string(data))
	conversation := records[1 : len(records)-1]
	if got := recordIDs(conversation, "id"); !slices.Equal(got, originalIDs) {
		t.Fatalf("ids = %v, want %v", got, originalIDs)
	}
	if got := recordIDs(conversation, "parentId"); !slices.Equal(got, []any{nil, userID, assistantID}) {
		t.Fatalf("parentIds = %v", got)
	}
	share := records[len(records)-1]
	if share["type"] != "custom" || share["customType"] != "pi.share" || share["parentId"] != resultID || share["timestamp"] == nil {
		t.Fatalf("share entry = %v", share)
	}
	// session-export.ts:13,27 the trailing entries receive the header's timestamp.
	if share["timestamp"] != records[0]["timestamp"] {
		t.Fatalf("share timestamp = %v, want the header timestamp %v", share["timestamp"], records[0]["timestamp"])
	}
	if id, _ := share["id"].(string); len(id) != 8 {
		t.Fatalf("share id = %q, want 8 characters like crypto.randomUUID().slice(0, 8)", id)
	}
	shareData, _ := share["data"].(map[string]any)
	if shareData["systemPrompt"] != "SYSTEM <prompt>" {
		t.Fatalf("systemPrompt = %v", shareData["systemPrompt"])
	}
	for _, absent := range []string{"renderedTools", "theme", "version"} {
		if _, ok := shareData[absent]; ok {
			t.Fatalf("share data has %q", absent)
		}
	}
	shareTools, _ := shareData["tools"].([]any)
	if len(shareTools) != 1 || shareTools[0].(map[string]any)["name"] != "share_tool" || shareTools[0].(map[string]any)["description"] != "Render a value for sharing" {
		t.Fatalf("tools = %v", shareData["tools"])
	}
	if !strings.HasSuffix(strings.TrimSpace(string(data)), `}`) || !strings.Contains(string(data), `{"type":"custom","customType":"pi.share","id":"`) {
		t.Fatalf("share entry key order differs from upstream:\n%s", data)
	}

	imported, err := NewSessionManagerWithDir(dir, dir).Load(sharePath)
	if err != nil {
		t.Fatal(err)
	}
	if leaf := imported.GetLeafID(); leaf == nil || *leaf != share["id"] {
		t.Fatalf("imported leaf = %v, want the share entry %v", leaf, share["id"])
	}
	var roles []string
	for _, message := range imported.BuildContext(imported.GetLeafID()) {
		switch {
		case message.User != nil:
			roles = append(roles, "user")
		case message.Assistant != nil:
			roles = append(roles, "assistant")
		case message.ToolResult != nil:
			roles = append(roles, "toolResult")
		}
	}
	if !slices.Equal(roles, []string{"user", "assistant", "toolResult"}) {
		t.Fatalf("imported roles = %v", roles)
	}
}

// Upstream createShareTrailingEntries records session.state.tools as
// {name, description, parameters}.
func TestNewShareStateRecordsActiveToolSchemas(t *testing.T) {
	active := tools.CreateCodingTools(t.TempDir(), tools.ToolsOptionsFromSettings(Settings{}, t.TempDir()))
	state := NewShareState("prompt", active)
	if state.SystemPrompt != "prompt" || len(state.Tools) != len(active) {
		t.Fatalf("state = %+v", state)
	}
	for i, tool := range active {
		schema := tool.Schema()
		got := state.Tools[i]
		if got.Name != tool.Name() || got.Description != schema.Description || got.Parameters == nil {
			t.Fatalf("tool %d = %+v", i, got)
		}
	}
}

// /bug attaches the pi.share entry to the transcript, as upstream
// buildBundle does through serializeSessionBranch.
func TestBugTranscriptEndsWithTheShareEntry(t *testing.T) {
	dir := chdirTemp(t)
	session := bugReportSession(t)
	script := &bugDialogScript{t: t, editor: "", choices: []string{"Yes, include the transcript", bugReportExport}}
	sc := script.context(session)
	sc.ShareState = func() ShareState {
		return ShareState{SystemPrompt: "BUG-SYSTEM", Tools: []ShareTool{{Name: "read", Description: "Read"}}}
	}
	lastID := *session.GetLeafID()
	if err := bugHandler(sc); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "pig-bug-report-*.zip"))
	if len(matches) != 1 {
		t.Fatalf("archives = %v", matches)
	}
	records := parseJSONL(t, string(readZip(t, matches[0])["session.jsonl"]))
	share := records[len(records)-1]
	data, _ := json.Marshal(share["data"])
	if share["customType"] != "pi.share" || share["parentId"] != lastID || !strings.Contains(string(data), "BUG-SYSTEM") {
		t.Fatalf("last transcript record = %v", share)
	}
}

func shareTestSession(t *testing.T, marker string) *Session {
	t.Helper()
	dir := t.TempDir()
	session := NewSession("share-"+marker, dir)
	session.SetPath(filepath.Join(dir, "session.jsonl"))
	exportTestUser(t, session, marker)
	return session
}

func TestShareSessionUploadsJSONLWithPrivacyNotice(t *testing.T) {
	var uploaded []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/artifacts" || request.URL.Query().Get("visibility") != "unlisted" || request.URL.Query().Get("title") != "PiG session" {
			t.Errorf("request URL = %s", request.URL)
		}
		if request.Method != http.MethodPost {
			t.Errorf("method = %s", request.Method)
		}
		if request.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Errorf("content type = %q", request.Header.Get("Content-Type"))
		}
		var err error
		uploaded, err = io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"` + serverOrigin(request) + `/session/p_test"}}`))
	}))
	defer server.Close()

	var statuses []string
	state := ShareState{SystemPrompt: "SYSTEM", Tools: []ShareTool{{Name: "read", Description: "Read a file"}}}
	result, err := shareSession(t.Context(), shareTestSession(t, "SHARE-MARKER"), state, server.URL+"/v1/artifacts?visibility=unlisted&title=PiG+session", server.Client(), func(message string) {
		statuses = append(statuses, message)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(statuses, []string{sharePrivacyNotice}) {
		t.Fatalf("statuses = %q", statuses)
	}
	shareURL := server.URL + "/session/p_test"
	if want := "Share URL: " + tui.Hyperlink(shareURL, shareURL); result != want {
		t.Fatalf("result = %q, want %q", result, want)
	}
	records := parseJSONL(t, string(uploaded))
	if len(records) != 3 {
		t.Fatalf("records = %v", records)
	}
	if records[1]["id"] == nil || records[2]["parentId"] != records[1]["id"] {
		t.Fatalf("conversation links changed: %v", records)
	}
	share := records[2]
	data := share["data"].(map[string]any)
	if share["customType"] != "pi.share" || data["systemPrompt"] != "SYSTEM" {
		t.Fatalf("share entry = %v", share)
	}
}

func serverOrigin(request *http.Request) string {
	return "http://" + request.Host
}

// Upstream packages/coding-agent/test/session-share.test.ts:27: keeps concurrent session exports isolated.
// D64 changes the transport to JSONL/HTTP. Both exports exist before A uploads, B waits until A completes, uploads are [A, B], and neither operation fails.
func TestShareSessionKeepsConcurrentExportsIsolated(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	aWritten, bWritten, releaseB := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var uploads, bodies, paths []string
	client := func(name string) *http.Client {
		return &http.Client{Transport: fakeRoundTripper(func(request *http.Request) (*http.Response, error) {
			defer func() { _ = request.Body.Close() }()
			ready, wait := aWritten, bWritten
			if name == "B" {
				ready, wait = bWritten, releaseB
			}
			close(ready)
			select {
			case <-wait:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				return nil, err
			}
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			var presentation shareEntry
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &presentation); err != nil {
				return nil, err
			}
			uploads = append(uploads, presentation.Data.SystemPrompt)
			bodies = append(bodies, string(body))
			if file, ok := request.Body.(*os.File); ok {
				paths = append(paths, file.Name())
			}
			return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"artifact":{"canonical_url":"https://share.example/` + name + `"}}`))}, nil
		})}
	}
	share := func(name string, session *Session) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, err := shareSession(ctx, session, ShareState{SystemPrompt: name}, "https://share.example", client(name), func(string) {})
			done <- err
		}()
		return done
	}
	a := shareTestSession(t, "A")
	b := shareTestSession(t, "B")
	doneA := share("A", a)
	select {
	case <-aWritten:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	doneB := share("B", b)
	select {
	case <-bWritten:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	errA := <-doneA
	close(releaseB)
	errB := <-doneB
	if errA != nil || errB != nil {
		t.Fatalf("errors = [%v, %v], want none", errA, errB)
	}
	if !slices.Equal(uploads, []string{"A", "B"}) {
		t.Fatalf("uploads = %q, want [A B]", uploads)
	}
	for i, name := range []string{"A", "B"} {
		records := parseJSONL(t, bodies[i])
		content := records[1]["message"].(map[string]any)["content"].([]any)
		if got := content[0].(map[string]any)["text"]; got != name {
			t.Fatalf("upload %s conversation = %v", name, got)
		}
	}
	if len(paths) != 2 || paths[0] == paths[1] {
		t.Fatalf("export paths are not independent: %v", paths)
	}
	for _, path := range paths {
		if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary export directory remains: %s: %v", path, err)
		}
	}
}

func TestShareSessionReportsGatewayError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":"session exceeds the 8 MiB limit"}`))
	}))
	defer server.Close()
	_, err := shareSession(t.Context(), shareTestSession(t, "too-large"), ShareState{}, server.URL, server.Client(), func(string) {})
	if err == nil || err.Error() != "Failed to upload session: session exceeds the 8 MiB limit" {
		t.Fatalf("err = %v", err)
	}
}

func TestUploadShareArtifactHonorsCancellationAndCanonicalOrigin(t *testing.T) {
	session := shareTestSession(t, "cancel")
	path := filepath.Join(t.TempDir(), "share.jsonl")
	if _, err := ExportSessionForShare(path, session, ShareState{}); err != nil {
		t.Fatal(err)
	}
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := uploadShareArtifact(ctx, http.DefaultClient, "https://pi-in-go.dev/api/share", path)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("different origin", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"https://evil.example/session/stolen"}}`))
		}))
		defer server.Close()
		_, err := uploadShareArtifact(t.Context(), server.Client(), server.URL, path)
		if err == nil || err.Error() != "share gateway returned a URL on a different origin" {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestShareHandlerUsesExplicitGatewayOperation(t *testing.T) {
	session := shareTestSession(t, "handler")
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.ShareState = func() ShareState { return ShareState{SystemPrompt: "HANDLER-SYSTEM"} }
	sc.ShareSession = func(got *Session, state ShareState, showStatus func(string)) (string, error) {
		if got != session || state.SystemPrompt != "HANDLER-SYSTEM" {
			t.Fatalf("share args = %p %+v", got, state)
		}
		showStatus(sharePrivacyNotice)
		return "Share URL: https://pi-in-go.dev/session/p_test", nil
	}
	if err := shareHandler(sc); err != nil {
		t.Fatal(err)
	}
	want := sharePrivacyNotice + "\nShare URL: https://pi-in-go.dev/session/p_test\n"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

func TestShareHandlerReportsCancellation(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return shareTestSession(t, "cancel-handler") }
	sc.ShareSession = func(*Session, ShareState, func(string)) (string, error) {
		return "", errShareCancelled
	}
	if err := shareHandler(sc); err != nil {
		t.Fatal(err)
	}
	if out.String() != "Share cancelled\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestShareGatewayURLDefaultAndOverride(t *testing.T) {
	t.Setenv("PI_SHARE_GATEWAY_URL", "")
	if got := shareGatewayURL(); got != defaultShareGatewayURL {
		t.Fatalf("default gateway = %q", got)
	}
	t.Setenv("PI_SHARE_GATEWAY_URL", " https://share.example/upload ")
	if got := shareGatewayURL(); got != "https://share.example/upload" {
		t.Fatalf("override gateway = %q", got)
	}
}

func TestShareBuiltinDescribesUnlistedExpiry(t *testing.T) {
	for _, command := range BuiltinSlashCommands() {
		if command.Name == "share" {
			if command.Description != "Share session with an unlisted 30-day link" {
				t.Fatalf("description = %q", command.Description)
			}
			return
		}
	}
	t.Fatal("share command is not registered")
}

func TestSharePrivacyNoticeRemainsVisibleAfterResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"` + serverOrigin(request) + `/session/p_notice"}}`))
	}))
	defer server.Close()

	mode := &InteractiveMode{chatContainer: tui.NewContainer()}
	session := shareTestSession(t, "notice")
	sc := &SlashContext{
		CurrentSession: func() *Session { return session },
		ShowStatus:     mode.showStatus,
		Append:         func(string) {},
		ShareSession: func(got *Session, state ShareState, showStatus func(string)) (string, error) {
			return shareSession(t.Context(), got, state, server.URL, server.Client(), showStatus)
		},
	}
	if err := shareHandler(sc); err != nil {
		t.Fatal(err)
	}
	screen := strings.Join(mode.chatContainer.Render(200), "\n")
	if !strings.Contains(screen, "Privacy:") {
		t.Fatalf("privacy notice not on screen after /share; chat = %q", screen)
	}
}

func TestShareLoaderEscapeCancelsUpload(t *testing.T) {
	for _, action := range []string{"escape", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				close(started)
				<-release
			}))
			defer func() {
				close(release)
				server.Close()
			}()
			t.Setenv("PI_SHARE_GATEWAY_URL", server.URL)

			editor := tui.NewEditor()
			editorContainer := tui.NewContainer()
			editorContainer.Add(editor)
			mode := &InteractiveMode{
				chatContainer:   tui.NewContainer(),
				uiTaskCh:        make(chan func(), 8),
				editor:          editor,
				editorContainer: editorContainer,
				tuiInst:         tui.NewWithOutput(io.Discard, 100, 30),
			}
			for i := range 60 {
				mode.chatContainer.Add(tui.NewText(fmt.Sprintf("transcript line %d", i)))
			}
			done := make(chan error, 1)
			session := shareTestSession(t, "cancel-loader")
			go func() {
				_, err := mode.shareSessionWithLoader(t.Context(), session, ShareState{}, func(string) {})
				done <- err
			}()

			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("share upload did not start")
			}
			if visible := len(mode.chatContainer.Render(200)); visible < 61 {
				t.Fatalf("share loader capped the transcript at %d lines; want the complete transcript", visible)
			}
			if action == "shutdown" {
				mode.requestShutdown()
			} else {
				deliverModalInput(t, mode, []byte("\x1b"))
			}
			select {
			case err := <-done:
				if !errors.Is(err, errShareCancelled) {
					t.Fatalf("err = %v, want share cancellation", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s did not cancel the share upload", action)
			}
			if screen := strings.Join(mode.chatContainer.Render(200), "\n"); !strings.Contains(screen, "Privacy:") {
				t.Fatalf("privacy notice missing after cancellation: %q", screen)
			}
		})
	}
}

// Pi's Radius share (the flow D64 replaces) serializes the branch from memory
// through exportSessionForShare, so an in-memory session shares too.
func TestShareHandlerSharesInMemorySession(t *testing.T) {
	session := NewSession("share-in-memory", t.TempDir())
	exportTestUser(t, session, "in-memory")
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	shared := false
	sc.ShareSession = func(got *Session, _ ShareState, _ func(string)) (string, error) {
		shared = got == session
		return "Share URL: https://pi-in-go.dev/session/p_memory", nil
	}
	if err := shareHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !shared || out.String() != "Share URL: https://pi-in-go.dev/session/p_memory\n" {
		t.Fatalf("shared = %v, output = %q", shared, out.String())
	}
}

// Upstream agent.state.systemPrompt replays every transcript system message
// (agent/src/agent.ts:89-91, ai/src/utils/transcript.ts:99-102): a later
// section diff joins the earlier baseline, and no system message renders "".
func TestAgentStateSystemPromptReplaysTranscriptSystemMessages(t *testing.T) {
	base := agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "preamble", Value: new("Test")}, {Name: "cwd", Value: new("<cwd>\n/w\n</cwd>")}}}}
	run := agent.AgentMessage{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "runmark", Value: new("<runmark>\nRUN\n</runmark>")}}}}
	user := userMsg("hi")
	for _, tc := range []struct {
		name     string
		messages []agent.AgentMessage
		want     string
	}{
		{"empty", nil, ""},
		{"no system message", []agent.AgentMessage{user}, ""},
		{"baseline", []agent.AgentMessage{base, user}, "Test\n\n<cwd>\n/w\n</cwd>"},
		{"later section diff", []agent.AgentMessage{base, user, run, user}, "Test\n\n<cwd>\n/w\n</cwd>\n\n<runmark>\nRUN\n</runmark>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AgentStateSystemPrompt(tc.messages); got != tc.want {
				t.Fatalf("AgentStateSystemPrompt = %q, want %q", got, tc.want)
			}
		})
	}
}

// Pi's /export, /share and /bug read session.state (agent-session.ts:3921,
// session-share.ts:38): the transcript's prompt, not a request-only forced
// prompt and not base-prompt changes the model has not seen yet.
func TestInteractiveShareStateReadsTheTranscriptSystemPrompt(t *testing.T) {
	m := sessionChromeMode(t)
	m.agent = mustNewAgent(agent.AgentOptions{})
	m.agent.SetTools(tools.CreateCodingTools(t.TempDir(), tools.ToolsOptionsFromSettings(Settings{}, t.TempDir()))[:2])
	m.agent.SetMessages([]agent.AgentMessage{
		{System: &ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "preamble", Value: new("TRANSCRIPT")}}}},
		userMsg("hi"),
	})
	m.opts.SystemPrompt = "BASE-NOT-SENT"
	m.agent.SetSystemPrompt("FORCED")
	state := m.buildSlashContext(t.Context()).ShareState()
	if state.SystemPrompt != "TRANSCRIPT" {
		t.Fatalf("ShareState().SystemPrompt = %q, want the transcript prompt %q", state.SystemPrompt, "TRANSCRIPT")
	}
	active := m.agent.Tools()
	if len(active) != 2 || len(state.Tools) != len(active) {
		t.Fatalf("ShareState().Tools = %d entries, want the %d active tools", len(state.Tools), len(active))
	}
	for i, tool := range active {
		if state.Tools[i].Name != tool.Name() {
			t.Fatalf("ShareState().Tools[%d] = %q, want %q", i, state.Tools[i].Name, tool.Name())
		}
	}
}

// JSON.stringify writes Pi's tool.parameters in source key order without
// escaping "<", "&" or U+2028; the export viewer lists parameters in that
// order (export-html/template.js Object.entries(properties)). Pi 0.87.1's RPC
// export_html writes read as {"type":"object","required":["path"],"properties":{"path":...,"offset":...,"limit":...}}.
func TestNewShareStateKeepsToolParameterOrder(t *testing.T) {
	var read agent.AgentTool
	for _, tool := range tools.CreateCodingTools(t.TempDir(), tools.ToolsOptionsFromSettings(Settings{}, t.TempDir())) {
		if tool.Name() == "read" {
			read = tool
		}
	}
	if read == nil {
		t.Fatal("read tool not found")
	}
	encoded, err := json.Marshal(NewShareState("", []agent.AgentTool{read}).Tools[0].Parameters)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},"limit":{"type":"number","description":"Maximum number of lines to read"}}}`
	if string(encoded) != want {
		t.Fatalf("read parameters = %s, want %s", encoded, want)
	}

	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"zz","description":"d","parameters":{"type":"object","properties":{"zeta":{"type":"string","description":"a\u2028b <x> & y"},"alpha":{"type":"number"}},"required":["zeta"]}}`), &schema); err != nil {
		t.Fatal(err)
	}
	line, err := marshalJSONLine(ShareTool{Name: schema.Name, Description: schema.Description, Parameters: orderedToolParameters(schema)})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"name\":\"zz\",\"description\":\"d\",\"parameters\":{\"type\":\"object\",\"properties\":{\"zeta\":{\"type\":\"string\",\"description\":\"a\u2028b <x> & y\"},\"alpha\":{\"type\":\"number\"}},\"required\":[\"zeta\"]}}"; string(line) != want {
		t.Fatalf("share tool = %s, want %s", line, want)
	}
}
