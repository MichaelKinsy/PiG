package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestProjectTrustStore_RoundTripAndInheritance(t *testing.T) {
	agentDir := t.TempDir()
	project := t.TempDir()
	child := filepath.Join(project, "sub", "deep")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}

	store := NewProjectTrustStore(agentDir)
	got, err := store.Get(project)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Get(project) before set = %v, want nil", *got)
	}

	if err := store.Set(project, new(true)); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(project)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !*got {
		t.Fatalf("Get(project) = %v, want true", got)
	}
	entry, err := store.GetEntry(child)
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || !entry.Decision || entry.Path != GetProjectTrustPath(project) {
		t.Fatalf("GetEntry(child) = %+v, want inherited true from project", entry)
	}

	if _, err := os.Stat(filepath.Join(agentDir, "trust.json")); err != nil {
		t.Fatalf("trust.json not written: %v", err)
	}

	if err := store.Set(project, nil); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(child)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Get(child) after clear = %v, want nil", *got)
	}
}

func TestProjectTrustStore_PreservesUnrelatedNull(t *testing.T) {
	agentDir := t.TempDir()
	trustPath := filepath.Join(agentDir, "trust.json")
	if err := os.WriteFile(trustPath, []byte("{\n  \"/legacy\": null\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewProjectTrustStore(agentDir)
	if err := store.Set("/new", new(true)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"/legacy": null`) {
		t.Fatalf("unrelated null entry was not preserved:\n%s", data)
	}
}

func TestProjectTrustStore_MalformedDataSurfacesError(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "invalid JSON", data: `{`, want: "Failed to read trust store"},
		{name: "array", data: `[]`, want: "expected an object"},
		{name: "invalid value", data: `{"/project":"yes"}`, want: `value for "/project" must be true, false, or null`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentDir, "trust.json"), []byte(tc.data), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := NewProjectTrustStore(agentDir).Get(t.TempDir())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Get() error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestProjectTrustStore_ConcurrentUpdatesWaitForInProcessWriter(t *testing.T) {
	store := NewProjectTrustStore(t.TempDir())
	entered := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- store.withLock(func() error {
			close(entered)
			time.Sleep(250 * time.Millisecond)
			return nil
		})
	}()
	<-entered

	if err := store.Set(t.TempDir(), new(true)); err != nil {
		t.Fatalf("Set while an in-process writer held the lock: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatalf("first writer: %v", err)
	}
}

func TestProjectTrustStore_ConcurrentUpdatesDoNotLoseEntries(t *testing.T) {
	store := NewProjectTrustStore(t.TempDir())
	root := t.TempDir()
	const count = 8
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := range count {
		wg.Go(func() {
			errs <- store.Set(filepath.Join(root, string(rune('a'+i))), new(true))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range count {
		decision, err := store.Get(filepath.Join(root, string(rune('a'+i))))
		if err != nil {
			t.Fatal(err)
		}
		if decision == nil || !*decision {
			t.Fatalf("entry %d missing after concurrent updates", i)
		}
	}
}

func TestProjectTrustStore_CanonicalSymlinkIdentity(t *testing.T) {
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	link := filepath.Join(parent, "project")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	testenv.Symlink(t, first, link)

	store := NewProjectTrustStore(t.TempDir())
	if err := store.Set(link, new(true)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, second, link)
	decision, err := store.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	if decision != nil {
		t.Fatalf("trust for old symlink target leaked to new target: %v", *decision)
	}
}

func TestGetProjectTrustOptions(t *testing.T) {
	project := t.TempDir()
	opts := GetProjectTrustOptions(project, false)
	var hasTrust, hasDoNotTrust bool
	for _, option := range opts {
		if option.Label == "Trust" && option.Trusted && len(option.Updates) == 1 && option.Updates[0].Decision != nil && *option.Updates[0].Decision {
			hasTrust = true
		}
		if option.Label == "Do not trust" && !option.Trusted && len(option.Updates) == 1 && option.Updates[0].Decision != nil && !*option.Updates[0].Decision {
			hasDoNotTrust = true
		}
	}
	if !hasTrust || !hasDoNotTrust {
		t.Fatalf("options missing Trust/Do not trust: %+v", opts)
	}

	withSession := GetProjectTrustOptions(project, true)
	if len(withSession) <= len(opts) {
		t.Fatalf("includeSessionOnly=true should add options: %d vs %d", len(withSession), len(opts))
	}
}

func TestHasTrustRequiringProjectResources_ExactProjectResources(t *testing.T) {
	for _, entry := range []string{
		"settings.json", "extensions", "skills", "prompts", "themes", "SYSTEM.md", "APPEND_SYSTEM.md",
	} {
		t.Run(entry, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, CONFIG_DIR_NAME, entry)
			if filepath.Ext(entry) == "" {
				if err := os.MkdirAll(path, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !HasTrustRequiringProjectResources(project) {
				t.Fatalf("%s should require project trust", entry)
			}
		})
	}
}

func TestHasTrustRequiringProjectResources_IgnoresUnrelatedConfigEntries(t *testing.T) {
	for _, entry := range []string{"", "unrelated.json", "state"} {
		t.Run(entry, func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, CONFIG_DIR_NAME, entry)
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if HasTrustRequiringProjectResources(project) {
				t.Fatalf("config entry %q should not require project trust", entry)
			}
		})
	}
}

func TestHasTrustRequiringProjectResources_AncestorAgentsSkillsExcludesUserHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userSkills := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(userSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	if HasTrustRequiringProjectResources(home) {
		t.Fatal("user ~/.agents/skills must not require project trust")
	}

	project := filepath.Join(home, "work", "project")
	if err := os.MkdirAll(filepath.Join(home, "work", ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if !HasTrustRequiringProjectResources(project) {
		t.Fatal("ancestor project .agents/skills should require project trust")
	}
}
