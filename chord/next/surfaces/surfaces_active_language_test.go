package surfaces

import (
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// pi: packages/chord/test/surfaces/active-language.test.ts

func notesEnDe(t testing.TB) *Messages {
	return mustMessages(t, "notes",
		LocaleCatalog{"en", Catalog{"newNote": "New note", "empty": "No notes"}},
		LocaleCatalog{"de", Catalog{"newNote": "Neue Notiz", "empty": "Keine Notizen"}})
}

// languageSetup is a server with locale choices, and a client of ada whose plugins contribute the notes catalogs and fr.
func languageSetup(t testing.TB, notes *Messages) (client host.Host, closeAll func()) {
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
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), Store: opened, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	facet, err := LocaleChoiceFacet()
	if err != nil {
		t.Fatal(err)
	}
	if report, err := server.Load([]host.HostFacet{{Name: "chord.locale.choice", Facet: facet}}, bg); err != nil || len(report.Failed) != 0 {
		t.Fatalf("server load: %v %v", err, report.Failed)
	}
	client, err = host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	contribute := func(translations []Translation) host.Facet {
		return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
			return self.Contribute(registries.Registry[any, any](Translations), translations)
		})
	}
	report, err := client.Load([]host.HostFacet{
		{Name: "chord.translations", Facet: TranslationsFacet()},
		{Name: "@test/notes", Facet: contribute(notes.Translations)},
		{Name: "@test/french", Facet: contribute([]Translation{{Namespace: "notes", Locale: "fr", Messages: Catalog{"newNote": "Nouvelle note"}}})},
	}, bg)
	if err != nil || len(report.Failed) != 0 {
		t.Fatalf("client load: %v %v", err, report.Failed)
	}
	mine, theirs := link.MemoryLinkPair()
	if err := server.Accept(theirs, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "user", "id", "ada"), Codec: link.BinaryCodec}, bg); err != nil {
		t.Fatal(err)
	}
	if err := client.Connect(mine, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server"), Codec: link.BinaryCodec}, bg); err != nil {
		t.Fatal(err)
	}
	return client, func() { client.Close(bg); server.Close(bg); opened.Close(bg) }
}

func TestFollowsTheDevicesLanguagesThePrincipalsPickAndTheCatalogsKeptAsThePageState(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	var mu sync.Mutex
	var seen []*Language
	last := func() string {
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 {
			return ""
		}
		return seen[len(seen)-1].Locale
	}
	if HostLanguage(client).Read() != nil {
		t.Fatal("a language before a follower")
	}
	follower, err := FollowActiveLanguage(client, []string{"de-AT", "en"}, func(l *Language) { mu.Lock(); seen = append(seen, l); mu.Unlock(); wakeups.poke() }, func(MessagesReport) {}, bg)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Stop()
	if got := last(); got != "de" {
		t.Fatalf("locale = %q, want de", got)
	}
	mu.Lock()
	same := HostLanguage(client).Read() == seen[len(seen)-1]
	mu.Unlock()
	if !same {
		t.Error("the page state is not the last language")
	}
	if got := mustT(t, notes.In(HostLanguage(client).Read()), "newNote"); got != "Neue Notiz" {
		t.Errorf("newNote = %q", got)
	}
	choice, err := client.Get(bg, LocaleChoice, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer choice.Release()
	if _, err := choice.Call("choose", "fr", bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return last() == "fr" })
	follower.SetDevice([]string{"en"})
	if got := last(); got != "fr" {
		t.Errorf("after a device change = %q, want fr", got)
	}
	if _, err := choice.Call("choose", nil, bg); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return last() == "en" })
	mu.Lock()
	count := len(seen)
	mu.Unlock()
	follower.SetDevice([]string{"en-GB"})
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != count {
		t.Errorf("the language was set again for an unchanged locale: %d -> %d", count, len(seen))
	}
}

func TestPassesEachDistinctReportOnOnce(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	var mu sync.Mutex
	var reports []MessagesReport
	follower, err := FollowActiveLanguage(client, []string{"fr"}, func(*Language) {}, func(r MessagesReport) { mu.Lock(); reports = append(reports, r); mu.Unlock() }, bg)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Stop()
	tr := notes.In(HostLanguage(client).Read())
	got := []string{mustT(t, tr, "empty"), mustT(t, tr, "empty"), mustT(t, tr, "newNote")}
	if want := []string{"No notes", "No notes", "Nouvelle note"}; !reflect.DeepEqual(got, want) {
		t.Errorf("texts = %v, want %v", got, want)
	}
	want := []MessagesReport{{Kind: ReportFallback, Namespace: "notes", Locale: "fr", Key: "empty", Used: ""}}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(reports, want) {
		t.Errorf("reports = %+v, want %+v", reports, want)
	}
}

// A follower stopped at once, before the server answers its pick watch, ends quietly (Pi: `.then(start, () => {})`).
func TestStoppingAFollowerBeforeItsPickWatchBindsEndsQuietly(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	for range 50 {
		follower, err := FollowActiveLanguage(client, []string{"en"}, func(*Language) {}, func(MessagesReport) {}, bg)
		if err != nil {
			t.Fatal(err)
		}
		follower.Stop()
	}
}

// Stop and SetDevice from the callback itself do not deadlock (Pi calls changed with no lock held).
func TestAFollowersCallbackMayStopItAndChangeTheDevice(t *testing.T) {
	notes := notesEnDe(t)
	client, closeAll := languageSetup(t, notes)
	defer closeAll()
	var follower *LanguageFollower
	var once sync.Once
	done := make(chan struct{})
	made := make(chan struct{})
	var err error
	go func() {
		follower, err = FollowActiveLanguage(client, []string{"en"}, func(*Language) {
			once.Do(func() {
				go func() { <-made; follower.SetDevice([]string{"de"}); follower.Stop(); close(done) }()
			})
		}, func(MessagesReport) {}, bg)
		close(made)
	}()
	<-made
	if err != nil {
		t.Fatal(err)
	}
	<-done
}
