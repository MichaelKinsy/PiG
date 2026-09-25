package codingagent

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
	"sync"
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
	if leaf := imported.LeafID(); leaf == nil || *leaf != share["id"] {
		t.Fatalf("imported leaf = %v, want the share entry %v", leaf, share["id"])
	}
	var roles []string
	for _, message := range imported.BuildContext(imported.LeafID()) {
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
	active := tools.CreateCodingTools(t.TempDir(), Settings{}, t.TempDir())
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
	lastID := *session.LeafID()
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

// Mirrors upstream session-share.test.ts: concurrent exports use independent
// temporary files and upload the matching session.
func TestShareSessionKeepsConcurrentExportsIsolated(t *testing.T) {
	var mu sync.Mutex
	var uploads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		uploads = append(uploads, string(body))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"artifact":{"canonical_url":"` + serverOrigin(request) + `/session/p_concurrent"}}`))
	}))
	defer server.Close()

	sessions := map[string]*Session{
		"SESSION-A": shareTestSession(t, "SESSION-A"),
		"SESSION-B": shareTestSession(t, "SESSION-B"),
	}
	var wg sync.WaitGroup
	for marker, session := range sessions {
		wg.Go(func() {
			if _, err := shareSession(t.Context(), session, ShareState{SystemPrompt: marker}, server.URL, server.Client(), func(string) {}); err != nil {
				t.Errorf("share %s: %v", marker, err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 2 {
		t.Fatalf("uploads = %d", len(uploads))
	}
	for _, marker := range []string{"SESSION-A", "SESSION-B"} {
		count := 0
		for _, upload := range uploads {
			if strings.Contains(upload, marker) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("marker %s appears in %d uploads: %q", marker, count, uploads)
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
	deliverModalInput(t, mode, []byte("\x1b"))
	select {
	case err := <-done:
		if !errors.Is(err, errShareCancelled) {
			t.Fatalf("err = %v, want share cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Escape did not cancel the share upload")
	}
	if screen := strings.Join(mode.chatContainer.Render(200), "\n"); !strings.Contains(screen, "Privacy:") {
		t.Fatalf("privacy notice missing after cancellation: %q", screen)
	}
}
