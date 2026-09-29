package coding

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

func importTestHeader(id, cwd string) string {
	return `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-09-28T07:00:00.000Z","cwd":"` + filepath.ToSlash(cwd) + `"}` + "\n"
}

func writeImportTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readImportTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// agent-session-runtime.test.ts:216-247 "preserves an existing session when
// importing a file with the same name". importFromJsonl
// (agent-session-runtime.ts:372-389) takes the first free name-N and copies
// with COPYFILE_EXCL, so neither the stored session nor an earlier -1 import
// changes.
func TestImportFromJsonlPreservesSameNamedSession(t *testing.T) {
	sessionDir := t.TempDir()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	cwd := t.TempDir()
	storedPath := filepath.Join(sessionDir, "collision.jsonl")
	earlierPath := filepath.Join(sessionDir, "collision-1.jsonl")
	importPath := filepath.Join(t.TempDir(), "import", "collision.jsonl")
	stored, earlier, imported := importTestHeader("stored", cwd), importTestHeader("earlier", cwd), importTestHeader("imported", cwd)
	writeImportTestFile(t, storedPath, stored)
	writeImportTestFile(t, earlierPath, earlier)
	writeImportTestFile(t, importPath, imported)

	result, err := sess.ImportFromJsonl(t.Context(), importPath, "")
	if err != nil || result.Cancelled {
		t.Fatalf("import = %+v, %v", result, err)
	}
	if got := readImportTestFile(t, storedPath); got != stored {
		t.Fatalf("stored session changed:\n%s", got)
	}
	if got := readImportTestFile(t, earlierPath); got != earlier {
		t.Fatalf("earlier import changed:\n%s", got)
	}
	if want := filepath.Join(sessionDir, "collision-2.jsonl"); sess.Path() != want {
		t.Fatalf("session file = %q, want %q", sess.Path(), want)
	}
	if got := readImportTestFile(t, sess.Path()); !strings.Contains(got, `"id":"imported"`) {
		t.Fatalf("imported copy = %q", got)
	}
}

// importFromJsonl emits session_before_switch with the chosen destination
// before copying (agent-session-runtime.ts:381-389). A cancel copies nothing
// and keeps the session; otherwise the switch is a resume.
func TestImportFromJsonlSwitchesAfterBeforeSwitch(t *testing.T) {
	var trace []string
	cancel := true
	runner := inproc.NewRunner([]extension.Extension{{Path: "/import", Handlers: map[string][]extension.HandlerFn{
		"session_before_switch": {func(args ...any) (any, error) {
			event := args[0].(extension.SessionBeforeSwitchEvent)
			trace = append(trace, "before:"+event.Reason+":"+filepath.Base(event.TargetSessionFile))
			return map[string]any{"cancel": cancel}, nil
		}},
		"session_shutdown": {func(args ...any) (any, error) {
			trace = append(trace, "shutdown:"+args[0].(extension.SessionShutdownEvent).Reason)
			return nil, nil
		}},
		"session_start": {func(args ...any) (any, error) {
			trace = append(trace, "start:"+args[0].(extension.SessionStartEvent).Reason)
			return nil, nil
		}},
	}}}, t.TempDir())
	sessionDir := t.TempDir()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner, SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	cwd := t.TempDir()
	writeImportTestFile(t, filepath.Join(sessionDir, "collision.jsonl"), importTestHeader("stored", cwd))
	importPath := filepath.Join(t.TempDir(), "collision.jsonl")
	writeImportTestFile(t, importPath, importTestHeader("imported", cwd))
	originalID := sess.ID()

	result, err := sess.ImportFromJsonl(t.Context(), importPath, "")
	if err != nil || !result.Cancelled || sess.ID() != originalID {
		t.Fatalf("cancelled import = %+v, %v, id=%s", result, err, sess.ID())
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "collision-1.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("cancelled import copied the file: %v", err)
	}
	if !slices.Equal(trace, []string{"before:resume:collision-1.jsonl"}) {
		t.Fatalf("cancel trace = %v", trace)
	}

	cancel, trace = false, nil
	result, err = sess.ImportFromJsonl(t.Context(), importPath, "")
	if err != nil || result.Cancelled || sess.ID() != "imported" {
		t.Fatalf("import = %+v, %v, id=%s", result, err, sess.ID())
	}
	if !slices.Equal(trace, []string{"before:resume:collision-1.jsonl", "shutdown:resume", "start:resume"}) {
		t.Fatalf("import trace = %v", trace)
	}
}

// A source already in the session directory is opened in place
// (agent-session-runtime.ts:373 and 387-389): no copy, no suffix.
func TestImportFromJsonlOpensStoredSourceInPlace(t *testing.T) {
	sessionDir := t.TempDir()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	storedPath := filepath.Join(sessionDir, "stored.jsonl")
	writeImportTestFile(t, storedPath, importTestHeader("stored", t.TempDir()))

	if result, err := sess.ImportFromJsonl(t.Context(), storedPath, ""); err != nil || result.Cancelled {
		t.Fatalf("import = %+v, %v", result, err)
	}
	if sess.Path() != storedPath || sess.ID() != "stored" {
		t.Fatalf("session = %q (%s), want %q", sess.Path(), sess.ID(), storedPath)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "stored-1.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("stored source was copied: %v", err)
	}
}

// A missing source fails with SessionImportFileNotFoundError naming the
// resolved path (agent-session-runtime.ts:362-365).
func TestImportFromJsonlReportsMissingSource(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	missing := filepath.Join(t.TempDir(), "missing-session.jsonl")
	_, err = sess.ImportFromJsonl(t.Context(), missing, "")
	notFound, ok := errors.AsType[*icodingagent.SessionImportFileNotFoundError](err)
	if !ok || notFound.FilePath != missing || err.Error() != "File not found: "+missing {
		t.Fatalf("err = %v", err)
	}
}

// A stored working directory that no longer exists fails after the copy
// with MissingSessionCwdError (agent-session-runtime.ts:386-392). The retry
// with the offered cwd opens that copy's successor, as upstream would.
func TestImportFromJsonlMissingCwdNeedsOverride(t *testing.T) {
	sessionDir := t.TempDir()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SessionDir: sessionDir})
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, sess)
	gone := filepath.Join(t.TempDir(), "gone")
	importPath := filepath.Join(t.TempDir(), "moved.jsonl")
	writeImportTestFile(t, importPath, importTestHeader("moved", gone))

	_, err = sess.ImportFromJsonl(t.Context(), importPath, "")
	if _, ok := errors.AsType[*icodingagent.MissingSessionCwdError](err); !ok {
		t.Fatalf("err = %v, want MissingSessionCwdError", err)
	}
	if _, statErr := os.Stat(filepath.Join(sessionDir, "moved.jsonl")); statErr != nil {
		t.Fatalf("the copy precedes the cwd check upstream: %v", statErr)
	}
	override := t.TempDir()
	if result, err := sess.ImportFromJsonl(t.Context(), importPath, override); err != nil || result.Cancelled {
		t.Fatalf("import with override = %+v, %v", result, err)
	}
	if want := filepath.Join(sessionDir, "moved-1.jsonl"); sess.Path() != want || sess.Inner().CWD() != override {
		t.Fatalf("session = %q cwd %q, want %q cwd %q", sess.Path(), sess.Inner().CWD(), want, override)
	}
}
