package codingagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// switchCall is one runtimeHost.switchSession call.
type switchCall struct{ path, cwdOverride string }

// missingCwdRuntime is the runtime host of Pi's interactive-mode-resume-missing-cwd.test.ts: switching to the orphan without a cwd override throws MissingSessionCwdError, any other switch answers with switchCancelled.
type missingCwdRuntime struct {
	InteractiveRuntime
	orphan          string
	missingCwd      string
	currentCwd      string
	switchCancelled bool
	calls           []switchCall
}

func (r *missingCwdRuntime) SwitchSession(_ context.Context, path, cwdOverride string, _ func(string) extension.ProjectTrustContext, _ *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
	r.calls = append(r.calls, switchCall{path, cwdOverride})
	if path == r.orphan && cwdOverride == "" {
		return extension.CancelledResult{}, &MissingSessionCwdError{Issue: SessionCwdIssue{SessionFile: r.orphan, SessionCwd: r.missingCwd, FallbackCwd: r.currentCwd}}
	}
	return extension.CancelledResult{Cancelled: r.switchCancelled}, nil
}

type missingCwdFixture struct {
	m              *InteractiveMode
	rt             *missingCwdRuntime
	host           resumeHost
	currentCwd     string
	currentDir     string
	orphan         string
	original       string
	selectedLabels []string
}

// setupMissingCwd is Pi's setup(): a session whose cwd was renamed away, a current session in another directory, and a selector that picks choice ("" cancels).
func setupMissingCwd(t *testing.T, choice MissingSessionCwdChoice, switchCancelled bool) *missingCwdFixture {
	t.Helper()
	currentCwd := t.TempDir()
	missingCwd := filepath.Join(currentCwd, "renamed-away")
	oldDir := t.TempDir()
	currentDir := t.TempDir()
	orphan := filepath.Join(oldDir, "orphan.jsonl")
	header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": "orphan", "timestamp": "2026-11-10T00:00:00.000Z", "cwd": missingCwd})
	message, _ := json.Marshal(map[string]any{"type": "message", "id": "m1", "parentId": nil, "timestamp": "2026-11-10T00:00:00.000Z", "message": map[string]any{"role": "user", "content": "hello", "timestamp": 1}})
	original := string(header) + "\n" + string(message) + "\n"
	if err := os.WriteFile(orphan, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := NewSessionManagerWithDir(currentCwd, currentDir).Create("", "")
	if err != nil {
		t.Fatal(err)
	}
	rt := &missingCwdRuntime{orphan: orphan, missingCwd: missingCwd, currentCwd: currentCwd, switchCancelled: switchCancelled}
	f := &missingCwdFixture{rt: rt, currentCwd: currentCwd, currentDir: currentDir, orphan: orphan, original: original}
	f.m = &InteractiveMode{runtimeHost: rt, opts: InteractiveModeOptions{SessionHandle: &recordingCompactHandle{inner: current}}}
	f.host = resumeHost{
		await: func(ctx context.Context, work func(context.Context) error) error { return work(ctx) },
		selectOption: func(_ context.Context, _ string, labels []string) (string, bool) {
			f.selectedLabels = labels
			switch choice {
			case MissingSessionCwdContinue:
				return labels[0], true
			case MissingSessionCwdFork:
				return labels[1], true
			}
			return "", false
		},
	}
	return f
}

func (f *missingCwdFixture) sessionFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(f.currentDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (f *missingCwdFixture) requireOriginalUnchanged(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(f.orphan)
	if err != nil || string(got) != f.original {
		t.Fatalf("original session = %q, %v; want it unchanged", got, err)
	}
}

// Pi interactive-mode-resume-missing-cwd.test.ts (#11060) "forks the session into the current cwd and leaves the original unchanged".
func TestResumeWithAMissingCwdForksIntoTheCurrentCwdAndLeavesTheOriginalUnchanged(t *testing.T) {
	f := setupMissingCwd(t, MissingSessionCwdFork, false)
	result, status, err := f.m.resumeThroughRuntime(t.Context(), f.host, f.orphan, nil)
	if err != nil || result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(f.rt.calls) != 2 {
		t.Fatalf("switch calls = %+v", f.rt.calls)
	}
	forkPath := f.rt.calls[1].path
	if !strings.HasPrefix(forkPath, f.currentDir) {
		t.Fatalf("fork path %q is not in the current session dir %q", forkPath, f.currentDir)
	}
	data, err := os.ReadFile(forkPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var header struct {
		Type, ID, Cwd, ParentSession string
	}
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatal(err)
	}
	if header.Type != "session" || header.Cwd != f.currentCwd || header.ParentSession != f.orphan || header.ID == "orphan" || header.ID == "" {
		t.Fatalf("fork header = %+v, want a new id in %s forked from %s", header, f.currentCwd, f.orphan)
	}
	if !strings.Contains(lines[1], `"id":"m1"`) {
		t.Fatalf("fork entry = %s", lines[1])
	}
	f.requireOriginalUnchanged(t)
	if want := "Forked into new session " + header.ID + " in " + f.currentCwd; status != want {
		t.Fatalf("status = %q, want %q", status, want)
	}
	if f.rt.calls[1].cwdOverride != "" {
		t.Fatalf("the fork switches without a cwd override, got %q", f.rt.calls[1].cwdOverride)
	}
}

// "continues the original session in the current cwd without writing a file".
func TestResumeWithAMissingCwdContinuesTheOriginalInTheCurrentCwdWithoutWritingAFile(t *testing.T) {
	f := setupMissingCwd(t, MissingSessionCwdContinue, false)
	result, status, err := f.m.resumeThroughRuntime(t.Context(), f.host, f.orphan, nil)
	if err != nil || result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if last := f.rt.calls[len(f.rt.calls)-1]; last != (switchCall{f.orphan, f.currentCwd}) {
		t.Fatalf("last switch = %+v", last)
	}
	f.requireOriginalUnchanged(t)
	if files := f.sessionFiles(t); len(files) != 0 {
		t.Fatalf("current session dir = %v, want empty", files)
	}
	if want := "Resumed session in " + f.currentCwd; status != want {
		t.Fatalf("status = %q, want %q", status, want)
	}
}

// "removes the fork when an extension cancels the switch".
func TestResumeWithAMissingCwdRemovesTheForkWhenAnExtensionCancelsTheSwitch(t *testing.T) {
	f := setupMissingCwd(t, MissingSessionCwdFork, true)
	result, _, err := f.m.resumeThroughRuntime(t.Context(), f.host, f.orphan, nil)
	if err != nil || !result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if _, statErr := os.Stat(f.rt.calls[1].path); !os.IsNotExist(statErr) {
		t.Fatalf("the cancelled fork still exists: %v", statErr)
	}
	if files := f.sessionFiles(t); len(files) != 0 {
		t.Fatalf("current session dir = %v, want empty", files)
	}
}

// "creates no fork when the user cancels".
func TestResumeWithAMissingCwdCreatesNoForkWhenTheUserCancels(t *testing.T) {
	f := setupMissingCwd(t, "", false)
	result, status, err := f.m.resumeThroughRuntime(t.Context(), f.host, f.orphan, nil)
	if err != nil || !result.Cancelled {
		t.Fatalf("result = %+v, %v", result, err)
	}
	if len(f.rt.calls) != 1 {
		t.Fatalf("switch calls = %+v, want one", f.rt.calls)
	}
	if files := f.sessionFiles(t); len(files) != 0 {
		t.Fatalf("current session dir = %v, want empty", files)
	}
	if status != statusResumeCancelled {
		t.Fatalf("status = %q", status)
	}
}

// getMissingSessionCwdOptions names the current cwd and ends with Cancel (undefined).
func TestMissingSessionCwdOptionsNameTheCurrentCwd(t *testing.T) {
	got := GetMissingSessionCwdOptions(SessionCwdIssue{SessionCwd: "/gone", FallbackCwd: "/here"})
	want := []MissingSessionCwdOption{
		{Label: "Continue in /here (asks again next time)", Value: MissingSessionCwdContinue},
		{Label: "Fork into /here (new session ID, original kept)", Value: MissingSessionCwdFork},
		{Label: "Cancel"},
	}
	if len(got) != len(want) {
		t.Fatalf("options = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("option %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
