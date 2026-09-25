package codingagent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSearchQuery(t *testing.T) {
	got := parseSearchQuery(`foo "bar baz"`)
	if got.mode != "tokens" || len(got.tokens) != 2 {
		t.Fatalf("parseSearchQuery tokens = %+v", got)
	}
	if got.tokens[0].kind != "fuzzy" || got.tokens[0].value != "foo" {
		t.Fatalf("first token = %+v", got.tokens[0])
	}
	if got.tokens[1].kind != "phrase" || got.tokens[1].value != "bar baz" {
		t.Fatalf("second token = %+v", got.tokens[1])
	}
	got = parseSearchQuery(`re:foo.*bar`)
	if got.mode != "regex" || got.regex == nil {
		t.Fatalf("regex parse = %+v", got)
	}
}

func TestFilterAndSortSessions(t *testing.T) {
	now := time.Now()
	sessions := []SessionInfo{
		{ID: "1", Name: "alpha", AllMessagesText: "deploy login", CWD: "/a", Modified: now.Add(-2 * time.Hour)},
		{ID: "2", Name: "beta", AllMessagesText: "node cve fix", CWD: "/b", Modified: now.Add(-1 * time.Hour)},
		{ID: "3", Name: "gamma", AllMessagesText: "login issue", CWD: "/c", Modified: now.Add(-3 * time.Hour)},
	}
	got := filterAndSortSessions(sessions, `"node cve"`, sessionSortRelevance)
	if len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("phrase filter got=%v", got)
	}
	got = filterAndSortSessions(sessions, `lgin`, sessionSortRelevance)
	if len(got) == 0 || got[0].ID != "1" && got[0].ID != "3" {
		t.Fatalf("fuzzy filter got=%v", got)
	}
	got = filterAndSortSessions(sessions, `login`, sessionSortRecent)
	if len(got) != 2 || got[0].ID != "3" && got[0].ID != "1" {
		t.Fatalf("recent filter got=%v", got)
	}
}

func TestBuildAndFlattenSessionTree(t *testing.T) {
	now := time.Now()
	root := SessionInfo{Path: "/tmp/root.jsonl", Modified: now, Name: "root"}
	child := SessionInfo{Path: "/tmp/child.jsonl", ParentSession: "/tmp/root.jsonl", Modified: now.Add(-time.Hour), Name: "child"}
	other := SessionInfo{Path: "/tmp/other.jsonl", Modified: now.Add(-2 * time.Hour), Name: "other"}
	flat := flattenSessionTree(buildSessionTree([]SessionInfo{child, other, root}))
	if len(flat) != 3 {
		t.Fatalf("flatten len=%d", len(flat))
	}
	if flat[0].Session.Path != root.Path {
		t.Fatalf("first root=%q want %q", flat[0].Session.Path, root.Path)
	}
	if flat[1].Depth != 1 {
		t.Fatalf("child depth=%d want 1", flat[1].Depth)
	}
}

func TestSessionSelectorToggleScopeAndMutation(t *testing.T) {
	now := time.Now()
	current := []SessionInfo{{Path: "/tmp/current.jsonl", Name: "current", Modified: now}, {Path: "/tmp/a.jsonl", Name: "a", Modified: now.Add(-time.Hour)}}
	all := []SessionInfo{{Path: "/tmp/a.jsonl", Name: "a", Modified: now}, {Path: "/tmp/b.jsonl", Name: "b", Modified: now.Add(-time.Hour)}}
	deleted := ""
	renamed := ""
	sel := newSessionSelector(
		func() ([]SessionInfo, error) { return current, nil },
		func() ([]SessionInfo, error) { return all, nil },
		func(path, name string) error { renamed = filepath.Base(path) + ":" + name; return nil },
		func(path string) error { deleted = filepath.Base(path); return nil },
		"/tmp/current.jsonl",
		DefaultKeybindingsManager(),
	)
	if len(sel.filtered) != 1 {
		t.Fatalf("current scope filtered=%d want 1", len(sel.filtered))
	}
	if sel.filtered[0].Session.Path != "/tmp/a.jsonl" {
		t.Fatalf("filtered current session should exclude active path, got %q", sel.filtered[0].Session.Path)
	}
	sel.HandleInput("\t")
	if sel.scope != sessionScopeAll || len(sel.filtered) != 2 {
		t.Fatalf("toggle scope failed scope=%s filtered=%d", sel.scope, len(sel.filtered))
	}
	sel.selected = 1
	sel.HandleInput("\x12") // ctrl+r rename
	if !sel.renameMode {
		t.Fatal("expected rename mode")
	}
	sel.renameInput.SetText("renamed")
	sel.HandleInput("\r")
	if renamed != "b.jsonl:renamed" {
		t.Fatalf("renamed=%q", renamed)
	}
	sel.selected = 1
	sel.HandleInput("\x04") // ctrl+d delete
	if sel.confirmDelete == "" {
		t.Fatal("expected delete confirmation")
	}
	sel.HandleInput("\r")
	if deleted != "b.jsonl" {
		t.Fatalf("deleted=%q", deleted)
	}
}

func TestSessionSelectorRenderEmptyCurrentScopeMatchesUpstreamEmptyStateCopy(t *testing.T) {
	now := time.Now()
	sel := newSessionSelector(
		func() ([]SessionInfo, error) {
			return []SessionInfo{{Path: "/tmp/current.jsonl", Name: "current", Modified: now}}, nil
		},
		func() ([]SessionInfo, error) { return nil, nil },
		nil,
		nil,
		"/tmp/current.jsonl",
		DefaultKeybindingsManager(),
	)

	var plain []string
	for _, line := range sel.Render(100) {
		plain = append(plain, strings.TrimRight(stripANSI(line), " "))
	}
	// Upstream buildBaseLayout plus SessionSelectorHeader (title left, scope,
	// name filter and sort right-aligned) and the empty SessionList.
	right := "◉ Current Folder | ○ All  Name: All  Sort: Threaded"
	title := "Resume Session (Current Folder)"
	want := []string{
		"",
		strings.Repeat("─", 100),
		"",
		title + strings.Repeat(" ", 100-len(title)-len([]rune(right))) + right,
		`tab scope · re:<pattern> regex · "phrase" exact`,
		"ctrl+s sort · ctrl+n named · ctrl+d delete · ctrl+p path (off) · ctrl+r rename",
		"",
		">",
		"",
		"  No sessions in current folder. Press Tab to view all.",
		"",
		strings.Repeat("─", 100),
	}
	if strings.Join(plain, "\n") != strings.Join(want, "\n") {
		t.Fatalf("render =\n%s\nwant\n%s", strings.Join(plain, "\n"), strings.Join(want, "\n"))
	}
}

// Upstream SessionList rows: "› " cursor, name or first message, and the
// message count and age right-aligned; the window is centered on the
// selection (maxVisible 10) with a "(n/total)" row.
func TestSessionSelectorRowsMatchUpstreamLayout(t *testing.T) {
	now := time.Now()
	var sessions []SessionInfo
	for i := range 12 {
		sessions = append(sessions, SessionInfo{
			Path:         fmt.Sprintf("/tmp/s%02d.jsonl", i),
			FirstMessage: fmt.Sprintf("message %02d", i),
			MessageCount: i + 1,
			Modified:     now.Add(-time.Duration(i) * time.Hour),
		})
	}
	sel := newSessionSelector(
		func() ([]SessionInfo, error) { return sessions, nil },
		func() ([]SessionInfo, error) { return sessions, nil },
		nil, nil, "", DefaultKeybindingsManager(),
	)
	sel.toggleSortMode() // recent: a flat list
	var rows []string
	for _, line := range sel.Render(60) {
		if plain := strings.TrimRight(stripANSI(line), " "); strings.Contains(plain, "message ") || strings.HasPrefix(plain, "  (") {
			rows = append(rows, plain)
		}
	}
	if len(rows) != 11 {
		t.Fatalf("rows = %d, want 10 sessions and a scroll row:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	first := "› message 00"
	if want := first + strings.Repeat(" ", 60-len([]rune(first))-len("1 now")) + "1 now"; rows[0] != want {
		t.Fatalf("selected row = %q, want %q", rows[0], want)
	}
	if rows[10] != "  (1/12)" {
		t.Fatalf("scroll row = %q", rows[10])
	}
}

func TestSessionSelectorRenderRenameModeUsesBareInputSurface(t *testing.T) {
	now := time.Now()
	sel := newSessionSelector(
		func() ([]SessionInfo, error) {
			return []SessionInfo{{Path: "/tmp/a.jsonl", Name: "alpha", Modified: now}}, nil
		},
		func() ([]SessionInfo, error) {
			return []SessionInfo{{Path: "/tmp/a.jsonl", Name: "alpha", Modified: now}}, nil
		},
		nil,
		nil,
		"/tmp/current.jsonl",
		DefaultKeybindingsManager(),
	)
	sel.renameMode = true
	sel.renameInput.SetText("alpha")

	lines := sel.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Rename Session") {
		t.Fatalf("rename render missing title:\n%s", joined)
	}
	if !strings.Contains(joined, "> ") || !strings.Contains(joined, "alpha") {
		t.Fatalf("rename render missing bare input line:\n%s", joined)
	}
	if strings.Contains(joined, "Enter: confirm  Esc: cancel") {
		t.Fatalf("rename render still contains legacy text input chrome:\n%s", joined)
	}
	if !strings.Contains(joined, "enter to save · escape/ctrl+c to cancel") {
		t.Fatalf("rename render missing rename hint:\n%s", joined)
	}
}
