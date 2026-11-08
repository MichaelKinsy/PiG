// Package sessions ports apps/p2/plugins/sessions (server and worker parts): the session catalog and its views, the
// session locks, and what a session's worker contributes (the lock mirror, `SessionTransfer`, the `rename_session`
// tool).
package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/google/uuid"
	_ "modernc.org/sqlite" // the catalog's driver
)

// schemaVersion is the catalog's `user_version`: 3 added `titleEdited` (Pi Mobile's: an explicit title, given at
// creation or renamed, is never replaced automatically); 4 `main` and `branches`, the current main conversation and
// the `/tree` branches (docs/tree.md).
const schemaVersion = 4

// autoTitle is the length of an automatic title: the first user message, line breaks as spaces, its first 100
// characters (as Pi Mobile).
const autoTitle = 100

// ErrSessionNotFound is what Get and Update answer for an unknown session.
var ErrSessionNotFound = errors.New("Session not found")

// CatalogDB is the SQLite catalog of the server. Durable owns transcripts; this holds what the sidebar needs without
// workers. Its methods are called from one object's line (Catalog), so they are not safe for concurrent use.
type CatalogDB struct {
	db *sql.DB
}

// OpenCatalog opens (and migrates) `catalog.sqlite` in directory.
func OpenCatalog(ctx context.Context, directory string) (*CatalogDB, error) {
	db, err := sql.Open("sqlite", filepath.Join(directory, "catalog.sqlite"))
	if err != nil {
		return nil, err
	}
	// One connection: the pragmas and the migration are per connection, and the catalog is written from one line.
	db.SetMaxOpenConns(1)
	catalog := &CatalogDB{db: db}
	if err := catalog.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return catalog, nil
}

func (c *CatalogDB) migrate(ctx context.Context) error {
	if _, err := c.db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"); err != nil {
		return err
	}
	var version int
	if err := c.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 0 || version > schemaVersion {
		return fmt.Errorf("Unsupported catalog schema: %d", version)
	}
	statements := []string{`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY, cwd TEXT NOT NULL, env TEXT, title TEXT NOT NULL,
		createdAt INTEGER NOT NULL, updatedAt INTEGER NOT NULL, archivedAt INTEGER,
		pinned INTEGER NOT NULL DEFAULT 0, model TEXT NOT NULL, thinkingLevel TEXT NOT NULL,
		titleEdited INTEGER NOT NULL DEFAULT 0, main INTEGER NOT NULL DEFAULT 1,
		branches TEXT NOT NULL DEFAULT '[]'
	) STRICT`}
	if version == 1 {
		statements = append(statements, "ALTER TABLE sessions ADD COLUMN env TEXT")
	}
	// Before the flag, any title other than the placeholder was explicit or already automatic: keep them all.
	if version == 1 || version == 2 {
		statements = append(statements,
			"ALTER TABLE sessions ADD COLUMN titleEdited INTEGER NOT NULL DEFAULT 0",
			"UPDATE sessions SET titleEdited=1 WHERE title<>'New session'")
	}
	if version != 0 && version < 4 {
		statements = append(statements,
			"ALTER TABLE sessions ADD COLUMN main INTEGER NOT NULL DEFAULT 1",
			"ALTER TABLE sessions ADD COLUMN branches TEXT NOT NULL DEFAULT '[]'")
	}
	statements = append(statements, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
	for _, statement := range statements {
		if _, err := c.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("catalog migration (%s): %w", strings.SplitN(statement, " ", 3)[0], err)
		}
	}
	return nil
}

// Close closes the database.
func (c *CatalogDB) Close() error { return c.db.Close() }

const selectSession = "SELECT id,cwd,env,title,createdAt,updatedAt,archivedAt,pinned,model,thinkingLevel,main,branches FROM sessions"

type scanner interface{ Scan(dest ...any) error }

func decode(row scanner) (contract.Session, error) {
	var (
		s                 contract.Session
		env               sql.NullString
		archived          sql.NullInt64
		pinned            int64
		model, branchJSON string
	)
	if err := row.Scan(&s.ID, &s.Cwd, &env, &s.Title, &s.CreatedAt, &s.UpdatedAt, &archived, &pinned, &model, &s.ThinkingLevel, &s.Main, &branchJSON); err != nil {
		return s, err
	}
	if env.Valid {
		s.Env = &env.String
	}
	if archived.Valid {
		s.ArchivedAt = &archived.Int64
	}
	s.Pinned = pinned == 1
	if err := json.Unmarshal([]byte(model), &s.Model); err != nil {
		return s, fmt.Errorf("session %s model: %w", s.ID, err)
	}
	var branches []struct {
		Conversation int     `json:"conversation"`
		Label        *string `json:"label"`
	}
	if err := json.Unmarshal([]byte(branchJSON), &branches); err != nil {
		return s, fmt.Errorf("session %s branches: %w", s.ID, err)
	}
	s.Branches = make([]contract.Branch, len(branches))
	for i, branch := range branches {
		s.Branches[i] = contract.Branch{Conversation: branch.Conversation, Label: branch.Label}
	}
	return s, nil
}

// All is every session, the most recently active first.
func (c *CatalogDB) All(ctx context.Context) ([]contract.Session, error) {
	rows, err := c.db.QueryContext(ctx, selectSession+" ORDER BY updatedAt DESC, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []contract.Session{}
	for rows.Next() {
		session, err := decode(rows)
		if err != nil {
			return nil, err
		}
		all = append(all, session)
	}
	return all, rows.Err()
}

// Get is session id, or ErrSessionNotFound.
func (c *CatalogDB) Get(ctx context.Context, id string) (contract.Session, error) {
	session, err := decode(c.db.QueryRowContext(ctx, selectSession+" WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return session, ErrSessionNotFound
	}
	return session, err
}

// NewSession is what Create takes.
type NewSession struct {
	// ID is a chosen ID (a clone's or fork's); default a new one.
	ID  string
	Env *string
	Cwd string
	// Title is the session's title; TitleEdited says it was given (not the placeholder): automatic titles never
	// replace it.
	Title         string
	TitleEdited   bool
	Model         durable.ModelRef
	ThinkingLevel string
}

// Create adds a session.
func (c *CatalogDB) Create(ctx context.Context, input NewSession) (contract.Session, error) {
	id := input.ID
	if id == "" {
		id = uuid.NewString()
	}
	model, err := json.Marshal(input.Model)
	if err != nil {
		return contract.Session{}, err
	}
	now := time.Now().UnixMilli()
	var env any
	if input.Env != nil {
		env = *input.Env
	}
	edited := 0
	if input.TitleEdited {
		edited = 1
	}
	if _, err := c.db.ExecContext(ctx,
		"INSERT INTO sessions (id,cwd,env,title,titleEdited,createdAt,updatedAt,model,thinkingLevel) VALUES (?,?,?,?,?,?,?,?,?)",
		id, input.Cwd, env, input.Title, edited, now, now, string(model), input.ThinkingLevel); err != nil {
		return contract.Session{}, err
	}
	return c.Get(ctx, id)
}

// Change is what Update takes: a nil field is unchanged. Env is set with SetEnv (a nil *string sets null).
type Change struct {
	Title    *string
	Cwd      *string
	SetEnv   bool
	Env      *string
	Pinned   *bool
	Archived *bool
	Model    *durable.ModelRef
	Thinking *string
	Main     *int
	Branches *[]contract.Branch
}

// Update changes session id.
func (c *CatalogDB) Update(ctx context.Context, id string, change Change) (contract.Session, error) {
	current, err := c.Get(ctx, id)
	if err != nil {
		return current, err
	}
	archivedAt := current.ArchivedAt
	if change.Archived != nil {
		if !*change.Archived {
			archivedAt = nil
		} else if archivedAt == nil {
			now := time.Now().UnixMilli()
			archivedAt = &now
		}
	}
	// A rename is explicit; a new location counts as activity (Pi Mobile's setLocation).
	moved := change.Cwd != nil || change.SetEnv
	title, cwd, env, pinned := current.Title, current.Cwd, current.Env, current.Pinned
	model, thinking, main, branches := current.Model, current.ThinkingLevel, current.Main, current.Branches
	if change.Title != nil {
		title = *change.Title
	}
	if change.Cwd != nil {
		cwd = *change.Cwd
	}
	if change.SetEnv {
		env = change.Env
	}
	if change.Pinned != nil {
		pinned = *change.Pinned
	}
	if change.Model != nil {
		model = *change.Model
	}
	if change.Thinking != nil {
		thinking = *change.Thinking
	}
	if change.Main != nil {
		main = *change.Main
	}
	if change.Branches != nil {
		branches = *change.Branches
	}
	modelJSON, err := json.Marshal(model)
	if err != nil {
		return current, err
	}
	type storedBranch struct {
		Conversation int     `json:"conversation"`
		Label        *string `json:"label"`
	}
	stored := make([]storedBranch, len(branches))
	for i, branch := range branches {
		stored[i] = storedBranch{branch.Conversation, branch.Label}
	}
	branchJSON, err := json.Marshal(stored)
	if err != nil {
		return current, err
	}
	flag := func(on bool) int {
		if on {
			return 1
		}
		return 0
	}
	var envValue, archivedValue any
	if env != nil {
		envValue = *env
	}
	if archivedAt != nil {
		archivedValue = *archivedAt
	}
	_, err = c.db.ExecContext(ctx,
		"UPDATE sessions SET title=?,titleEdited=CASE WHEN ? THEN 1 ELSE titleEdited END,updatedAt=CASE WHEN ? THEN max(updatedAt,?) ELSE updatedAt END,pinned=?,archivedAt=?,model=?,thinkingLevel=?,cwd=?,env=?,main=?,branches=? WHERE id=?",
		title, flag(change.Title != nil), flag(moved), time.Now().UnixMilli(), flag(pinned), archivedValue, string(modelJSON), thinking, cwd, envValue, main, string(branchJSON), id)
	if err != nil {
		return current, err
	}
	return c.Get(ctx, id)
}

// Activity applies indexed activity: updatedAt never moves back, and an automatic title (the first user message's
// start) replaces the title unless it was edited.
func (c *CatalogDB) Activity(ctx context.Context, id string, timestamp int64, firstUserText string) error {
	title := strings.NewReplacer("\r", " ", "\n", " ").Replace(firstUserText)
	title = utf16Slice(title, autoTitle)
	_, err := c.db.ExecContext(ctx, `UPDATE sessions SET updatedAt=max(updatedAt,?),
		title=CASE WHEN titleEdited=0 AND ?<>'' THEN ? ELSE title END WHERE id=?`, timestamp, title, title, id)
	return err
}

// utf16Slice is JavaScript's `slice(0, n)` of s as the catalog stores it: at most n UTF-16 code units. A surrogate pair
// the limit falls inside is cut in two, and SQLite stores the lone surrogate as U+FFFD (UTF-8 has no form for it), so the
// character becomes U+FFFD.
func utf16Slice(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += width
	}
	return s
}
