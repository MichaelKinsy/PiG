package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi rebinds after /resume and rebuilds the autocomplete provider on this.sessionManager.getCwd()
// (interactive-mode.ts:820, :2040). Resuming a session recorded in directory B from a mode started in A must complete
// paths from B, even though the process directory is neither.
func TestResumeSessionFromOtherCwdAutocompletesFromSessionCwd(t *testing.T) {
	icodingagent.ObserveRebindTitles(t, func(string) {})
	t.Chdir(t.TempDir())
	f := newRebindFixture(t, func(*coding.Session, ...any) (any, error) { return nil, nil }, nil)
	f.runtime.SetRebindSession(func(ctx context.Context, session *coding.Session) error {
		// The production ReplacementResources (coding/cli interactiveReplacement) carries the runtime build's cwd.
		return f.h.RebindToReplacement(ctx, session, icodingagent.InteractiveReplacement{
			CWD:             session.SessionManager().GetCwd(),
			Settings:        session.Services().SettingsManager().Get(),
			SettingsManager: session.Services().SettingsManager(),
			ModelRegistry:   session.Services().Registry().ModelRegistry,
		})
	})
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirB, "only-in-b.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(t.TempDir(), "b.jsonl")
	log := `{"type":"session","version":3,"id":"in-b","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(dirB) + `}` + "\n" +
		`{"type":"message","id":"u1","parentId":null,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := f.runtime.SwitchSession(t.Context(), sessionFile); err != nil || result.Cancelled {
		t.Fatalf("resume = %+v, %v", result, err)
	}
	suggestions := f.h.AutocompleteSuggestions("./only")
	if suggestions == nil || len(suggestions.Items) != 1 || suggestions.Items[0].Value != "./only-in-b.txt" {
		t.Fatalf("suggestions after resume = %#v, want ./only-in-b.txt from session cwd %s", suggestions, dirB)
	}
}
