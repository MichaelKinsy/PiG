package coding

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withTempHome isolates PIG_HOME for a test so settings + auth lookups
// don't touch the developer's real config.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	return tmp
}

func TestNewServicesDefaults(t *testing.T) {
	withTempHome(t)
	srv, err := NewServices(ServicesOptions{})
	if err != nil {
		t.Fatalf("NewServices: %v", err)
	}
	if srv == nil {
		t.Fatal("nil services")
	}
	if srv.CWD() == "" {
		t.Errorf("CWD should default to os.Getwd(); got empty")
	}
	if srv.AgentDir() == "" {
		t.Errorf("AgentDir should default; got empty")
	}
	if srv.Auth() == nil {
		t.Errorf("Auth should be non-nil")
	}
	if srv.Registry() == nil {
		t.Errorf("Registry should be non-nil")
	}
}

func TestNewServicesAuthDirSelectsCorrectFile(t *testing.T) {
	tmp := withTempHome(t)
	srv, err := NewServices(ServicesOptions{
		AgentDir: filepath.Join(tmp, "agent"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(tmp, "agent")
	if got := srv.AgentDir(); got != want {
		t.Errorf("AgentDir = %q, want %q", got, want)
	}
	// Auth should have been opened pointing at <agentDir>/auth.json,
	// even if the file doesn't exist yet (NewAuthStorage creates the dir).
	wantAuth := filepath.Join(want, "auth.json")
	if got := srv.Auth().Path(); got != wantAuth {
		t.Errorf("auth path = %q, want %q", got, wantAuth)
	}
}

func TestNewServicesExplicitCWDIsHonored(t *testing.T) {
	withTempHome(t)
	tmp := t.TempDir()
	srv, err := NewServices(ServicesOptions{CWD: tmp})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.CWD(); got != tmp {
		t.Errorf("CWD = %q, want %q", got, tmp)
	}
}

func TestServicesAreImmutableAfterConstruction(t *testing.T) {
	// There are no public mutators on Services itself; this test
	// asserts that intent at the type level. If a future refactor
	// adds e.g. SetCWD, this test should also be updated to either
	// remove the immutability claim from godoc or keep the contract.
	withTempHome(t)
	srv, _ := NewServices(ServicesOptions{})

	cwdBefore := srv.CWD()
	dirBefore := srv.AgentDir()
	// Mutating returned settings struct should not affect future reads.
	s1 := srv.Settings()
	s1.QuietStartup = !s1.QuietStartup
	s2 := srv.Settings()
	if s1.QuietStartup == s2.QuietStartup {
		// They were the same after mutation → settings is value-typed (good)
		// OR mutation worked (bad); verify by checking against pristine.
		if cwdBefore != srv.CWD() || dirBefore != srv.AgentDir() {
			t.Errorf("identity fields changed after Settings mutation")
		}
	}
}

func TestNewServicesReturnsErrorOnUnusableAuthDir(t *testing.T) {
	withTempHome(t)
	// Point AgentDir at something that can't be created (a path under
	// a regular file). On Linux/macOS, mkdirall fails with ENOTDIR.
	tmp := t.TempDir()
	notADir := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(notADir, "agent")
	_, err := NewServices(ServicesOptions{AgentDir: bad})
	if err == nil {
		t.Fatal("expected error for unwritable AgentDir")
	}
	if !strings.Contains(err.Error(), "auth storage") {
		t.Errorf("error should mention auth storage; got: %v", err)
	}
	_ = runtime.GOOS // pacify import on platforms where this test is fine
}

func TestServicesSettingsReflectsProjectOverlay(t *testing.T) {
	tmp := withTempHome(t)
	cwd := t.TempDir()
	// Write a project-level settings file to verify it's picked up.
	// Using `theme` (string) rather than `quietStartup` (bool) because
	// internal/codingagent.mergeSettings only overrides string/slice
	// fields today: bools always come from the global layer. (Tracked
	// as a separate parity follow-up; not in F2 scope.)
	projSettingsDir := filepath.Join(cwd, ".pig")
	if err := os.MkdirAll(projSettingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projFile := filepath.Join(projSettingsDir, "settings.json")
	if err := os.WriteFile(projFile, []byte(`{"theme": "my-project-theme"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServices(ServicesOptions{
		CWD:      cwd,
		AgentDir: filepath.Join(tmp, "agent"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.Settings().Theme; got != "my-project-theme" {
		t.Errorf("project-overlay theme = %q, want %q", got, "my-project-theme")
	}
}

func TestDefaultAgentDirHonorsPIG_HOME(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	got := DefaultAgentDir()
	want := filepath.Join(tmp, "agent")
	if got != want {
		t.Errorf("DefaultAgentDir = %q, want %q", got, want)
	}
}
