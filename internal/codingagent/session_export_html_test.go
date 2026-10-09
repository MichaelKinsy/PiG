//go:build !pig_strip_export_html

package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

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
		{"not written yet", func(t *testing.T) *Session {
			// Setup entries alone do not create the file (session-manager.ts:1172-1185).
			session := NewSession("unwritten", t.TempDir())
			session.SetPath(filepath.Join(t.TempDir(), "session.jsonl"))
			if _, err := session.AppendThinkingLevelChange("off"); err != nil {
				t.Fatal(err)
			}
			return session
		}, "Failed to export session: Nothing to export yet - start a conversation first"},
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
