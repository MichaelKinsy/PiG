package sharing

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Ports apps/p2/test/p2/sharing-db.test.ts (Pi c23b9ec1): the share store: version 1 guests keep their access, and one guest
// holds grants to several sessions.

var background = context.Background()

func mustDo[T any](t *testing.T) func(T, error) T {
	return func(value T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func openShareDB(t *testing.T, directory string) *ShareDB {
	t.Helper()
	db, err := OpenShareDB(background, directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestShareStoreUpgradesVersion1GuestsToGrants(t *testing.T) {
	directory := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(directory, "shares.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = old.Exec(`
		CREATE TABLE links (
			id TEXT PRIMARY KEY, session TEXT NOT NULL, mode TEXT NOT NULL, token TEXT NOT NULL UNIQUE,
			UNIQUE(session, mode)
		) STRICT;
		CREATE TABLE guests (
			id TEXT PRIMARY KEY, linkId TEXT NOT NULL, name TEXT NOT NULL, secretHash TEXT NOT NULL,
			removed INTEGER NOT NULL DEFAULT 0, revision INTEGER NOT NULL DEFAULT 1, lastSeen INTEGER NOT NULL
		) STRICT;
		INSERT INTO links VALUES ('l1', 's1', 'full', '` + strings.Repeat("a", 43) + `');
		INSERT INTO guests VALUES ('g1', 'l1', 'Alice', 'hash', 0, 3, 5), ('g2', 'l1', 'Bob', 'hash', 1, 2, 6);
		PRAGMA user_version=1;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	db := openShareDB(t, directory)
	if got, want := mustDo[*GuestRow](t)(db.Guest(background, "g1")), (&GuestRow{ID: "g1", Name: "Alice", Revision: 3}); !reflect.DeepEqual(got, want) {
		t.Fatalf("guest g1 = %+v, want %+v", got, want)
	}
	l1 := "l1"
	if got, want := mustDo[[]GrantRow](t)(db.Grants(background, "g1")), []GrantRow{{Session: "s1", Mode: "full", LinkID: &l1, GrantedAt: 5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("grants of g1 = %+v, want %+v", got, want)
	}
	if grants := mustDo[[]GrantRow](t)(db.Grants(background, "g2")); len(grants) == 0 || !grants[0].Removed {
		t.Fatalf("a removed guest stays removed: %+v", grants)
	}
}

func sessionModes(grants []GrantRow) [][2]string {
	out := [][2]string{}
	for _, grant := range grants {
		out = append(out, [2]string{grant.Session, grant.Mode})
	}
	return out
}

func TestAGuestHoldsSeveralSessionsRemovalAndLinkResetsEndOneGrantEach(t *testing.T) {
	db := openShareDB(t, t.TempDir())
	mustDo[[]string](t)(db.MakeLink(background, "s1", "view", false))
	mustDo[[]string](t)(db.MakeLink(background, "s2", "full", false))
	links := mustDo[[]Link](t)(db.Links(background))
	if len(links) != 2 {
		t.Fatalf("links = %+v", links)
	}
	first, second := links[0], links[1]

	joined := mustDo[Joined](t)(db.Join(background, first, "Bob", nil))
	if joined.Cookie == "" {
		t.Fatal("joining without a cookie mints one")
	}
	bob := mustDo[*GuestRow](t)(db.GuestOfCookie(background, joined.Cookie))
	if bob == nil {
		t.Fatal("the cookie finds its guest")
	}
	if got := mustDo[Joined](t)(db.Join(background, second, "ignored", bob)); got != (Joined{Guest: bob.ID, Changed: true}) {
		t.Fatalf("the same browser gains s2: %+v", got)
	}
	if got, want := sessionModes(mustDo[[]GrantRow](t)(db.Grants(background, bob.ID))), [][2]string{{"s1", "view"}, {"s2", "full"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("grants = %v, want %v", got, want)
	}
	if revision := mustDo[*GuestRow](t)(db.Guest(background, bob.ID)).Revision; revision != 2 {
		t.Fatalf("revision = %d, want 2", revision)
	}
	again := mustDo[*GuestRow](t)(db.Guest(background, bob.ID))
	if got := mustDo[Joined](t)(db.Join(background, second, "ignored", again)); got != (Joined{Guest: bob.ID}) {
		t.Fatalf("joining the same link again changes nothing: %+v", got)
	}

	ann := mustDo[Shared](t)(db.Share(background, "s1", "ann@example.com", "Ann@Example.com", "view"))
	mustDo[Shared](t)(db.Share(background, "s2", "ann@example.com", "Ann@Example.com", "full"))
	if got := mustDo[*GuestRow](t)(db.GuestOfEmail(background, "ann@example.com")); got == nil || got.ID != ann.Guest {
		t.Fatalf("guest of email = %+v, want %s", got, ann.Guest)
	}
	if got := mustDo[[]string](t)(db.Emails(background, func(string) bool { return true })); !reflect.DeepEqual(got, []string{"ann@example.com"}) {
		t.Fatalf("emails = %v", got)
	}
	if got := mustDo[[]string](t)(db.Emails(background, func(session string) bool { return session != "s1" && session != "s2" })); len(got) != 0 {
		t.Fatalf("archived sessions report nobody: %v", got)
	}
	if got := len(mustDo[[]PersonRow](t)(db.People(background))); got != 2 {
		t.Fatalf("people = %d, want 2", got)
	}

	if !mustDo[bool](t)(db.Remove(background, "s1", ann.Guest)) {
		t.Fatal("removing a grant it has")
	}
	var sessions []string
	for _, person := range mustDo[[]PersonRow](t)(db.People(background)) {
		sessions = append(sessions, person.Session)
	}
	if !reflect.DeepEqual(sessions, []string{"s2"}) {
		t.Fatalf("people's sessions = %v, want [s2]", sessions)
	}
	mustDo[Shared](t)(db.Share(background, "s1", "ann@example.com", "Ann@Example.com", "view"))
	if got := len(mustDo[[]PersonRow](t)(db.People(background))); got != 2 {
		t.Fatalf("sharing again gives a removed person access again: %d people", got)
	}

	if got := mustDo[[]string](t)(db.MakeLink(background, "s1", "view", true)); !reflect.DeepEqual(got, []string{bob.ID}) {
		t.Fatalf("a link reset ends bob's grant: %v", got)
	}
	if got, want := sessionModes(mustDo[[]GrantRow](t)(db.Grants(background, bob.ID))), [][2]string{{"s2", "full"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a link reset ends only the grants made through it: %v", got)
	}
}

func TestEmailsNormalizeAsRadiusNormalizesThem(t *testing.T) {
	for email, want := range map[string]string{
		" Ann.Example@GoogleMail.com ": "annexample@gmail.com",
		"Bob@Bücher.example":           "bob@xn--bcher-kva.example",
		"not an address":               "",
	} {
		if got := NormalizeEmail(email); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", email, got, want)
		}
	}
}
