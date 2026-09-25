package codingagent

import (
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
	id, err := session.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: text}}, Timestamp: 1}})
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
