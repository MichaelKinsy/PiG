package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func exportTestUser(t *testing.T, session *Session, text string) string {
	t.Helper()
	id, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// branchedExportSession builds root → abandoned and root → kept → tail, with
// the leaf on tail, so an export must drop the abandoned branch.
func branchedExportSession(t *testing.T) (session *Session, branchIDs []string) {
	t.Helper()
	session = NewSession("export-session", t.TempDir())
	root := exportTestUser(t, session, "root")
	exportTestUser(t, session, "abandoned")
	if err := session.SetLeafID(&root); err != nil {
		t.Fatal(err)
	}
	kept := exportTestUser(t, session, "kept")
	tail := exportTestUser(t, session, "tail")
	return session, []string{root, kept, tail}
}

func parseJSONL(t *testing.T, content string) []map[string]any {
	t.Helper()
	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSuffix(content, "\n"), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func recordIDs(records []map[string]any, field string) []any {
	out := make([]any, 0, len(records))
	for _, record := range records {
		out = append(out, record[field])
	}
	return out
}

// Mirrors upstream serializeSessionBranch: a fresh header, the current branch
// with each parentId chained to the previous entry, then trailing entries.
func TestSerializeSessionBranchChainsTheCurrentBranch(t *testing.T) {
	session, ids := branchedExportSession(t)
	now := time.Date(2026, 9, 23, 10, 11, 12, 345_000_000, time.UTC)
	var trailingParent *string
	var trailingTimestamp string
	content, err := SerializeSessionBranch(session.Header(), BugReportBranch(session), now, func(parentID *string, timestamp string) []any {
		trailingParent, trailingTimestamp = parentID, timestamp
		return []any{map[string]any{"type": "custom", "note": "<trailing>"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	records := parseJSONL(t, content)
	if len(records) != 5 {
		t.Fatalf("records = %d, want header + 3 + trailing:\n%s", len(records), content)
	}
	header := records[0]
	if header["type"] != "session" || header["id"] != session.ID() || header["cwd"] != session.CWD() || header["timestamp"] != "2026-09-23T10:11:12.345Z" || header["version"] != float64(CurrentSessionVersion) {
		t.Fatalf("header = %v", header)
	}
	if !strings.HasPrefix(content, `{"type":"session","version":`) {
		t.Fatalf("header key order differs from upstream: %s", content[:60])
	}
	conversation := records[1:4]
	if got := recordIDs(conversation, "id"); !slices.Equal(got, []any{ids[0], ids[1], ids[2]}) {
		t.Fatalf("ids = %v, want %v", got, ids)
	}
	if got := recordIDs(conversation, "parentId"); !slices.Equal(got, []any{nil, ids[0], ids[1]}) {
		t.Fatalf("parentIds = %v", got)
	}
	if trailingParent == nil || *trailingParent != ids[2] || trailingTimestamp != "2026-09-23T10:11:12.345Z" {
		t.Fatalf("trailing called with %v %q", trailingParent, trailingTimestamp)
	}
	// JSON.stringify does not escape HTML characters.
	if !strings.Contains(content, `"note":"<trailing>"`) {
		t.Fatalf("HTML characters escaped:\n%s", content)
	}
}

func TestSerializeSessionBranchEmptyBranchPassesNilParent(t *testing.T) {
	session := NewSession("empty", t.TempDir())
	called := false
	content, err := SerializeSessionBranch(session.Header(), BugReportBranch(session), time.Now(), func(parentID *string, _ string) []any {
		called = true
		if parentID != nil {
			t.Fatalf("parentID = %q, want nil", *parentID)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("content=%q err=%v called=%v", content, err, called)
	}
	if records := parseJSONL(t, content); len(records) != 1 {
		t.Fatalf("records = %v", records)
	}
}

// Mirrors upstream exportSessionToJsonl: relative paths resolve against the
// process cwd, parents are created, and the default name is timestamped.
func TestExportSessionToJsonlResolvesPathsAndDefaultsTheName(t *testing.T) {
	dir := chdirTemp(t)
	session, _ := branchedExportSession(t)
	filePath, err := ExportSessionToJsonl(session, filepath.Join("nested", "out.jsonl"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "nested", "out.jsonl"); filePath != want {
		t.Fatalf("path = %q, want %q", filePath, want)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Fatal(err)
	}
	defaultPath, err := ExportSessionToJsonl(session, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(defaultPath) != dir || !regexp.MustCompile(`^session-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z\.jsonl$`).MatchString(filepath.Base(defaultPath)) {
		t.Fatalf("default path = %q", defaultPath)
	}
}

// Upstream /export <file>.jsonl calls session.exportToJsonl: the current branch
// only, no pi.share entry, and works for an in-memory session.
func TestExportHandlerJSONLWritesTheCurrentBranch(t *testing.T) {
	dir := chdirTemp(t)
	session, ids := branchedExportSession(t)
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = "out.jsonl"
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "out.jsonl")
	if !strings.Contains(out.String(), "Session exported to: "+filePath) {
		t.Fatalf("status = %q", out.String())
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	records := parseJSONL(t, string(data))
	if got := recordIDs(records[1:], "id"); !slices.Equal(got, []any{ids[0], ids[1], ids[2]}) {
		t.Fatalf("exported ids = %v", got)
	}
	if strings.Contains(string(data), "abandoned") || strings.Contains(string(data), "pi.share") {
		t.Fatalf("export carries another branch or a share entry:\n%s", data)
	}
}

func TestExportHandlerJSONLReportsWriteFailure(t *testing.T) {
	dir := chdirTemp(t)
	session, _ := branchedExportSession(t)
	if err := os.WriteFile(filepath.Join(dir, "blocker"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sc, _ := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = filepath.Join("blocker", "out.jsonl")
	err := exportHandler(sc)
	if err == nil || !strings.HasPrefix(err.Error(), "Failed to export session: ") {
		t.Fatalf("err = %v", err)
	}
}

// htmlExportSession returns a session whose file on disk holds one exchange.
func htmlExportSession(t *testing.T, name string) *Session {
	t.Helper()
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, name)
	jsonl := `{"type":"session","version":3,"id":"html-export","timestamp":"2026-05-14T12:00:00Z","cwd":"` + filepath.ToSlash(dir) + `"}
{"type":"message","id":"u1","parentId":null,"timestamp":"2026-05-14T12:00:01Z","message":{"role":"user","content":"hello","timestamp":1}}
`
	if err := os.WriteFile(sessionPath, []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Session{header: SessionHeader{CWD: dir}, path: sessionPath}
}

// Upstream handleExportCommand reports every exportToHtml failure through
// showError as "Failed to export session: <message>", and exportSessionToHtml
// refuses an in-memory session and one whose file has not been written yet.
func TestExportHandlerHTMLFailuresMatchUpstream(t *testing.T) {
	chdirTemp(t)
	for _, tc := range []struct {
		name    string
		session func(t *testing.T) *Session
		want    string
	}{
		{"in-memory", func(t *testing.T) *Session { return NewSession("in-memory", t.TempDir()) }, "Failed to export session: Cannot export in-memory session to HTML"},
		{"not written yet", func(t *testing.T) *Session { return shareTestSession(t, "unwritten") }, "Failed to export session: Nothing to export yet - start a conversation first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := tc.session(t)
			sc, out := newFakeSlashCtx()
			sc.CurrentSession = func() *Session { return session }
			err := exportHandler(sc)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("export also appended %q", out.String())
			}
		})
	}
}

// Upstream exportSessionToHtml names the default file
// `${APP_NAME}-session-${basename(sessionFile, ".jsonl")}.html` in the
// working directory and writes an explicit path exactly as given.
func TestExportHandlerHTMLOutputPathMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		args, want string
	}{
		{"", "pig-session-2026-05-14T12-00-00-000Z_html-export.html"},
		{"report", "report"},
		{`"my report.html" trailing words`, "my report.html"},
		{`'quoted.html'`, "quoted.html"},
		{"out.html extra", "out.html"},
		{`"unclosed.html`, "pig-session-2026-05-14T12-00-00-000Z_html-export.html"},
	} {
		t.Run(tc.args, func(t *testing.T) {
			dir := chdirTemp(t)
			session := htmlExportSession(t, "2026-05-14T12-00-00-000Z_html-export.jsonl")
			sc, out := newFakeSlashCtx()
			sc.CurrentSession = func() *Session { return session }
			sc.Args = tc.args
			if err := exportHandler(sc); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != "Session exported to: "+tc.want+"\n" {
				t.Fatalf("status = %q, want the path %q", got, tc.want)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != tc.want {
				t.Fatalf("cwd holds %v, want only %q", entries, tc.want)
			}
		})
	}
}

// Upstream writeFileSync does not create a missing parent directory.
func TestExportHandlerHTMLDoesNotCreateParentDirectory(t *testing.T) {
	dir := chdirTemp(t)
	sc, _ := newFakeSlashCtx()
	session := htmlExportSession(t, "s.jsonl")
	sc.CurrentSession = func() *Session { return session }
	sc.Args = filepath.Join("missing", "out.html")
	err := exportHandler(sc)
	if err == nil || !strings.HasPrefix(err.Error(), "Failed to export session: ") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(statErr) {
		t.Fatalf("export created the parent directory: %v", statErr)
	}
}

// Upstream getPathCommandArgument applies to the .jsonl form too.
func TestExportHandlerJSONLTakesQuotedPathArgument(t *testing.T) {
	dir := chdirTemp(t)
	session, _ := branchedExportSession(t)
	sc, out := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.Args = `"my branch.jsonl" ignored`
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(dir, "my branch.jsonl")
	if got := out.String(); got != "Session exported to: "+filePath+"\n" {
		t.Fatalf("status = %q", got)
	}
}

// Upstream handleExportCommand calls session.exportToHtml, which passes the
// live agent state (core/export-html/index.ts:267-268): session-data carries
// systemPrompt and tools {name, description, parameters}.
func TestExportHandlerHTMLIncludesLiveSystemPromptAndTools(t *testing.T) {
	dir := chdirTemp(t)
	session := htmlExportSession(t, "s.jsonl")
	sc, _ := newFakeSlashCtx()
	sc.CurrentSession = func() *Session { return session }
	sc.ShareState = func() ShareState {
		return ShareState{SystemPrompt: "LIVE <system> prompt", Tools: []ShareTool{{Name: "read", Description: "Read a file", Parameters: map[string]any{"type": "object"}}, {Name: "bare", Description: "No schema"}}}
	}
	sc.Args = "out.html"
	if err := exportHandler(sc); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(dir, "out.html"))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`<script id="session-data" type="application/json">([^<]+)</script>`).FindSubmatch(html)
	if match == nil {
		t.Fatal("session-data script not found")
	}
	raw, err := base64.StdEncoding.DecodeString(string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	if got := string(data["systemPrompt"]); got != `"LIVE <system> prompt"` {
		t.Fatalf("systemPrompt = %s", got)
	}
	if got, want := string(data["tools"]), `[{"name":"read","description":"Read a file","parameters":{"type":"object"}},{"name":"bare","description":"No schema"}]`; got != want {
		t.Fatalf("tools = %s, want %s", got, want)
	}
}
