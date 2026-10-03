package session_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// Shared helpers for the ports of packages/durable/test/session-*.test.ts.

type obj = durable.JsonObject

type ctxT = context.Context

var ctx = context.Background()

var (
	sessionScope = durable.DocumentSemantics{Scope: durable.ScopeSession}
	taskScope    = durable.DocumentSemantics{Scope: durable.ScopeTask}
)

func latestScope(fork durable.DocumentFork) durable.DocumentSemantics {
	return durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: fork}
}

func rewindableScope(fork durable.DocumentFork) durable.DocumentSemantics {
	return durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryRewindable, Fork: fork}
}

type docOption func(*durable.CommonDocDefinition[obj])

func withMigrate(migrate func(value obj, fromVersion int) obj) docOption {
	return func(definition *durable.CommonDocDefinition[obj]) {
		definition.Migrate = func(value durable.JsonObject, fromVersion int) (obj, error) {
			return migrate(value, fromVersion), nil
		}
	}
}

func withCheckpoint(checkpoint func(value obj, ops []durable.Op, info durable.CheckpointInfo) bool) docOption {
	return func(definition *durable.CommonDocDefinition[obj]) { definition.CheckpointWhen = checkpoint }
}

func defineDoc(kind string, version int, semantics durable.DocumentSemantics, initial func() obj, options ...docOption) durable.DocToken[obj] {
	common := durable.CommonDocDefinition[obj]{Kind: kind, Version: version}
	for _, option := range options {
		option(&common)
	}
	return durable.DefineDoc(durable.DocDefinition[obj]{CommonDocDefinition: common, DocumentSemantics: semantics, Initial: initial})
}

func defineFamily[I any](kind string, version int, semantics durable.DocumentSemantics, initial func(seed I) obj, options ...docOption) durable.DocFamilyToken[obj, I] {
	common := durable.CommonDocDefinition[obj]{Kind: kind, Version: version}
	for _, option := range options {
		option(&common)
	}
	return durable.DefineDocFamily(durable.DocFamilyDefinition[obj, I]{CommonDocDefinition: common, DocumentSemantics: semantics, Initial: initial})
}

func open() sessiontest.Harness { return sessiontest.OpenTestSession() }

func flush(harness sessiontest.Harness) { harness.Session.WaitDeliveries() }

// commit runs one commit that must succeed.
func commit(t *testing.T, kernel *session.SessionImpl, change func(tx durable.Tx) error) {
	t.Helper()
	if err := tryCommit(kernel, change); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func tryCommit(kernel *session.SessionImpl, change func(tx durable.Tx) error) error {
	_, err := kernel.Commit(ctx, func(tx durable.Tx) (any, error) { return nil, change(tx) })
	return err
}

// commitWith runs one internal commit that must succeed.
func commitWith(t *testing.T, kernel *session.SessionImpl, change func(tx *session.Transaction) error) {
	t.Helper()
	if err := tryCommitWith(kernel, change); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func tryCommitWith(kernel *session.SessionImpl, change func(tx *session.Transaction) error) error {
	_, err := kernel.CommitWith(ctx, func(tx *session.Transaction) (any, error) { return nil, change(tx) }, session.TransactionScope{})
	return err
}

func mustDoc(t *testing.T, tx durable.Tx, token durable.AnyDocToken, args ...any) *delta.Object {
	t.Helper()
	draft, err := tx.Doc(token, args...)
	if err != nil {
		t.Fatalf("doc: %v", err)
	}
	return draft
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func snapshot(t *testing.T, kernel *session.SessionImpl, token durable.AnyDocToken, args ...any) obj {
	t.Helper()
	value, err := kernel.SnapshotErased(ctx, token, args...)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return value
}

// same reports reference identity of two JSON containers, upstream's toBe.
func same(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	a, b := reflect.ValueOf(left), reflect.ValueOf(right)
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case reflect.Map, reflect.Pointer:
		return a.UnsafePointer() == b.UnsafePointer()
	case reflect.Slice:
		return a.UnsafePointer() == b.UnsafePointer() && a.Len() == b.Len()
	}
	return reflect.DeepEqual(left, right)
}

func equal(left, right any) bool { return reflect.DeepEqual(normalize(left), normalize(right)) }

// normalize maps Go numeric kinds to float64 so literal expectations compare with JSON values.
func normalize(value any) any {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalize(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = normalize(item)
		}
		return out
	case []durable.Op:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = normalize(item)
		}
		return out
	}
	return value
}

func expectEqual(t *testing.T, got, want any) {
	t.Helper()
	if !equal(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func expectErrorContains(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("got error %v, want one containing %q", err, text)
	}
}

func expectStrictJSON(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, delta.ErrNotStrictJSON) {
		t.Fatalf("got error %v, want a strict JSON rejection", err)
	}
	expectErrorContains(t, err, "strict JSON")
}

func num(value any) float64 {
	number, _ := value.(float64)
	return number
}

func writesOfType(writes []durable.StorageWrite, kind string) []durable.StorageWrite {
	var matched []durable.StorageWrite
	for _, write := range writes {
		if durable.StorageWriteType(write) == kind {
			matched = append(matched, write)
		}
	}
	return matched
}

func ownerless() durable.CreateConversationOptions {
	return durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}
}

func createConversation(t *testing.T, harness sessiontest.Harness) durable.ConversationId {
	t.Helper()
	return sessiontest.CreateConversation(t, harness.Session)
}

func expectPanic(t *testing.T, text string, use func()) {
	t.Helper()
	defer func() {
		t.Helper()
		recovered := recover()
		if recovered == nil {
			t.Fatalf("expected a panic containing %q", text)
		}
		message := ""
		if err, ok := recovered.(error); ok {
			message = err.Error()
		} else if s, ok := recovered.(string); ok {
			message = s
		}
		if !strings.Contains(message, text) {
			t.Fatalf("panic %v does not contain %q", recovered, text)
		}
	}()
	use()
}
