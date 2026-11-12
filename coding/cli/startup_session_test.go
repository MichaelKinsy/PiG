package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestResolveCLIResourceFlagsAnchorsPathsToLaunchCWD(t *testing.T) {
	launchCWD := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "absolute")
	got, err := resolveCLIResourceFlags(Args{
		Extensions:      []string{"./ext", absolute},
		Skills:          []string{"skills/demo"},
		PromptTemplates: []string{"prompts/demo.md"},
		Themes:          []string{"themes/demo.json"},
	}, launchCWD)
	if err != nil {
		t.Fatal(err)
	}
	if got.Extensions[0] != filepath.Join(launchCWD, "ext") || got.Extensions[1] != absolute {
		t.Fatalf("Extensions = %v", got.Extensions)
	}
	if got.Skills[0] != filepath.Join(launchCWD, "skills/demo") {
		t.Fatalf("Skills = %v", got.Skills)
	}
	if got.PromptTemplates[0] != filepath.Join(launchCWD, "prompts/demo.md") {
		t.Fatalf("PromptTemplates = %v", got.PromptTemplates)
	}
	if got.Themes[0] != filepath.Join(launchCWD, "themes/demo.json") {
		t.Fatalf("Themes = %v", got.Themes)
	}
}

func TestResolveStartupSessionSelectionUsesSessionCWD(t *testing.T) {
	launchCWD := t.TempDir()
	targetCWD := filepath.Join(launchCWD, "target")
	if err := os.Mkdir(targetCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := writeStartupSession(t, filepath.Join(launchCWD, "sessions"), "selected", targetCWD)

	got, err := resolveStartupSessionSelection(Args{Session: sessionPath}, launchCWD, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.resumePath != sessionPath {
		t.Fatalf("resumePath = %q, want %q", got.resumePath, sessionPath)
	}
	wantTarget := canonicalStartupDir(targetCWD)
	if got.runtimeCWD != wantTarget {
		t.Fatalf("runtimeCWD = %q, want %q", got.runtimeCWD, wantTarget)
	}
	if got.sessionDir != filepath.Dir(sessionPath) {
		t.Fatalf("sessionDir = %q, want %q", got.sessionDir, filepath.Dir(sessionPath))
	}
}

func TestResolveStartupSessionSelectionFindsLocalPrefix(t *testing.T) {
	launchCWD := t.TempDir()
	sessionDir := filepath.Join(launchCWD, "sessions")
	path := writeStartupSession(t, sessionDir, "local-session-123", launchCWD)

	got, err := resolveStartupSessionSelection(Args{Session: "local-sess"}, launchCWD, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.resumePath != path || got.crossProject != nil {
		t.Fatalf("selection = %+v, want local prefix %q", got, path)
	}
}

func TestResolveStartupSessionSelectionFindsGlobalPrefixBeforeRuntime(t *testing.T) {
	launchCWD := t.TempDir()
	otherCWD := t.TempDir()
	sessionDir := filepath.Join(launchCWD, "sessions")
	path := writeStartupSession(t, sessionDir, "global-session-456", otherCWD)

	got, err := resolveStartupSessionSelection(Args{Session: "global-sess"}, launchCWD, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.crossProject == nil || got.crossProject.path != path || got.crossProject.cwd != otherCWD {
		t.Fatalf("crossProject = %+v, want %q from %q", got.crossProject, path, otherCWD)
	}
	if got.resumePath != "" {
		t.Fatalf("resumePath = %q, want confirmation before opening", got.resumePath)
	}
}

func TestConfirmCrossProjectSessionDefaultsNo(t *testing.T) {
	var output strings.Builder
	confirmed, err := confirmCrossProjectSession(strings.NewReader("\n"), &output, "/other")
	if err != nil {
		t.Fatal(err)
	}
	if confirmed {
		t.Fatal("empty answer confirmed cross-project fork")
	}
	if !strings.Contains(output.String(), "Session found in different project: /other") || !strings.Contains(output.String(), "[y/N]") {
		t.Fatalf("prompt = %q", output.String())
	}
}

func TestConfirmCrossProjectSessionAcceptsYes(t *testing.T) {
	confirmed, err := confirmCrossProjectSession(strings.NewReader("yes\n"), io.Discard, "/other")
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("yes did not confirm cross-project fork")
	}
}

func TestResolveStartupSessionSelectionReportsMissingStoredCWD(t *testing.T) {
	launchCWD := t.TempDir()
	missingCWD := filepath.Join(launchCWD, "removed")
	sessionPath := writeStartupSession(t, filepath.Join(launchCWD, "sessions"), "selected", missingCWD)

	got, err := resolveStartupSessionSelection(Args{Session: sessionPath}, launchCWD, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.missingCWD == nil {
		t.Fatal("missingCWD issue is nil")
	}
	wantLaunch := canonicalStartupDir(launchCWD)
	wantMissing := canonicalStartupDir(missingCWD)
	if got.missingCWD.sessionFile != sessionPath || got.missingCWD.storedCWD != wantMissing || got.missingCWD.fallbackCWD != wantLaunch {
		t.Fatalf("missingCWD = %+v", got.missingCWD)
	}
	if got.runtimeCWD != wantLaunch {
		t.Fatalf("runtimeCWD = %q, want unchanged launch cwd %q", got.runtimeCWD, wantLaunch)
	}
}

// Pi main.ts (#11060): Continue runs the Session in the current cwd and stores nothing; Fork copies it into the current cwd as a new
// Session with a new id and the original as its parent, leaves the original unchanged, and opens the copy.
func TestApplyMissingCWDChoiceContinuesOrForks(t *testing.T) {
	setup := func(t *testing.T) (startupSessionSelection, string, string, string) {
		launchCWD := t.TempDir()
		sessionDir := filepath.Join(launchCWD, "current-sessions")
		path := writeStartupSession(t, filepath.Join(launchCWD, "old-sessions"), "orphan", filepath.Join(launchCWD, "removed"))
		got, err := resolveStartupSessionSelection(Args{Session: path}, launchCWD, "")
		if err != nil || got.missingCWD == nil {
			t.Fatalf("selection = %+v, %v", got, err)
		}
		return got, path, canonicalStartupDir(launchCWD), sessionDir
	}
	t.Run("continue", func(t *testing.T) {
		got, path, launch, sessionDir := setup(t)
		var flags Args
		if err := got.applyMissingCWDChoice(codingagent.MissingSessionCwdContinue, &flags, sessionDir); err != nil {
			t.Fatal(err)
		}
		if got.missingCWD != nil || got.runtimeCWD != launch || got.forkPath != "" || got.resumePath != path || flags.sessionCwdOverride == nil || *flags.sessionCwdOverride != launch {
			t.Fatalf("selection = %+v, override = %v", got, flags.sessionCwdOverride)
		}
		if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
			t.Fatalf("continue wrote into the session dir: %v", err)
		}
	})
	t.Run("fork", func(t *testing.T) {
		got, path, launch, sessionDir := setup(t)
		before, _ := os.ReadFile(path)
		var flags Args
		if err := got.applyMissingCWDChoice(codingagent.MissingSessionCwdFork, &flags, sessionDir); err != nil {
			t.Fatal(err)
		}
		if got.missingCWD != nil || got.runtimeCWD != launch || got.resumePath != "" || flags.sessionCwdOverride != nil {
			t.Fatalf("selection = %+v", got)
		}
		if filepath.Dir(got.forkPath) != sessionDir || got.sessionDir != sessionDir {
			t.Fatalf("fork %q / session dir %q, want the current session dir %q", got.forkPath, got.sessionDir, sessionDir)
		}
		header, err := codingagent.ReadSessionHeader(got.forkPath)
		if err != nil || header.CWD != launch || header.ID == "orphan" || header.ParentSession != path {
			t.Fatalf("fork header = %+v, %v", header, err)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
			t.Fatal("the original session changed")
		}
	})
}

// Pi main.ts:707-721 checks every session manager that has a session file, a new Session's included: SessionManager.create gives
// it a file (session-manager.ts:1079-1082) and its cwd is the launch directory, so a launch directory that no longer exists is a
// missing session cwd (session-cwd.ts:14-33) and not a failure of the runtime that builds the Session later.
func TestResolveStartupSessionSelectionReportsAMissingLaunchCWDForANewSession(t *testing.T) {
	for name, flags := range map[string]Args{
		"new session":           {},
		"new session with id":   {SessionID: "fresh-id"},
		"continue with nothing": {Continue: true},
		"explicit new path":     {Session: "fresh.jsonl"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			launchCWD := filepath.Join(root, "removed")
			sessionDir := filepath.Join(root, "sessions")
			got, err := resolveStartupSessionSelection(flags, launchCWD, sessionDir)
			if err != nil {
				t.Fatal(err)
			}
			issue := got.missingCWD
			if issue == nil {
				t.Fatal("missingCWD issue is nil")
			}
			want := canonicalStartupDir(launchCWD)
			if issue.storedCWD != want || issue.fallbackCWD != want {
				t.Errorf("missingCWD = %+v, want stored and fallback %q", issue, want)
			}
			if filepath.Dir(issue.sessionFile) != sessionDir && flags.Session == "" || !strings.HasSuffix(issue.sessionFile, ".jsonl") {
				t.Errorf("session file = %q, want a .jsonl file in %q", issue.sessionFile, sessionDir)
			}
			if got.resumePath != issue.sessionFile {
				t.Errorf("resumePath = %q, want the session file %q for the continue-in-current-cwd choice", got.resumePath, issue.sessionFile)
			}
			message := issue.Error()
			if !strings.HasPrefix(message, "Stored session working directory does not exist: "+want+"\nSession file: "+issue.sessionFile+"\nCurrent working directory: "+want) {
				t.Errorf("message = %q", message)
			}
		})
	}
}

func TestResolveStartupSessionSelectionKeepsAnExistingLaunchCWDForANewSession(t *testing.T) {
	root := t.TempDir()
	for name, flags := range map[string]Args{"new session": {}, "no session": {NoSession: true}, "help": {Help: true}} {
		got, err := resolveStartupSessionSelection(flags, root, filepath.Join(root, "sessions"))
		if err != nil || got.missingCWD != nil || got.resumePath != "" {
			t.Errorf("%s: missingCWD = %+v, resumePath = %q, err = %v", name, got.missingCWD, got.resumePath, err)
		}
	}
	removed := filepath.Join(root, "removed")
	for name, flags := range map[string]Args{"no session": {NoSession: true}, "help": {Help: true}, "list models": {ListModelsAll: true}} {
		got, err := resolveStartupSessionSelection(flags, removed, filepath.Join(root, "sessions"))
		if err != nil || got.missingCWD != nil {
			t.Errorf("%s in a missing cwd: missingCWD = %+v, err = %v (Pi's session manager is in memory and has no session file)", name, got.missingCWD, err)
		}
	}
}

func TestResolveStartupSessionSelectionNoSessionDoesNotReadSelectedFile(t *testing.T) {
	launchCWD := t.TempDir()
	got, err := resolveStartupSessionSelection(Args{NoSession: true, Session: "missing.jsonl"}, launchCWD, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.runtimeCWD != canonicalStartupDir(launchCWD) || got.resumePath != "" {
		t.Fatalf("selection = %+v, want launch cwd with no resume", got)
	}
}

func TestResolveStartupSessionSelectionForkStaysInLaunchCWD(t *testing.T) {
	launchCWD := t.TempDir()
	sourceCWD := filepath.Join(launchCWD, "source")
	if err := os.Mkdir(sourceCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionDir := filepath.Join(launchCWD, "sessions")
	source := writeStartupSession(t, sessionDir, "source-session", sourceCWD)

	got, err := resolveStartupSessionSelection(Args{Fork: source}, launchCWD, sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if got.forkPath == "" {
		t.Fatal("forkPath is empty")
	}
	wantLaunch := canonicalStartupDir(launchCWD)
	if got.runtimeCWD != wantLaunch {
		t.Fatalf("runtimeCWD = %q, want launch cwd %q", got.runtimeCWD, wantLaunch)
	}
}

func TestReadSessionCWDReadsOnlyValidatedHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	content := "{\"type\":\"session\",\"version\":3,\"id\":\"s1\",\"cwd\":\"/target/project\"}\n{not valid trailing json}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readSessionCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/target/project" {
		t.Fatalf("readSessionCWD() = %q, want /target/project", got)
	}
}

func TestReadSessionCWDRejectsNonSessionHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"message\",\"cwd\":\"/target\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSessionCWD(path); err == nil {
		t.Fatal("readSessionCWD() succeeded for non-session header")
	}
}

func BenchmarkReadSessionCWDDoesNotScaleWithTranscript(b *testing.B) {
	path := filepath.Join(b.TempDir(), "large.jsonl")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.WriteString("{\"type\":\"session\",\"version\":3,\"id\":\"large\",\"cwd\":\"/target/project\"}\n"); err != nil {
		b.Fatal(err)
	}
	line := make([]byte, 1025)
	line[len(line)-1] = '\n'
	for range 8192 {
		if _, err := f.Write(line); err != nil {
			b.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := readSessionCWD(path); err != nil {
			b.Fatal(err)
		}
	}
}

func writeStartupSession(t *testing.T, dir, id, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	content := fmt.Sprintf("{\"type\":\"session\",\"version\":3,\"id\":%q,\"timestamp\":\"2026-08-04T00:00:00Z\",\"cwd\":%q}\n", id, cwd)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Upstream reads the session file with no line cap, so a header line over
// 16 MiB still yields its cwd.
func TestReadSessionCWDReadsHeaderOver16MiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header := "{\"type\":\"session\",\"id\":\"s1\",\"cwd\":\"/target/project\",\"pad\":\"" + strings.Repeat("x", 17<<20) + "\"}\n"
	if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readSessionCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/target/project" {
		t.Fatalf("readSessionCWD() = %q, want /target/project", got)
	}
}
