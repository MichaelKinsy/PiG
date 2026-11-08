package sessions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

func TestMain(m *testing.M) { p2test.Main(m) }

var owner = p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}

func marshal(t testing.TB, value delta.JsonValue) string {
	t.Helper()
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// openStore opens a host store in a temporary directory, closed after the host that uses it.
func openStore(t testing.TB) store.Store {
	t.Helper()
	opened, err := p2runtime.OpenHostStore(context.Background(), t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(context.Background()) })
	return opened
}

func lockOf(mcp ...any) *delta.JsonObject {
	if mcp == nil {
		mcp = []any{}
	}
	return delta.JsonObjectOf("mcp", mcp, "projectMcp", false, "instructions", true, "capabilities", []any{"ports"})
}

func TestOwnersSetLocksAndWorkersFollowTheirOwn(t *testing.T) {
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: openStore(t)})

	ownerView, err := p2test.HoldIn(server.Client(owner), contract.SessionLock, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer ownerView.Release()
	workerView, err := p2test.HoldIn(server.Client(p2runtime.WorkerPrincipal{Session: "s1", Generation: 1}), contract.SessionLock, delta.NewJsonObject(0), "worker")
	if err != nil {
		t.Fatal(err)
	}
	defer workerView.Release()

	// A session without a lock reads none.
	if _, err := p2test.Until(workerView.Member("lock"), func(value delta.JsonValue) bool { return value == nil }, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerView.Call("set", delta.JsonObjectOf("session", "s1", "lock", lockOf("github")), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerView.Call("set", delta.JsonObjectOf("session", "s2", "lock", lockOf()), p2test.Background); err != nil {
		t.Fatal(err)
	}
	// The worker sees only its session's lock; the owner sees every one.
	want := `{"mcp":["github"],"projectMcp":false,"instructions":true,"capabilities":["ports"]}`
	if _, err := p2test.Until(workerView.Member("lock"), func(value delta.JsonValue) bool { return value != nil && marshal(t, value) == want }, 0); err != nil {
		t.Fatalf("worker's lock: %v", err)
	}
	all, err := p2test.Until(ownerView.Member("locks"), func(value delta.JsonValue) bool { return object(value).Len() == 2 }, 0)
	if err != nil || !object(all).Has("s1") || !object(all).Has("s2") {
		t.Fatalf("owner's locks: %v %v", all, err)
	}
	// Setting null removes the lock.
	if _, err := ownerView.Call("set", delta.JsonObjectOf("session", "s1", "lock", nil), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(workerView.Member("lock"), func(value delta.JsonValue) bool { return value == nil }, 0); err != nil {
		t.Fatalf("lock after removal: %v", err)
	}
	if _, err := p2test.Until(ownerView.Member("locks"), func(value delta.JsonValue) bool { return !object(value).Has("s1") && object(value).Has("s2") }, 0); err != nil {
		t.Fatalf("locks after removal: %v", err)
	}
}

func TestLocksAreTheOwnersAndTheirWorkers(t *testing.T) {
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: openStore(t)})
	guest := p2runtime.GuestPrincipal{ID: "g", Name: "G", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": p2runtime.GuestFull}}
	denied, err := server.Client(guest).Hold(p2test.Background, contract.SessionLock, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Release()
	failure, err := p2test.FailureOf(denied)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, data.CodeForbidden)
}

func asChord(err error, target **data.ChordError) bool { return errors.As(err, target) }
