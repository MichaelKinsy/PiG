package driver_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/sqlhost"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// openCore opens the real core over a SQLite file through the native driver.
func openCore(t *testing.T) *driver.Session {
	t.Helper()
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	uuid := func() string { id, _ := ai.UUIDv7(nil); return id }
	models := ai.CreateModels()
	s, err := driver.OpenSession(t.Context(), driver.SessionConfig{
		Core: core.NewSession(uuid), SQL: core.SQLTable(), DB: db,
		Harness: harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(t.Context()) })
	return s
}

// An idle write settles done in the commit that admits it, and the same request ID returns the same submission without a
// second commit (src/harness/submissions.ts admitSubmission).
func TestSubmitAdmitsAnIdleWriteAndReplaysItsRequestID(t *testing.T) {
	s := openCore(t)
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rid := "request-1"
	entry := durable.EntryDraft{Kind: "note"}
	draft := durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &entry, RequestId: &rid}
	first, err := root.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	record, err := first.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "done" || record.Entry == nil {
		t.Fatalf("an idle write is done with its entry: %+v", record)
	}
	again, err := root.Submit(t.Context(), draft)
	if err != nil {
		t.Fatal(err)
	}
	if again.Id() != first.Id() {
		t.Fatalf("a known request ID returns its submission: %d vs %d", again.Id(), first.Id())
	}
	result, err := first.Abort(t.Context())
	if err != nil || result != durable.SubmissionSettled {
		t.Fatalf("aborting a settled submission reports settled: %v %v", result, err)
	}
}

// The api request `agent` (ABI 8.1) is answered by the core from the host's resolveAgent hook: a Section's text reaches the
// prompt only if the host can read the conversation's resolved agent (the section effect asks for it).
func TestAgentIsServedByTheCoreFromTheRegistryAndTheStoredState(t *testing.T) {
	db, err := sqlhost.OpenDB(t.Context(), filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	registry := harness.CreateRegistry()
	untagged := false
	if err := registry.Install(&durable.Extension{
		Name:     "ext",
		Sections: []*durable.PromptSection{harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) { return new("hello"), nil }, harness.SectionOptions{Tag: &untagged})},
	}); err != nil {
		t.Fatal(err)
	}
	uuid := func() string { id, _ := ai.UUIDv7(nil); return id }
	s, err := driver.OpenSession(t.Context(), driver.SessionConfig{
		Core: core.NewSession(uuid), SQL: core.SQLTable(), DB: db,
		Harness: harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(t.Context()) })
	root, err := s.Root(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := root.Agent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.Sections) != 1 || agent.Sections[0].Key != "preamble" {
		t.Fatalf("agent sections = %+v", agent.Sections)
	}
}
