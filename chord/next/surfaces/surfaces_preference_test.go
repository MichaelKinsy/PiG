package surfaces

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// pi: packages/chord/test/surfaces/preference.test.ts
// pi: packages/chord/test/surfaces/locale-choice.test.ts

var bg = context.Background()

func zoomContract(t testing.TB) (*contracts.Contract, contracts.Schema) {
	t.Helper()
	pick := contracts.Int(contracts.Min(50), contracts.Max(200))
	zoom, err := PreferenceContract("test.zoom.choice", pick)
	if err != nil {
		t.Fatal(err)
	}
	return zoom, pick
}

// serverAndClients is a server keeping picks in the store at path, with facet loaded under name, and a client held by
// each of users.
func serverAndClients(t testing.TB, path string, facet host.Facet, name string, users ...string) (clients []host.Host, server host.Host, closeAll func()) {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), bg, store.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	address := serverKind.Address(nil)
	server, err = host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), Store: opened, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	if report, err := server.Load([]host.HostFacet{{Name: name, Facet: facet}}, bg); err != nil || len(report.Failed) != 0 {
		t.Fatalf("load: %v %v", err, report.Failed)
	}
	for _, id := range users {
		clients = append(clients, connectUser(t, server, id))
	}
	return clients, server, func() {
		for _, client := range clients {
			client.Close(bg)
		}
		server.Close(bg)
		opened.Close(bg)
	}
}

// connectUser is a client of server for the principal user id.
func connectUser(t testing.TB, server host.Host, id string) host.Host {
	t.Helper()
	client, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Load(nil, bg); err != nil {
		t.Fatal(err)
	}
	mine, theirs := link.MemoryLinkPair()
	if err := server.Accept(theirs, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "user", "id", id), Codec: link.BinaryCodec}, bg); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(mine, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server"), Codec: link.BinaryCodec}, bg); err != nil {
		t.Fatal(err)
	}
	return client
}

// serverWith is serverAndClients under the name "test.preference".
func serverWith(t testing.TB, path string, facet host.Facet, users ...string) ([]host.Host, func()) {
	t.Helper()
	clients, _, closeAll := serverAndClients(t, path, facet, "test.preference", users...)
	return clients, closeAll
}

func until(t testing.TB, check func() bool) {
	t.Helper()
	for i := 0; i < 400 && !check(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !check() {
		t.Fatal("condition not reached")
	}
}

func pickOf(t testing.TB, ref *host.Reference) delta.JsonValue {
	t.Helper()
	value, err := ref.Member("pick").Value()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func requireInvalidArgs(t testing.TB, err error) {
	t.Helper()
	var chordErr *data.ChordError
	if !errors.As(err, &chordErr) || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonArgs {
		t.Fatalf("error = %v, want invalid/args", err)
	}
}

func TestPreferenceIsNoneBeforeAChoiceAndEachPrincipalsOwnAfter(t *testing.T) {
	zoom, pick := zoomContract(t)
	facet, err := PreferenceFacet(zoom, "test.zoom.picks", pick, 100.0)
	if err != nil {
		t.Fatal(err)
	}
	clients, closeAll := serverWith(t, filepath.Join(t.TempDir(), "store.db"), facet, "ada", "grace")
	defer closeAll()
	ada, err := clients[0].Get(bg, zoom, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	grace, err := clients[1].Get(bg, zoom, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if a, g := pickOf(t, ada), pickOf(t, grace); a != 100.0 || g != 100.0 {
		t.Fatalf("picks = %v, %v", a, g)
	}
	if _, err := ada.Call("choose", 150.0, bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return pickOf(t, ada) == 150.0 })
	if got := pickOf(t, grace); got != 100.0 {
		t.Errorf("grace = %v, want 100", got)
	}
	_, err = grace.Call("choose", 500.0, bg)
	requireInvalidArgs(t, err)
}

func TestPreferenceIsKeptInTheServersStoreAcrossARestart(t *testing.T) {
	zoom, pick := zoomContract(t)
	facet, err := PreferenceFacet(zoom, "test.zoom.picks", pick, 100.0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "store.db")
	first, closeFirst := serverWith(t, path, facet, "ada")
	choice, err := first[0].Get(bg, zoom, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := choice.Call("choose", 120.0, bg); err != nil {
		t.Fatal(err)
	}
	closeFirst()
	second, closeSecond := serverWith(t, path, facet, "ada")
	defer closeSecond()
	again, err := second[0].Get(bg, zoom, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pickOf(t, again); got != 120.0 {
		t.Errorf("after restart = %v, want 120", got)
	}
}

func TestLocaleChoiceKeepsALanguageTagOrNullPerPrincipalAndRefusesAnythingElse(t *testing.T) {
	facet, err := LocaleChoiceFacet()
	if err != nil {
		t.Fatal(err)
	}
	clients, closeAll := serverWith(t, ":memory:", facet, "ada")
	defer closeAll()
	choice, err := clients[0].Get(bg, LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := pickOf(t, choice); got != nil {
		t.Fatalf("pick = %v, want null", got)
	}
	for _, good := range []delta.JsonValue{"pt-BR", nil} {
		if _, err := choice.Call("choose", good, bg); err != nil {
			t.Fatalf("choose(%v): %v", good, err)
		}
	}
	for _, bad := range []string{"not a tag", strings.Repeat("x", 36), "de_DE"} {
		_, err := choice.Call("choose", bad, bg)
		requireInvalidArgs(t, err)
	}
	choice.Release()
}
