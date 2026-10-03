package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func persistReadonlyIDSession(t *testing.T, session *codingagent.Session, content string) {
	t.Helper()
	for _, message := range []agent.AgentMessage{
		// The raw carrier preserves the upstream fixture's string-valued user content.
		{Custom: map[string]any{"role": "user", "content": content, "timestamp": time.Now().UnixMilli()}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "persisted"}}, API: "anthropic-messages", Provider: "anthropic", ModelID: "test", Usage: &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}},
	} {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
}

// Go splits createSessionManager between startup selection and Runtime.New. Drive both production stages, including the same option conversion main uses.
func openReadonlyIDSession(t *testing.T, flags CLIFlags, cwd, dir string) (*coding.Session, error) {
	t.Helper()
	selection, err := resolveStartupSessionSelection(flags, cwd, dir)
	if err != nil {
		return nil, err
	}
	services, err := coding.NewServices(coding.ServicesOptions{CWD: selection.runtimeCWD, AgentDir: filepath.Join(t.TempDir(), "agent")})
	if err != nil {
		return nil, err
	}
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	options := selection.startOptions(flags)
	options.SkipBuiltinTools = true
	session, err := runtime.New(options)
	if err != nil {
		return nil, err
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() {
		}
	}()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		<-drained
	})
	return session, nil
}

func readonlyIDFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	return canonicalStartupDir(cwd), filepath.Join(root, "sessions")
}

// Upstream 0.99.1 changes only how its harness starts the CLI: `--import` receives the source resolver as a file URL (pathToFileURL) instead of a path
// (.upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:23-24). The Go test starts the compiled binary and has no Node loader flag, so that substitution has no Go
// counterpart: the inputs and expectations of every case are unchanged.
func TestUpstreamSessionIDReadonly(t *testing.T) {
	observed := []any{}
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:117
	t.Run("does not persist a custom ID for metadata commands", func(t *testing.T) {
		cwd, _ := readonlyIDFixture(t)
		agentDir := filepath.Join(t.TempDir(), "agent")
		cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), "--session-id", "read-only-help", "--help")
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("help exit != 0: %v: %s", err, output)
		}
		persisted := false
		if err := filepath.WalkDir(filepath.Join(agentDir, "sessions"), func(path string, entry os.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			session, err := codingagent.NewSessionManagerWithDir(cwd, filepath.Dir(path)).Load(path)
			if err == nil && session.ID() == "read-only-help" {
				persisted = true
				t.Error("metadata command persisted custom ID")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		observed = append(observed, []any{cmd.ProcessState.ExitCode(), persisted})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:124
	t.Run("creates missing IDs and reopens existing IDs in process", func(t *testing.T) {
		cwd, dir := readonlyIDFixture(t)
		previous := os.Stderr
		capture, err := os.CreateTemp(t.TempDir(), "stderr")
		if err != nil {
			t.Fatal(err)
		}
		os.Stderr = capture
		t.Cleanup(func() {
			os.Stderr = previous
			if err := capture.Close(); err != nil {
				t.Error(err)
			}
		})
		readonly, err := openReadonlyIDSession(t, CLIFlags{SessionID: "read-only", Help: true}, cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		if readonly.ID() != "read-only" || readonly.Path() != "" {
			t.Fatalf("readonly ID=%q path=%q", readonly.ID(), readonly.Path())
		}
		created, err := openReadonlyIDSession(t, CLIFlags{SessionID: "persisted-id"}, cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		persistReadonlyIDSession(t, created.Inner(), "persist me")
		diagnostic, err := os.ReadFile(capture.Name())
		if err != nil {
			t.Fatal(err)
		}
		warned := strings.Contains(string(diagnostic), "creating a new session")
		if !warned {
			t.Fatalf("missing creation notice: %q", diagnostic)
		}
		if err := capture.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := capture.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		reopened, err := openReadonlyIDSession(t, CLIFlags{SessionID: "persisted-id"}, cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Path() != created.Path() {
			t.Fatalf("reopened %q != created %q", reopened.Path(), created.Path())
		}
		diagnostic, err = os.ReadFile(capture.Name())
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostic) != 0 {
			t.Fatalf("unexpected reopen diagnostic: %q", diagnostic)
		}
		observed = append(observed, []any{readonly.ID(), readonly.Path() == "", warned, reopened.Path() == created.Path(), len(diagnostic)})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:162
	t.Run("looks up exact IDs without building full session listings", func(t *testing.T) {
		cwd, dir := readonlyIDFixture(t)
		unrelated, err := newSessionManagerWithDir(cwd, dir).Create("unrelated-id", "")
		if err != nil {
			t.Fatal(err)
		}
		persistReadonlyIDSession(t, unrelated, "large transcript contents must not be loaded")
		original := listStartupSessions
		calls := 0
		listStartupSessions = func(*codingagent.SessionManager) ([]codingagent.SessionInfo, error) {
			calls++
			return nil, errors.New("unexpected full listing")
		}
		t.Cleanup(func() { listStartupSessions = original })
		created, err := openReadonlyIDSession(t, CLIFlags{SessionID: "fresh-id"}, cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		if created.ID() != "fresh-id" || calls != 0 {
			t.Fatalf("ID=%q full listings=%d", created.ID(), calls)
		}
		observed = append(observed, []any{created.ID(), calls})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:183
	t.Run("reopens an exact ID from a renamed session file", func(t *testing.T) {
		cwd, dir := readonlyIDFixture(t)
		original, err := newSessionManagerWithDir(cwd, dir).Create("renamed-id", "")
		if err != nil {
			t.Fatal(err)
		}
		persistReadonlyIDSession(t, original, "persist me")
		renamed := filepath.Join(dir, "imported-session.jsonl")
		if err := os.Rename(original.Path(), renamed); err != nil {
			t.Fatal(err)
		}
		reopened, err := openReadonlyIDSession(t, CLIFlags{SessionID: "renamed-id"}, cwd, dir)
		if err != nil {
			t.Fatal(err)
		}
		if reopened.Path() != renamed {
			t.Fatalf("reopened path=%q want=%q", reopened.Path(), renamed)
		}
		observed = append(observed, reopened.Path() == renamed)
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:203
	t.Run("filters exact IDs by cwd in a custom session directory", func(t *testing.T) {
		root := t.TempDir()
		projectA, projectB, dir := filepath.Join(root, "project-a"), filepath.Join(root, "project-b"), filepath.Join(root, "sessions")
		for _, path := range []string{projectA, projectB} {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		managerB := newSessionManagerWithDir(projectB, dir)
		foreign, err := managerB.Create("foreign-id", "")
		if err != nil {
			t.Fatal(err)
		}
		persistReadonlyIDSession(t, foreign, "foreign session")
		foundA := newSessionManagerWithDir(projectA, dir).FindByID("foreign-id")
		foundB := managerB.FindByID("foreign-id")
		if foundA != "" {
			t.Fatalf("foreign ID visible in A: %q", foundA)
		}
		if foundB != foreign.Path() {
			t.Fatalf("own ID=%q want=%q", foundB, foreign.Path())
		}
		observed = append(observed, []bool{foundA == "", foundB == foreign.Path()})
	})
	// .upstream/v0.99.1/packages/coding-agent/test/session-id-readonly.test.ts:217
	t.Run("rejects an existing fork target in process", func(t *testing.T) {
		cwd, dir := readonlyIDFixture(t)
		manager := newSessionManagerWithDir(cwd, dir)
		for _, id := range []string{"source-id", "existing-id"} {
			session, err := manager.Create(id, "")
			if err != nil {
				t.Fatal(err)
			}
			content := "source"
			if id == "existing-id" {
				content = "target"
			}
			persistReadonlyIDSession(t, session, content)
		}
		_, err := openReadonlyIDSession(t, CLIFlags{Fork: "source-id", SessionID: "existing-id"}, cwd, dir)
		if err == nil || !strings.Contains(err.Error(), "Session already exists with id 'existing-id'") {
			t.Fatalf("fork target rejection = %v", err)
		}
		cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), "--session-dir", dir, "--fork", "source-id", "--session-id", "existing-id", "-p", "test")
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "PIG_CODING_AGENT_DIR="+filepath.Join(t.TempDir(), "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0", "NO_COLOR=1")
		output, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || string(output) != "Session already exists with id 'existing-id'\n" {
			t.Fatalf("exit=%v output=%s", err, output)
		}
		observed = append(observed, []any{exit.ExitCode(), string(output)})
	})
	data, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SESSION_ID_READONLY %s\n", data)
}
