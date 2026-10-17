package core

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/session/sessiontest"
)

func migrationRequest(t *testing.T, json string) *jv.Object {
	t.Helper()
	v, err := jv.Parse([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jv.Object)
}

// A read-only access to a document stored at an older version asks the host to run the definition's migrate: the first
// attempt rejects NeedMigrate with {value, from}, and the request repeated with `migrated` takes the host's value.
func TestAReadOnlyMigrationIsAskedOfTheHostThenAnswered(t *testing.T) {
	stored := jv.NewObject()
	stored.Set("count", float64(1))

	def := &session.DocDef{}
	m := hostMigration(def, migrationRequest(t, `{"def":{"migrates":true}}`))
	if _, err := def.HostMigrate(stored, 1); err == nil {
		t.Fatal("the first attempt must need the host")
	} else if _, ok := session.IsNeedRead(err); !ok {
		t.Fatalf("the first attempt returned %v, want a NeedRead", err)
	}
	var rej *Rejection
	if err := m.need(&session.NeedRead{}); !errors.As(err, &rej) || rej.Name != "NeedMigrate" || rej.Message != `{"value":{"count":1},"from":1}` {
		t.Fatalf("need = %v, want NeedMigrate {value, from}", err)
	}

	def = &session.DocDef{}
	hostMigration(def, migrationRequest(t, `{"def":{"migrates":true},"migrated":{"count":2}}`))
	got, err := def.HostMigrate(stored, 1)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := got.Get("count"); n != float64(2) {
		t.Fatalf("migrated = %v, want the host's count 2", n)
	}
}

// A definition without migrate leaves HostMigrate unset, so the kernel reports Pi's "requires migration" error with the document ID.
func TestADefinitionWithoutMigrateKeepsPisRequiresMigrationError(t *testing.T) {
	def := &session.DocDef{}
	m := hostMigration(def, migrationRequest(t, `{"def":{"kind":"x"}}`))
	if def.HostMigrate != nil {
		t.Fatal("HostMigrate set for a definition without migrate")
	}
	plain := errors.New("Document 2 (x) requires migration from version 1")
	if got := m.need(plain); !errors.Is(got, plain) {
		t.Fatalf("need rewrote an ordinary error: %v", got)
	}
}

// The host's unloadDocuments drops the loaded-document cache (session.ts unloadDocuments), so the next read of a
// document stored at an older version asks the host to migrate again.
func TestUnloadDocumentsDropsTheLoadedDocuments(t *testing.T) {
	mem := sessiontest.New()
	s := session.New(mem, mem.NextID(), func() float64 { return 0 })
	def := &session.DocDef{Kind: "t.note", Version: 1, Scope: session.ScopeSession, History: session.HistoryLatest,
		Initial: func(any) *jv.Object { return jv.NewObject() }}
	if _, err := s.Commit(func(tx *session.Tx) error {
		_, err := tx.Doc(def, session.DocArgs{})
		return err
	}, session.TxScope{}); err != nil {
		t.Fatal(err)
	}
	if !s.Loaded(def, session.DocArgs{}) {
		t.Fatal("the commit did not load the document")
	}
	h := &handler{sess: s}
	if _, err := h.api("unloadDocuments", nil); err != nil {
		t.Fatal(err)
	}
	if s.Loaded(def, session.DocArgs{}) {
		t.Fatal("unloadDocuments left the document loaded")
	}
}
