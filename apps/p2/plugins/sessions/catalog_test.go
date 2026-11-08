package sessions

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/durable"
)

var model = durable.ModelRef{Provider: "faux", ModelId: "faux-1"}

func openCatalog(t *testing.T, dir string) *CatalogDB {
	t.Helper()
	catalog, err := OpenCatalog(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = catalog.Close() })
	return catalog
}

func create(t *testing.T, catalog *CatalogDB, input NewSession) string {
	t.Helper()
	if input.Cwd == "" {
		input.Cwd = "/work"
	}
	if input.Title == "" {
		input.Title = "New session"
	}
	input.Model, input.ThinkingLevel = model, "high"
	session, err := catalog.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return session.ID
}

func TestCatalogCreatesRowsUnderChosenOrGeneratedIDs(t *testing.T) {
	catalog := openCatalog(t, t.TempDir())
	ctx := context.Background()
	chosen := create(t, catalog, NewSession{ID: "11111111-1111-4111-8111-111111111111", Title: "Chosen", TitleEdited: true})
	generated := create(t, catalog, NewSession{})
	if chosen != "11111111-1111-4111-8111-111111111111" || len(generated) != 36 {
		t.Fatalf("ids %q %q", chosen, generated)
	}
	row, err := catalog.Get(ctx, chosen)
	if err != nil {
		t.Fatal(err)
	}
	if row.Env != nil || row.ArchivedAt != nil || row.Pinned || row.Main != 1 || len(row.Branches) != 0 || row.Model != model || row.ThinkingLevel != "high" {
		t.Fatalf("row = %+v", row)
	}
	if _, err := catalog.Get(ctx, "missing"); err != ErrSessionNotFound {
		t.Fatalf("missing: %v", err)
	}
	// The same ID twice is the database's refusal.
	if _, err := catalog.Create(ctx, NewSession{ID: chosen, Cwd: "/", Title: "x", Model: model, ThinkingLevel: "high"}); err == nil {
		t.Fatal("a second row under one ID")
	}
}

func TestCatalogListsTheMostRecentlyActiveFirst(t *testing.T) {
	catalog := openCatalog(t, t.TempDir())
	ctx := context.Background()
	older, newer := create(t, catalog, NewSession{}), create(t, catalog, NewSession{})
	future := time.Now().Add(time.Hour).UnixMilli()
	if err := catalog.Activity(ctx, older, future, ""); err != nil {
		t.Fatal(err)
	}
	all, err := catalog.All(ctx)
	if err != nil || len(all) != 2 || all[0].ID != older || all[1].ID != newer {
		t.Fatalf("order %v %v", all, err)
	}
	// Activity never moves time backwards.
	if err := catalog.Activity(ctx, older, 5, ""); err != nil {
		t.Fatal(err)
	}
	row, _ := catalog.Get(ctx, older)
	if row.UpdatedAt != future {
		t.Fatalf("updatedAt = %d, want %d", row.UpdatedAt, future)
	}
}

func TestAnExplicitTitleIsNeverReplacedByAnAutomaticOne(t *testing.T) {
	catalog := openCatalog(t, t.TempDir())
	ctx := context.Background()
	auto := create(t, catalog, NewSession{})
	given := create(t, catalog, NewSession{Title: "Given", TitleEdited: true})
	renamed := create(t, catalog, NewSession{})
	title := "Renamed"
	if _, err := catalog.Update(ctx, renamed, Change{Title: &title}); err != nil {
		t.Fatal(err)
	}
	text := "first line\r\nsecond line "
	for range 120 {
		text += "x"
	}
	for _, id := range []string{auto, given, renamed} {
		if err := catalog.Activity(ctx, id, 1, text); err != nil {
			t.Fatal(err)
		}
	}
	// Line breaks become spaces, and the title is the first 100 characters.
	want := map[string]string{auto: ("first line  second line " + text[len("first line\r\nsecond line "):])[:autoTitle], given: "Given", renamed: "Renamed"}
	for id, title := range want {
		row, _ := catalog.Get(ctx, id)
		if row.Title != title {
			t.Errorf("title of %s = %q, want %q", id, row.Title, title)
		}
	}
	// Once automatic, the next first message replaces it again (the flag is only set by an explicit title).
	if err := catalog.Activity(ctx, auto, 2, "another"); err != nil {
		t.Fatal(err)
	}
	if row, _ := catalog.Get(ctx, auto); row.Title != "another" {
		t.Fatalf("auto title = %q", row.Title)
	}
}

func TestUpdateArchivesRestoresAndMovesWithActivity(t *testing.T) {
	catalog := openCatalog(t, t.TempDir())
	ctx := context.Background()
	id := create(t, catalog, NewSession{})
	yes, no := true, false
	archived, err := catalog.Update(ctx, id, Change{Archived: &yes})
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	// Archiving again keeps the first time.
	again, _ := catalog.Update(ctx, id, Change{Archived: &yes})
	if *again.ArchivedAt != *archived.ArchivedAt {
		t.Fatal("archivedAt moved")
	}
	restored, _ := catalog.Update(ctx, id, Change{Archived: &no})
	if restored.ArchivedAt != nil {
		t.Fatal("not restored")
	}
	// An old row, so that a move's activity shows.
	if _, err := catalog.db.ExecContext(ctx, "UPDATE sessions SET updatedAt=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	before := int64(1)
	pinned, _ := catalog.Update(ctx, id, Change{Pinned: &yes})
	if !pinned.Pinned || pinned.UpdatedAt != before {
		t.Fatalf("a pin is no activity: %+v", pinned)
	}
	cwd, env := "/elsewhere", "box"
	moved, err := catalog.Update(ctx, id, Change{Cwd: &cwd, SetEnv: true, Env: &env})
	if err != nil || moved.Cwd != cwd || moved.Env == nil || *moved.Env != env || moved.UpdatedAt <= before {
		t.Fatalf("move: %+v %v", moved, err)
	}
	local, _ := catalog.Update(ctx, id, Change{SetEnv: true})
	if local.Env != nil {
		t.Fatal("env not cleared")
	}
	main, label := 3, "side"
	branches := []contract.Branch{{Conversation: 1}, {Conversation: 3, Label: &label}}
	tree, err := catalog.Update(ctx, id, Change{Main: &main, Branches: &branches})
	if err != nil || tree.Main != 3 || len(tree.Branches) != 2 || tree.Branches[0].Label != nil || *tree.Branches[1].Label != "side" {
		t.Fatalf("tree: %+v %v", tree, err)
	}
	if _, err := catalog.Update(ctx, "missing", Change{}); err != ErrSessionNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestCatalogMigratesOlderSchemas(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	// Version 1: no env, no titleEdited, no main/branches.
	for _, statement := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL, createdAt INTEGER NOT NULL,
			updatedAt INTEGER NOT NULL, archivedAt INTEGER, pinned INTEGER NOT NULL DEFAULT 0, model TEXT NOT NULL,
			thinkingLevel TEXT NOT NULL) STRICT`,
		`INSERT INTO sessions VALUES ('a','/a','Kept title',1,1,NULL,0,'{"provider":"faux","modelId":"faux-1"}','high')`,
		`INSERT INTO sessions VALUES ('b','/b','New session',1,2,NULL,0,'{"provider":"faux","modelId":"faux-1"}','high')`,
		`PRAGMA user_version=1`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	catalog := openCatalog(t, dir)
	ctx := context.Background()
	a, err := catalog.Get(ctx, "a")
	if err != nil || a.Env != nil || a.Main != 1 || len(a.Branches) != 0 {
		t.Fatalf("a = %+v %v", a, err)
	}
	// A title other than the placeholder was explicit or already automatic: kept.
	_ = catalog.Activity(ctx, "a", 3, "first message")
	_ = catalog.Activity(ctx, "b", 3, "first message")
	if a, _ = catalog.Get(ctx, "a"); a.Title != "Kept title" {
		t.Fatalf("a's title = %q", a.Title)
	}
	if b, _ := catalog.Get(ctx, "b"); b.Title != "first message" {
		t.Fatalf("b's title = %q", b.Title)
	}
}

func TestCatalogRefusesANewerSchema(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := OpenCatalog(context.Background(), dir); err == nil || err.Error() != "Unsupported catalog schema: 99" {
		t.Fatalf("open = %v", err)
	}
}
