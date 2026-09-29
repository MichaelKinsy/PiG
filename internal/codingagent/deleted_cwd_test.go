package codingagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Node's path.resolve throws ENOENT from process.cwd() for a relative path when
// the working directory was removed, so each Pi resolvePath port that can
// report an error reports it; a display or best-effort port keeps the input.

func TestSessionFileOpenFailsWithoutAWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	sm := NewSessionManagerWithDir("/work", filepath.Join(t.TempDir(), "sessions"))
	if s, err := sm.Open("session.jsonl"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open(relative) = %v, %v; Node's path.resolve throws ENOENT", s, err)
	}
	if s, err := sm.Load("session.jsonl"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load(relative) = %v, %v; Node's path.resolve throws ENOENT", s, err)
	}
	if s, err := loadSessionFile("session.jsonl"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "session: resolve session.jsonl") {
		t.Fatalf("loadSessionFile(relative) = %v, %v; want the resolve error", s, err)
	}
	if s, err := sm.ForkFromFile("session.jsonl"); !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "fork: bad path") {
		t.Fatalf("ForkFromFile(relative) = %v, %v; want the resolve error", s, err)
	}
}

func TestDeleteListedSessionRefusesWithoutAWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	sm := NewSessionManagerWithDir("/work", "sessions")
	result := sm.deleteListedSession("sessions/a.jsonl")
	if result.ok || !strings.Contains(result.error, "bad path") {
		t.Fatalf("deleteListedSession = %+v; want a refusal that names the unresolved path", result)
	}
	if err := sm.DeleteSession("sessions/a.jsonl"); err == nil || !strings.Contains(err.Error(), "bad path") {
		t.Fatalf("DeleteSession = %v; want the resolve failure", err)
	}
}

func TestResourcePathWithinFailsClosedWithoutAWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if resourcePathWithin("user/extensions/evil.ts", "user") {
		t.Fatal("a path that cannot be resolved must not count as inside the base")
	}
	if !resourcePathWithin("/user/extensions/ok.ts", "/user") {
		t.Fatal("absolute paths never read the working directory")
	}
}

func TestBestEffortPathPortsKeepTheirInputWithoutAWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if got := CanonicalizePath("rel/dir"); got != "rel/dir" {
		t.Errorf("CanonicalizePath = %q, want the input", got)
	}
	if got := formatCwdForFooter("rel/dir", "home"); got != "rel/dir" {
		t.Errorf("formatCwdForFooter = %q, want the input", got)
	}
	if got := normalizeTrustCwd("rel/dir"); got != "rel/dir" {
		t.Errorf("normalizeTrustCwd = %q, want the input", got)
	}
	if got := NewProjectTrustStore("agent").trustPath; got != filepath.Join("agent", "trust.json") {
		t.Errorf("trust store path = %q, want the input joined", got)
	}
	if got := GetCwdRelativePath("a.md", "rel"); got != "" {
		t.Errorf("GetCwdRelativePath = %q, want \"\" (unresolvable is not inside cwd)", got)
	}
	if got := canonicalDir("rel"); got != "rel" {
		t.Errorf("canonicalDir = %q, want the cleaned input", got)
	}
	if got := absCleanDir("rel/../rel2"); got != "rel2" {
		t.Errorf("absCleanDir = %q, want the cleaned input", got)
	}
	if got := LoadProjectContextFiles("rel", ""); got != nil {
		t.Errorf("LoadProjectContextFiles = %v", got)
	}
}
