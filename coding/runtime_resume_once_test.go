package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi sdk.ts:185-194 consumes one already-opened SessionManager. Validating its cwd must not cause a second load of the same transcript.
func TestRuntimeOpenUsesTheValidatedSessionSnapshot(t *testing.T) {
	services := newTestServices(t)
	runtime, err := NewRuntime(RuntimeOptions{Services: services})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Close() }()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	header, err := json.Marshal(map[string]any{"type": "session", "version": icodingagent.CurrentSessionVersion, "id": "opened-once", "cwd": services.CWD(), "timestamp": "2026-09-23T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{"type": "message", "id": "user-entry", "parentId": nil, "timestamp": "2026-09-23T00:00:01Z", "message": agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "retained from the validated snapshot"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 0, len(header)+len(message)+2)
	data = append(data, header...)
	data = append(data, '\n')
	data = append(data, message...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := runtime.startSessionWithFactory(SessionStartOptions{ResumePath: path, Model: fakeModel(), NoSession: true, NoTools: "all"}, func(s *AgentSessionServices, options SessionOptions) (*Session, error) {
		// The factory receives ownership after validation. A later filesystem change must not reopen or replace that snapshot.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		return NewSession(s, options)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if session.Inner().ID() != "opened-once" {
		t.Fatalf("constructor reopened the file: session id %q", session.Inner().ID())
	}
	messages := session.Messages()
	if len(messages) != 1 || messages[0].User == nil || ai.ContentText(messages[0].User.Content.(ai.UserContentBlocks)) != "retained from the validated snapshot" {
		t.Fatalf("validated transcript lost: %#v", messages)
	}
}
