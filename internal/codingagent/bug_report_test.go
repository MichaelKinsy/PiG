package codingagent

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/pigversion"
)

// Upstream: test/bug-report.test.ts "removes URL credentials and secret query
// parameters".
func TestRedactBugReportURLMatchesPi(t *testing.T) {
	for input, want := range map[string]string{
		"https://user:pass@proxy.example.com:8080/":   "https://proxy.example.com:8080/",
		"git:https://pat@github.com/org/repo":         "git:https://github.com/org/repo",
		"https://api.example/v1?api-key=abc&model=x":  "https://api.example/v1?api-key=%3Credacted%3E&model=x",
		"https://example.com/path?q=1":                "https://example.com/path?q=1",
		"sk-123":                                      "sk-123",
		"https://user@host.example":                   "https://host.example/",
		"https://x.example/?token=a&token=b&keep=c d": "https://x.example/?token=%3Credacted%3E&keep=c+d",
	} {
		if got := RedactBugReportURL(input); got != want {
			t.Errorf("RedactBugReportURL(%q) = %q, want %q", input, got, want)
		}
	}
}

// Upstream: "redacts nested secret values without hiding token counts".
func TestRedactBugReportJSONKeepsTokenCounts(t *testing.T) {
	got, err := RedactBugReportJSON(map[string]any{
		"apiKey":     "sk-123",
		"headers":    map[string]any{"Authorization": "Bearer x", "X-Trace": "1"},
		"compaction": map[string]any{"reserveTokens": 16384, "keepRecentTokens": 20000},
		"baseUrl":    "https://me:secret@example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}
	indented, err := bugReportJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, indented); err != nil {
		t.Fatal(err)
	}
	encoded := compact.String()
	want := `{"apiKey":"<redacted>","baseUrl":"https://example.com/","compaction":{"keepRecentTokens":20000,"reserveTokens":16384},"headers":{"Authorization":"<redacted>","X-Trace":"1"}}`
	if encoded != want {
		t.Fatalf("redacted = %s, want %s", encoded, want)
	}
}

func TestBugReportArchiveFileNameUsesPigIdentity(t *testing.T) {
	if got := BugReportArchiveFileName("0190"); got != "pig-bug-report-0190.zip" {
		t.Fatalf("archive name = %q", got)
	}
}

// Upstream collectEnvironment records getPiUserAgent(VERSION). D65 uses PiG's product identity and the node:os platform/release/arch suffix instead of the coding-agent runtime suffix.
func TestBugReportEnvironmentUsesPiGUserAgent(t *testing.T) {
	const version = "test-" + pigversion.Version
	t.Setenv("NODE_OPTIONS", "")
	machine, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", `import os from "node:os"; process.stdout.write(" (" + os.platform() + " " + os.release() + "; " + os.arch() + ")");`).Output()
	if err != nil {
		t.Fatalf("read Node OS identity: %v", err)
	}
	environment := collectBugReportEnvironment(version, func(string) string { return "" }, nil)
	want := "pig/" + version + string(machine)
	if environment.UserAgent != want {
		t.Fatalf("UserAgent = %q, want independent Node OS identity %q", environment.UserAgent, want)
	}
}

// bugDialogScript answers /bug dialogs in order and records what they showed.
type bugDialogScript struct {
	t         *testing.T
	editor    string
	choices   []string
	prompts   []string
	status    []string
	appended  []string
	summaries int
}

func (s *bugDialogScript) context(session *Session) *SlashContext {
	return &SlashContext{
		Append:     func(text string) { s.appended = append(s.appended, text) },
		AppendText: func(text string) { s.appended = append(s.appended, text) },
		ShowStatus: func(text string) { s.status = append(s.status, text) },
		ModelName:  func() string { return "Test Model" },
		ShowExtensionEditor: func(title, description, prefill string) (string, bool) {
			s.prompts = append(s.prompts, title+"\n"+description)
			return s.editor, true
		},
		ShowExtensionSelector: func(title string, options []string, description string) (string, bool) {
			s.prompts = append(s.prompts, title+"\n"+description)
			if len(s.choices) == 0 {
				s.t.Fatalf("unexpected selector %q", title)
			}
			choice := s.choices[0]
			s.choices = s.choices[1:]
			if !slices.Contains(options, choice) {
				s.t.Fatalf("selector %q offers %v, script chose %q", title, options, choice)
			}
			return choice, true
		},
		CurrentSession: func() *Session { return session },
		BugReportInputs: func() (BugReportInputs, error) {
			return BugReportInputs{
				Version:        "0.0.0-test",
				MessageCount:   2,
				ThinkingLevel:  "medium",
				GlobalSettings: Settings{DefaultProvider: "openai"},
			}, nil
		},
		SummarizeForBugReport: func(modelName, hint string) (string, bool, error) {
			s.summaries++
			return "## What went wrong\nThe tool hung.", false, nil
		},
		UpstreamVersion: func() string { return "0.87.1" },
	}
}

func bugReportSession(t *testing.T) *Session {
	t.Helper()
	session := NewSession("session-bug", t.TempDir())
	if _, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "TRANSCRIPT-MARKER please read the file"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: 2, Provider: "openai", ModelID: "gpt-test", API: "openai-responses", StopReason: ai.StopReasonError, ErrorMessage: "Unexpected internal state"}}); err != nil {
		t.Fatal(err)
	}
	return session
}

func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	resolved, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// Upstream: "preserves line breaks in pasted descriptions", ending with
// Cancel on the delivery selector.
func TestBugPromptPreservesDescriptionLinesAndCancels(t *testing.T) {
	dir := chdirTemp(t)
	script := &bugDialogScript{t: t, editor: "Request failed\n  ↳ pi exiting...\nstack trace", choices: []string{"No", "No", "Cancel"}}
	if err := bugHandler(script.context(bugReportSession(t))); err != nil {
		t.Fatal(err)
	}
	delivery := script.prompts[len(script.prompts)-1]
	for _, want := range []string{"Description: Request failed", "↳ pi exiting...", "stack trace", "Nothing is uploaded"} {
		if !strings.Contains(delivery, want) {
			t.Errorf("delivery prompt lacks %q:\n%s", want, delivery)
		}
	}
	if !strings.Contains(script.prompts[2], "Attach a summary written by Test Model") {
		t.Errorf("summary prompt = %q", script.prompts[2])
	}
	if !slices.Equal(script.status, []string{bugReportCancelled}) {
		t.Fatalf("status = %v, want cancelled", script.status)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("cancelled report wrote %v", entries)
	}
}

// failingTransport fails and counts every HTTP request.
type failingTransport struct{ calls atomic.Int32 }

func (f *failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.calls.Add(1)
	return nil, errors.New("network is not allowed in /bug")
}

func TestBugExportWritesArchiveRecordsSessionAndLinksPigIssue(t *testing.T) {
	trap := &failingTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = trap
	t.Cleanup(func() { http.DefaultTransport = previous })

	dir := chdirTemp(t)
	session := bugReportSession(t)
	script := &bugDialogScript{t: t, editor: "Tool hung\nsecond line", choices: []string{"Yes, include the transcript", bugReportExport}}
	sc := script.context(session)
	sc.Args = "prefilled"
	if err := bugHandler(sc); err != nil {
		t.Fatal(err)
	}
	if trap.calls.Load() != 0 {
		t.Fatalf("/bug made %d HTTP requests", trap.calls.Load())
	}
	if script.summaries != 0 {
		t.Fatal("a transcript report must not request a summary")
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "pig-bug-report-*.zip"))
	if len(matches) != 1 {
		t.Fatalf("archives = %v", matches)
	}
	files := readZip(t, matches[0])
	if names := slices.Sorted(maps.Keys(files)); !slices.Equal(names, []string{"diagnostics.json", "report.json", "session.jsonl"}) {
		t.Fatalf("archive members = %v", names)
	}
	var report BugReportMetadata
	if err := json.Unmarshal(files["report.json"], &report); err != nil {
		t.Fatal(err)
	}
	if report.Hint == nil || *report.Hint != "Tool hung\nsecond line" || !report.Session.Included || report.Session.CWD != session.CWD() || report.Session.ID != "session-bug" {
		t.Fatalf("report = %+v", report)
	}
	if filepath.Base(matches[0]) != BugReportArchiveFileName(report.ID) {
		t.Fatalf("archive %s does not carry report id %s", matches[0], report.ID)
	}
	var diagnostics BugReportDiagnostics
	if err := json.Unmarshal(files["diagnostics.json"], &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.AssistantMessageCount != 1 || len(diagnostics.Assistant) != 1 || diagnostics.Assistant[0].ErrorMessage != "Unexpected internal state" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	assertSessionBranchJSONL(t, files["session.jsonl"], session)

	var recorded CustomEntry
	entries := session.Entries()
	if err := json.Unmarshal(entries[len(entries)-1].Raw(), &recorded); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(recorded.Data)
	if recorded.CustomType != BugReportCustomEntryType || !strings.Contains(string(data), `"delivery":"zip"`) || !strings.Contains(string(data), report.ID) {
		t.Fatalf("recorded entry = %s %s", recorded.CustomType, data)
	}

	link := script.appended[len(script.appended)-1]
	start := strings.Index(link, BugReportIssueURL)
	if start < 0 {
		t.Fatalf("no PiG issue link in %q", link)
	}
	assertIssueLink(t, link[start:], report)
}

func assertIssueLink(t *testing.T, raw string, report BugReportMetadata) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Host != "github.com" || parsed.Path != "/MichaelKinsy/PiG/issues/new" {
		t.Fatalf("link target = %s", raw)
	}
	query := parsed.Query()
	fields := slices.Sorted(maps.Keys(query))
	if !slices.Equal(fields, []string{"actual", "platform", "template", "title", "version"}) {
		t.Fatalf("link fields = %v", fields)
	}
	if query.Get("template") != "bug.yml" || query.Get("title") != "bug: Tool hung" || !strings.Contains(query.Get("actual"), report.ID) || query.Get("version") != "pig 0.0.0-test (Pi 0.87.1)" {
		t.Fatalf("link = %v", query)
	}
	if strings.Contains(raw, "TRANSCRIPT-MARKER") || strings.Contains(raw, "second") || len(raw) > 1024 {
		t.Fatalf("link carries session content or is unbounded: %s", raw)
	}
}

func assertSessionBranchJSONL(t *testing.T, data []byte, session *Session) {
	t.Helper()
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var lines []map[string]any
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if len(lines) != 3 || lines[0]["type"] != "session" || lines[0]["id"] != session.ID() {
		t.Fatalf("session.jsonl = %s", data)
	}
	if lines[1]["parentId"] != nil || lines[2]["parentId"] != lines[1]["id"] {
		t.Fatalf("parent chain = %v -> %v", lines[1]["parentId"], lines[2]["parentId"])
	}
	if !strings.Contains(string(data), "TRANSCRIPT-MARKER") {
		t.Fatal("transcript missing from session.jsonl")
	}
}

func TestBugSummaryReplacesTranscript(t *testing.T) {
	dir := chdirTemp(t)
	script := &bugDialogScript{t: t, editor: "", choices: []string{"No", "Yes, generate a summary", bugReportExport}}
	if err := bugHandler(script.context(bugReportSession(t))); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "pig-bug-report-*.zip"))
	if len(matches) != 1 || script.summaries != 1 {
		t.Fatalf("archives = %v summaries = %d", matches, script.summaries)
	}
	files := readZip(t, matches[0])
	if _, ok := files["session.jsonl"]; ok {
		t.Fatal("summary report must not include the transcript")
	}
	if string(files["summary.md"]) != "## What went wrong\nThe tool hung.\n" {
		t.Fatalf("summary.md = %q", files["summary.md"])
	}
	var report BugReportMetadata
	if err := json.Unmarshal(files["report.json"], &report); err != nil {
		t.Fatal(err)
	}
	if report.Hint != nil || report.Session.Included || !report.Session.SummaryIncluded || report.Session.CWD != "" {
		t.Fatalf("report session = %+v hint = %v", report.Session, report.Hint)
	}
}

func TestBugSummaryCancelWritesNothing(t *testing.T) {
	dir := chdirTemp(t)
	script := &bugDialogScript{t: t, choices: []string{"No", "Yes, generate a summary", bugReportExport}}
	sc := script.context(bugReportSession(t))
	sc.SummarizeForBugReport = func(string, string) (string, bool, error) { return "", true, nil }
	if err := bugHandler(sc); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 || !slices.Equal(script.status, []string{bugReportCancelled}) {
		t.Fatalf("entries = %v status = %v", entries, script.status)
	}
}

func TestBugReportMetadataRedactsSettingsAndModel(t *testing.T) {
	model := &ai.Model{ID: "m", DisplayName: "M", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom", API: "openai-completions", BaseURL: "https://u:p@llm.example/v1?api_key=k", Headers: map[string]string{"X-B": "1", "Authorization": "secret"}}}
	metadata, err := CollectBugReportMetadata(BugReportInputs{Model: model, GlobalSettings: Settings{DefaultProvider: "custom"}}, BugReportOptions{}, false, fixedBugReportTime())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Model.BaseURL != "https://llm.example/v1?api_key=%3Credacted%3E" || !reflect.DeepEqual(metadata.Model.HeaderNames, []string{"Authorization", "X-B"}) {
		t.Fatalf("model = %+v", metadata.Model)
	}
	encoded, _ := bugReportJSON(metadata)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), `\u003c`) {
		t.Fatalf("report leaks a header value or escapes HTML:\n%s", encoded)
	}
	if metadata.CreatedAt != "2026-01-02T03:04:05.006Z" || metadata.SchemaVersion != 1 {
		t.Fatalf("createdAt = %s", metadata.CreatedAt)
	}
}

// The /bug flow must never reach the network outside the consented summary,
// which goes through the session's own provider. None of its files import a
// network package.
func TestBugReportSourcesImportNoNetworkPackage(t *testing.T) {
	for _, file := range []string{"bug_report.go", "slash_bug.go", "interactive_bug.go"} {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range parsed.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if path == "net" || strings.HasPrefix(path, "net/http") || path == "net/rpc" || path == "net/smtp" {
				t.Errorf("%s imports %s", file, path)
			}
		}
	}
}

func readZip(t *testing.T, path string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	files := make(map[string][]byte)
	for _, member := range reader.File {
		if member.Method != zip.Deflate {
			t.Errorf("%s is not deflated", member.Name)
		}
		rc, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[member.Name] = data
	}
	return files
}

func fixedBugReportTime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC) }

// TestBugReportOmitsTrackingID mirrors upstream bug-report.ts redactSettings,
// which drops settings.trackingId.
func TestBugReportOmitsTrackingID(t *testing.T) {
	metadata, err := CollectBugReportMetadata(BugReportInputs{GlobalSettings: Settings{TrackingID: "tracking-secret", EnableAnalytics: new(true)}}, BugReportOptions{}, false, fixedBugReportTime())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bugReportJSON(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tracking-secret") || strings.Contains(string(encoded), "trackingId") {
		t.Fatalf("bug report carries the tracking identifier:\n%s", encoded)
	}
	if !strings.Contains(string(encoded), `"enableAnalytics": true`) && !strings.Contains(string(encoded), `"enableAnalytics":true`) {
		t.Fatalf("bug report dropped enableAnalytics:\n%s", encoded)
	}
}
