package coding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/codingagent/compaction"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi keeps extension values as JavaScript strings and persists them with JSON.stringify (session-manager.ts:1160-1185), so a lone UTF-16 unit an extension supplies reaches the session file as the same \udXXX escape.

func TestExtensionCompactionPreservesLoneSurrogates(t *testing.T) {
	// agent-session.ts compact(): a session_before_compact result's compaction summary and details are saved as given (compaction-extensions.test.ts:173).
	lone := jsstring.FromUTF16([]uint16{0xd800})
	p := &scriptedProvider{}
	for range 4 {
		p.responses = append(p.responses, fauxReply("complete summary", ai.StopReasonStop, 0))
	}
	model := fakeModelWithProvider(p)
	model.ID = "faux-1"
	model.Capabilities.ContextWindow = 200000
	model.Capabilities.MaxOutputTokens = 8192
	s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SystemPrompt: "sys"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	s.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
		"session_before_compact": {func(args ...any) (any, error) {
			prep := args[0].(extension.SessionBeforeCompactEvent).Preparation.(*compaction.CompactionPreparation)
			return extension.SessionBeforeCompactResult{Compaction: map[string]any{"summary": "s" + lone, "firstKeptEntryId": prep.FirstKeptEntryID, "tokensBefore": prep.TokensBefore, "details": map[string]any{"d": lone}}}, nil
		}},
	}}}, t.TempDir()))
	for _, prompt := range []string{"one", "two"} {
		if _, err := s.Send(t.Context(), prompt); err != nil {
			t.Fatal(err)
		}
		drainEvents(t, s)
	}
	result, err := s.CompactResult(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "s"+lone {
		t.Fatalf("summary units=%x, want %x", jsstring.ToUTF16(result.Summary), jsstring.ToUTF16("s"+lone))
	}
	var line string
	for _, entry := range s.inner.Entries() {
		if entry.Base.Type == "compaction" {
			line = string(entry.Raw())
		}
	}
	if !strings.Contains(line, `"summary":"s\ud800"`) || !strings.Contains(line, `"details":{"d":"\ud800"}`) {
		t.Fatalf("compaction entry=%s, want JSON.stringify escapes", line)
	}
}

func TestBoundaryContextEditPreservesLoneSurrogates(t *testing.T) {
	// session-manager.ts appendContextEdit stores the extension's replacement object; the boundary preview returns each source entry as Pi's parsed object.
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	target, err := sess.inner.AppendCustomMessage("note", "x"+jsstring.FromUTF16([]uint16{0xdfff}), true, nil)
	if err != nil {
		t.Fatal(err)
	}
	appended, err := applyBoundaryDrafts(sess.inner, []extension.SessionBoundaryDraft{{Type: "context_edit", TargetID: target, Replacement: json.RawMessage(`{"content":"r\ud800"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if line := string(appended[0].Raw()); !strings.Contains(line, `"replacement":{"content":"r\ud800"}`) {
		t.Fatalf("context_edit entry=%s, want the replacement escape", line)
	}
	preview, err := sess.buildBoundaryContext(nil, icodingagent.EventTurnEnd)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range preview.ContextEntries {
		if source, ok := entry.SourceEntry.(map[string]any); ok && source["id"] == target {
			if content := source["content"]; content != "x"+jsstring.FromUTF16([]uint16{0xdfff}) {
				t.Fatalf("preview content=%q", content)
			}
			return
		}
	}
	t.Fatalf("preview lacks the custom message: %+v", preview.ContextEntries)
}
