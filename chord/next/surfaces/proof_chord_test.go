package surfaces

import (
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Proof of locale-choice.ts: a language tag may be 35 characters, not 36 (its practical bound), whatever its segments.

func TestLocaleChoiceTakesATagOf35CharactersAndRefusesOneOf36(t *testing.T) {
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
	defer choice.Release()
	const at35, at36 = "abcdefgh-12345678-12345678-123456-1", "abcdefgh-12345678-12345678-1234567-1"
	if len(at35) != 35 || len(at36) != 36 {
		t.Fatalf("the fixtures are %d and %d characters", len(at35), len(at36))
	}
	if _, err := choice.Call("choose", at35, bg); err != nil {
		t.Fatalf("choose of a 35-character tag: %v", err)
	}
	if got := pickOf(t, choice); got != at35 {
		t.Fatalf("pick = %v, want %s", got, at35)
	}
	_, err = choice.Call("choose", at36, bg)
	requireInvalidArgs(t, err)
}

// Proof of LoadedFacets (loaded.ts): a failed plugin is left out of the first load, an unchanged plugin (same digest) is not
// evaluated again on a reload, a plugin whose new script fails keeps its loaded facet and is named failed, and a plugin not in
// the set is removed.

func loadedHost(t *testing.T) host.Host {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := store.OpenStore(store.SqliteBackend(database), bg, store.StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	address := serverKind.Address(nil)
	h, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), Store: opened, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closed(t, h.Close(bg)) })
	return h
}

func TestLoadedFacetsLeavesAFailedPluginOutAndReloadsOnlyWhatChanged(t *testing.T) {
	h := loadedHost(t)
	empty := host.DefineFacet(func(host.FacetHost, host.FacetSelf) error { return nil })
	calls := map[string]int{}
	entry := func(name, digest string, err error) SetEntry {
		d := digest
		return SetEntry{Name: name, Digest: &d, Prepare: func() Prepared {
			calls[name]++
			return Prepared{Facet: empty, Digest: digest, Err: err}
		}}
	}
	broken := errors.New("script failed")
	var loaded *LoadedFacets
	loaded, report, err := LoadFacets(h, nil, []SetEntry{entry("a", "d1", nil), entry("b", "d1", broken)}, bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Failed) != 1 || report.Failed[0].Plugin != "b" {
		t.Fatalf("Failed = %v, want b", report.Failed)
	}
	if _, has := loaded.find("a"); !has {
		t.Fatal("the plugin that loaded is not kept")
	}
	if _, has := loaded.find("b"); has {
		t.Fatal("the plugin whose script failed is kept at the first load")
	}
	calls = map[string]int{}
	reloaded, err := loaded.Reload(h, []SetEntry{entry("a", "d1", nil), entry("c", "d9", nil)}, bg)
	if err != nil {
		t.Fatal(err)
	}
	if calls["a"] != 0 || calls["c"] != 1 {
		t.Fatalf("Prepare calls = %v; an unchanged digest must not be evaluated again, a new plugin once", calls)
	}
	if !slices.Equal(reloaded.Plugins.Added, []string{"c"}) {
		t.Fatalf("reload added %v, want [c]", reloaded.Plugins.Added)
	}
	failing, err := loaded.Reload(h, []SetEntry{entry("a", "d2", broken), entry("c", "d9", nil)}, bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(failing.Failed) != 1 || failing.Failed[0].Plugin != "a" {
		t.Fatalf("Failed = %v, want a", failing.Failed)
	}
	if kept, has := loaded.find("a"); !has || kept.digest != "d1" {
		t.Fatalf("a failing script replaced the loaded facet: %v %v", kept.digest, has)
	}
	if _, err := loaded.Reload(h, []SetEntry{entry("c", "d9", nil)}, bg); err != nil {
		t.Fatal(err)
	}
	if _, has := loaded.find("a"); has {
		t.Fatal("a plugin left out of the set stays loaded")
	}
}

// sharedClose is the link the surfaces' connection loop and host follow together (surfaces_client.go): over a real link it runs
// each listener once, and one added after the close at once.
func TestASharedCloseLinkRunsEachCloseListenerOnceAndALateOneAtOnce(t *testing.T) {
	a, _ := link.MemoryLinkPair()
	shared := newSharedClose(a)
	var _ LoopLink = shared
	var first, second atomic.Int32
	done := make(chan struct{})
	shared.OnClose(func() { first.Add(1) })
	shared.OnClose(func() { second.Add(1); close(done) })
	shared.Close()
	shared.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the close listeners did not run")
	}
	var late atomic.Int32
	shared.OnClose(func() { late.Add(1) })
	if first.Load() != 1 || second.Load() != 1 || late.Load() != 1 {
		t.Fatalf("listener runs = %d, %d, %d; want 1, 1 and a late one at once", first.Load(), second.Load(), late.Load())
	}
}
