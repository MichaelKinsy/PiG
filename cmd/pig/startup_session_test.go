package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCLIResourceFlagsAnchorsPathsToLaunchCWD(t *testing.T) {
	launchCWD := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "absolute")
	got := resolveCLIResourceFlags(CLIFlags{
		Extensions:      []string{"./ext", absolute},
		Skills:          []string{"skills/demo"},
		PromptTemplates: []string{"prompts/demo.md"},
		Themes:          []string{"themes/demo.json"},
	}, launchCWD)
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

	got, err := resolveStartupSessionSelection(CLIFlags{Session: sessionPath}, launchCWD, "")
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

	got, err := resolveStartupSessionSelection(CLIFlags{Session: "local-sess"}, launchCWD, sessionDir)
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

	got, err := resolveStartupSessionSelection(CLIFlags{Session: "global-sess"}, launchCWD, sessionDir)
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

	got, err := resolveStartupSessionSelection(CLIFlags{Session: sessionPath}, launchCWD, "")
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

func TestResolveStartupSessionSelectionNoSessionDoesNotReadSelectedFile(t *testing.T) {
	launchCWD := t.TempDir()
	got, err := resolveStartupSessionSelection(CLIFlags{NoSession: true, Session: "missing.jsonl"}, launchCWD, "")
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

	got, err := resolveStartupSessionSelection(CLIFlags{Fork: source}, launchCWD, sessionDir)
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
