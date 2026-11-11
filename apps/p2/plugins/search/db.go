// Package search is p2's search plugin (apps/p2/plugins/search): the message index (SQLite FTS5), the workers' feed into it,
// owner search over sessions and messages, and unread answers.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	_ "modernc.org/sqlite" // the index's driver (FTS5 included)
)

// Ports apps/p2/plugins/search/db.ts: the message index (SQLite FTS5): user/assistant text and catch-up cursors per
// conversation.
//
// Forced Go differences. `node:sqlite`'s synchronous `DatabaseSync` is database/sql over modernc's SQLite with one
// connection (a transaction holds it, so two handlers never interleave their statements); every call takes a context.

// schemaVersion is bumped when the schema or what is indexed changes: the index is dropped and rebuilt by catch-up.
const schemaVersion = 2

// maxSafeInteger is `Number.MAX_SAFE_INTEGER`.
const maxSafeInteger = 1<<53 - 1

// MessageHit is a message match: a bounded snippet around it.
type MessageHit struct {
	Session        string
	ConversationID int
	EntryID        int
	Role           string
	Text           string
}

// ConversationHit is a match inside one conversation.
type ConversationHit struct {
	EntryID int
	Role    string
	Snippet string
}

// IndexedMessage is one committed user or assistant message of a session conversation, as indexed.
type IndexedMessage struct {
	ConversationID int
	EntryID        int
	Role           string
	Timestamp      float64
	Text           string
}

// DB is the index (`search.sqlite`).
type DB struct{ db *sql.DB }

// Open opens (and migrates) `search.sqlite` in directory, readable by its owner only.
func Open(ctx context.Context, directory string) (*DB, error) {
	path := filepath.Join(directory, "search.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	opened := &DB{db: db}
	if err := opened.migrate(ctx, path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return opened, nil
}

func (d *DB) migrate(ctx context.Context, path string) error {
	// The file exists once the first statement ran.
	if err := d.db.PingContext(ctx); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if _, err := d.db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"); err != nil {
		return err
	}
	var version int
	if err := d.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	// Derived data: an unknown version is dropped, and every session's cursor starts again at 0.
	if version != schemaVersion {
		if _, err := d.db.ExecContext(ctx, `
			DROP TRIGGER IF EXISTS messages_insert; DROP TRIGGER IF EXISTS messages_delete;
			DROP TABLE IF EXISTS message_search; DROP TABLE IF EXISTS messages; DROP TABLE IF EXISTS cursors;`); err != nil {
			return err
		}
	}
	if _, err := d.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS messages (
			rowid INTEGER PRIMARY KEY, session TEXT NOT NULL, conversationId INTEGER NOT NULL,
			entryId INTEGER NOT NULL, role TEXT NOT NULL, timestamp INTEGER NOT NULL, text TEXT NOT NULL,
			UNIQUE(session, conversationId, entryId)
		) STRICT;
		CREATE TABLE IF NOT EXISTS cursors (
			session TEXT NOT NULL, conversationId INTEGER NOT NULL, through INTEGER NOT NULL,
			PRIMARY KEY(session, conversationId)
		) STRICT;
		CREATE VIRTUAL TABLE IF NOT EXISTS message_search USING fts5(text, content='messages', content_rowid='rowid');
		CREATE TRIGGER IF NOT EXISTS messages_insert AFTER INSERT ON messages BEGIN
			INSERT INTO message_search(rowid,text) VALUES (new.rowid,new.text);
		END;
		CREATE TRIGGER IF NOT EXISTS messages_delete AFTER DELETE ON messages BEGIN
			INSERT INTO message_search(message_search,rowid,text) VALUES ('delete',old.rowid,old.text);
		END;
		PRAGMA user_version=%d;`, schemaVersion)); err != nil {
		return err
	}
	// Not derived: what the owner has read survives rebuilds of the index (entry IDs are stable). Answers from before the
	// table existed count as read: unread starts with the next answer, not with every old session.
	var existing int
	fresh := d.db.QueryRowContext(ctx, "SELECT 1 FROM sqlite_master WHERE type='table' AND name='reads'").Scan(&existing) == sql.ErrNoRows
	if _, err := d.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS reads (session TEXT PRIMARY KEY, through INTEGER NOT NULL) STRICT;"); err != nil {
		return err
	}
	if fresh {
		_, err := d.db.ExecContext(ctx, "INSERT INTO reads(session,through) SELECT session, max(entryId) FROM messages WHERE conversationId=1 AND role='assistant' GROUP BY session")
		return err
	}
	return nil
}

func (d *DB) transaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Cursor is the entry ID up to which the conversation is indexed (0: nothing).
func (d *DB) Cursor(ctx context.Context, session string, conversation int) (int, error) {
	var through int
	err := d.db.QueryRowContext(ctx, "SELECT through FROM cursors WHERE session=? AND conversationId=?", session, conversation).Scan(&through)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return through, err
}

// Index inserts the messages (an entry already indexed is kept) and advances the cursor, atomically.
func (d *DB) Index(ctx context.Context, session string, conversation int, messages []IndexedMessage, through int) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		insert, err := tx.PrepareContext(ctx, `INSERT INTO messages (session,conversationId,entryId,role,timestamp,text)
			VALUES (?,?,?,?,?,?) ON CONFLICT(session,conversationId,entryId) DO NOTHING`)
		if err != nil {
			return err
		}
		defer insert.Close()
		for _, message := range messages {
			if _, err := insert.ExecContext(ctx, session, message.ConversationID, message.EntryID, message.Role, int64(message.Timestamp), message.Text); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO cursors(session,conversationId,through) VALUES(?,?,?)
			ON CONFLICT(session,conversationId) DO UPDATE SET through=max(through,excluded.through)`, session, conversation, through)
		return err
	})
}

// FirstUserText is the text of the session's first root user message, "" and false when it has none.
func (d *DB) FirstUserText(ctx context.Context, session string) (string, bool, error) {
	var text string
	err := d.db.QueryRowContext(ctx, "SELECT text FROM messages WHERE session=? AND conversationId=1 AND role='user' ORDER BY entryId LIMIT 1", session).Scan(&text)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return text, err == nil, err
}

func (d *DB) counts(ctx context.Context, query string) (map[string]int, []string, error) {
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	var order []string
	for rows.Next() {
		var session string
		var value int
		if err := rows.Scan(&session, &value); err != nil {
			return nil, nil, err
		}
		counts[session] = value
		order = append(order, session)
	}
	return counts, order, rows.Err()
}

// Answers is each session's latest indexed answer: the highest assistant entry ID of its root conversation.
func (d *DB) Answers(ctx context.Context) (map[string]int, []string, error) {
	return d.counts(ctx, "SELECT session, max(entryId) AS entryId FROM messages WHERE conversationId=1 AND role='assistant' GROUP BY session")
}

// Reads is up to which root entry the owner has read each session.
func (d *DB) Reads(ctx context.Context) (map[string]int, []string, error) {
	return d.counts(ctx, "SELECT session, through FROM reads")
}

// MarkRead records that the owner has read session through `through` (never moves back).
func (d *DB) MarkRead(ctx context.Context, session string, through int) error {
	_, err := d.db.ExecContext(ctx, "INSERT INTO reads(session,through) VALUES(?,?) ON CONFLICT(session) DO UPDATE SET through=max(through,excluded.through)", session, through)
	return err
}

// Reset drops a session's messages and cursors.
func (d *DB) Reset(ctx context.Context, session string) error {
	return d.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE session=?", session); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM cursors WHERE session=?", session)
		return err
	})
}

func roleOf(role string) string {
	if role == "user" {
		return "user"
	}
	return "assistant"
}

// Messages are the best matches of every literal term (never raw FTS syntax) in sessions; bounded snippets, at most 41.
func (d *DB) Messages(ctx context.Context, query string, sessions []string) ([]MessageHit, error) {
	terms := strings.FieldsFunc(query, jsstring.IsSpace)
	if len(terms) == 0 || len(sessions) == 0 {
		return nil, nil
	}
	quoted := make([]string, len(terms))
	for at, term := range terms {
		quoted[at] = `"` + strings.ReplaceAll(term, `"`, `""`) + `"`
	}
	encoded, err := json.Marshal(sessions)
	if err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT m.session,m.conversationId,m.entryId,m.role,substr(snippet(message_search,0,'','','…',24),1,300) AS text
		FROM message_search JOIN messages m ON m.rowid=message_search.rowid
		WHERE message_search MATCH ? AND m.session IN (SELECT value FROM json_each(?))
		ORDER BY rank,m.entryId DESC LIMIT 41`, strings.Join(quoted, " AND "), string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []MessageHit
	for rows.Next() {
		var hit MessageHit
		if err := rows.Scan(&hit.Session, &hit.ConversationID, &hit.EntryID, &hit.Role, &hit.Text); err != nil {
			return nil, err
		}
		hit.Role = roleOf(hit.Role)
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

// Mentions are the sessions with a message matching the MATCH expression match (see FTSQuery) at or after since, each with
// the time of its latest one: one query over every session, never their transcripts.
func (d *DB) Mentions(ctx context.Context, match string, since float64) (map[string]float64, []string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT m.session, max(m.timestamp) AS at FROM message_search JOIN messages m ON m.rowid=message_search.rowid
		WHERE message_search MATCH ? AND m.timestamp >= ? GROUP BY m.session`, match, since)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	latest := map[string]float64{}
	var order []string
	for rows.Next() {
		var session string
		var at float64
		if err := rows.Scan(&session, &at); err != nil {
			return nil, nil, err
		}
		latest[session] = at
		order = append(order, session)
	}
	return latest, order, rows.Err()
}

// ConversationMessages are the matches of the MATCH expression match (see FTSQuery) in one conversation, below entry before
// (nil: all): session, conversation and the bound are applied before ranking and the limit, so other sessions' or later
// entries never take a place. At most limit, best first; more when there were further matches.
func (d *DB) ConversationMessages(ctx context.Context, session string, conversation int, match string, before *int, limit int) (hits []ConversationHit, more bool, err error) {
	bound := int64(maxSafeInteger)
	if before != nil {
		bound = int64(*before)
	}
	rows, err := d.db.QueryContext(ctx, `SELECT m.entryId,m.role,substr(snippet(message_search,0,'','','…',24),1,300) AS snippet
		FROM message_search JOIN messages m ON m.rowid=message_search.rowid
		WHERE message_search MATCH ? AND m.session=? AND m.conversationId=? AND m.entryId<?
		ORDER BY rank,m.entryId DESC LIMIT ?`, match, session, conversation, bound, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var hit ConversationHit
		if err := rows.Scan(&hit.EntryID, &hit.Role, &hit.Snippet); err != nil {
			return nil, false, err
		}
		hit.Role = roleOf(hit.Role)
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(hits) > limit {
		return hits[:limit], true, nil
	}
	return hits, false, nil
}

// Close closes the database.
func (d *DB) Close() error { return d.db.Close() }
