package sharing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite" // the share store's driver
)

// Ports apps/p2/plugins/sharing/db.ts: share links, guests and their grants (SQLite). Session state (archived) is checked
// by the caller.
//
// Forced Go differences. `node:sqlite`'s synchronous `DatabaseSync` is database/sql over modernc's SQLite with one
// connection (a transaction holds it, so two handlers never interleave their statements); every call takes a context.

// schemaVersion is `user_version`: 2 separates who a guest is (`guests`) from what was shared with them (`grants`, one per
// session), so that one guest can hold several sessions, and adds guests the owner shared with by email. Version 1 had one
// guest per link.
const schemaVersion = 2

const maxGuestsPerLink = 1000

// modes are the share modes in the order links sort by.
var modes = []string{"view", "full"}

const guestsTable = `CREATE TABLE IF NOT EXISTS guests (
	id TEXT PRIMARY KEY, name TEXT NOT NULL,
	secretHash TEXT, email TEXT UNIQUE,
	revision INTEGER NOT NULL DEFAULT 1, lastSeen INTEGER NOT NULL
) STRICT`

const grantsTable = `CREATE TABLE IF NOT EXISTS grants (
	guest TEXT NOT NULL, session TEXT NOT NULL, mode TEXT NOT NULL, linkId TEXT,
	removed INTEGER NOT NULL DEFAULT 0, grantedAt INTEGER NOT NULL,
	PRIMARY KEY (guest, session)
) STRICT`

func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Link is a share link (contract.LinkSchema).
type Link struct {
	ID      string
	Session string
	Mode    string
	Token   string
}

// GuestRow is a guest: a browser that joined through links (`secretHash` of its cookie), or a person the owner shared
// with by email (`Email`, normalized; Radius authenticates them). Never both.
type GuestRow struct {
	ID       string
	Name     string
	Email    *string
	Revision int64
}

// GrantRow is what one guest may do in one session. LinkID is the link it joined through; nil when shared by email.
type GrantRow struct {
	Session   string
	Mode      string
	LinkID    *string
	Removed   bool
	GrantedAt int64
}

// PersonRow is a session shared with a person by email; Name is their email as the owner wrote it.
type PersonRow struct {
	ID      string
	Name    string
	Session string
	Mode    string
}

// EmailGrant is a grant to a person by email that is not removed: the email (normalized) and the session.
type EmailGrant struct {
	Email   string
	Session string
}

// OpenEmails are the emails of grants on sessions open accepts, each once, sorted.
func OpenEmails(grants []EmailGrant, open func(session string) bool) []string {
	emails := []string{}
	for _, grant := range grants {
		if open(grant.Session) && !slices.Contains(emails, grant.Email) {
			emails = append(emails, grant.Email)
		}
	}
	slices.SortFunc(emails, compareUTF16)
	return emails
}

// compareUTF16 orders strings as JavaScript's default `sort()` does: by UTF-16 code unit.
func compareUTF16(a, b string) int {
	return slices.Compare(utf16.Encode([]rune(a)), utf16.Encode([]rune(b)))
}

// JoinError is a join failure the guest may see.
type JoinError struct{ Message string }

func (err *JoinError) Error() string { return err.Message }

// ShareDB is the shares database (`shares.sqlite`); listeners hear of every committed change of links, guests and grants.
type ShareDB struct {
	db *sql.DB

	mu        sync.Mutex
	next      int
	listeners map[int]func()
}

// OpenShareDB opens (and migrates) `shares.sqlite` in directory, readable by its owner only.
func OpenShareDB(ctx context.Context, directory string) (*ShareDB, error) {
	path := filepath.Join(directory, "shares.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	shares := &ShareDB{db: db, listeners: map[int]func(){}}
	if err := shares.migrate(ctx, path); err != nil {
		_ = db.Close()
		return nil, err
	}
	return shares, nil
}

func (s *ShareDB) migrate(ctx context.Context, path string) error {
	// The file exists once the first statement ran.
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL;"); err != nil {
		return err
	}
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != 1 && version != schemaVersion {
		return fmt.Errorf("Unsupported shares schema: %d", version)
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS links (
			id TEXT PRIMARY KEY, session TEXT NOT NULL, mode TEXT NOT NULL, token TEXT NOT NULL UNIQUE,
			UNIQUE(session, mode)
		) STRICT`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, grantsTable); err != nil {
			return err
		}
		// Version 1: each guest becomes a guest with one grant, the session of its link.
		if version == 1 {
			for _, statement := range []string{
				"ALTER TABLE guests RENAME TO guests_v1",
				guestsTable,
				`INSERT INTO guests(id,name,secretHash,revision,lastSeen)
					SELECT id,name,secretHash,revision,lastSeen FROM guests_v1`,
				`INSERT INTO grants(guest,session,mode,linkId,removed,grantedAt)
					SELECT guests_v1.id,links.session,links.mode,links.id,guests_v1.removed,guests_v1.lastSeen
					FROM guests_v1 JOIN links ON links.id=guests_v1.linkId`,
				"DROP TABLE guests_v1",
			} {
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
		} else if _, err := tx.ExecContext(ctx, guestsTable); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", schemaVersion))
		return err
	})
}

// Subscribe makes listener hear of every committed change from now on; the result stops it.
func (s *ShareDB) Subscribe(listener func()) (stop func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	s.listeners[id] = listener
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}

// transaction runs fn in one transaction, then tells the listeners; fn's error rolls it back.
func (s *ShareDB) transaction(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	listeners := make([]func(), 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
	return nil
}

// querier is what a statement runs on: the database, or the transaction it runs in.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Links are every link, by session then mode.
func (s *ShareDB) Links(ctx context.Context) ([]Link, error) { return linksOf(ctx, s.db) }

func linksOf(ctx context.Context, q querier) ([]Link, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,session,mode,token FROM links")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []Link{}
	for rows.Next() {
		var link Link
		if err := rows.Scan(&link.ID, &link.Session, &link.Mode, &link.Token); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(links, func(a, b Link) int {
		if c := p2runtime.LocaleCompare(a.Session, b.Session); c != 0 {
			return c
		}
		return slices.Index(modes, a.Mode) - slices.Index(modes, b.Mode)
	})
	return links, nil
}

var tokenPattern = regexp.MustCompile(`^[\w-]{43}$`)

// LinkByToken is the link of a token, nil when there is none.
func (s *ShareDB) LinkByToken(ctx context.Context, token string) (*Link, error) {
	if !tokenPattern.MatchString(token) {
		return nil, nil
	}
	links, err := s.Links(ctx)
	if err != nil {
		return nil, err
	}
	for i := range links {
		if links[i].Token == token {
			return &links[i], nil
		}
	}
	return nil, nil
}

func randomString(encode func([]byte) string) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return encode(random), nil
}

// MakeLink creates the session's link of mode, or replaces it. Replacing ends the grants made through the old link;
// it returns the guests whose grants ended.
func (s *ShareDB) MakeLink(ctx context.Context, session, mode string, reset bool) ([]string, error) {
	links, err := s.Links(ctx)
	if err != nil {
		return nil, err
	}
	var existing *Link
	for i := range links {
		if links[i].Session == session && links[i].Mode == mode {
			existing = &links[i]
		}
	}
	if existing != nil && !reset {
		return []string{}, nil
	}
	ended := []string{}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		ended = []string{}
		if existing != nil {
			rows, err := tx.QueryContext(ctx, "SELECT guest FROM grants WHERE linkId=?", existing.ID)
			if err != nil {
				return err
			}
			for rows.Next() {
				var guest string
				if err := rows.Scan(&guest); err != nil {
					_ = rows.Close()
					return err
				}
				ended = append(ended, guest)
			}
			if err := errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM grants WHERE linkId=?", existing.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM links WHERE id=?", existing.ID); err != nil {
				return err
			}
			for _, id := range ended {
				if err := bump(ctx, tx, id); err != nil {
					return err
				}
			}
		}
		token, err := randomString(base64.RawURLEncoding.EncodeToString)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO links(id,session,mode,token) VALUES(?,?,?,?)", uuid.NewString(), session, mode, token)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ended, nil
}

// Guest is the guest of an ID, nil when there is none.
func (s *ShareDB) Guest(ctx context.Context, id string) (*GuestRow, error) {
	return guestOf(ctx, s.db, id)
}

func guestOf(ctx context.Context, q querier, id string) (*GuestRow, error) {
	var guest GuestRow
	var email sql.NullString
	err := q.QueryRowContext(ctx, "SELECT id,name,email,revision FROM guests WHERE id=?", id).Scan(&guest.ID, &guest.Name, &email, &guest.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if email.Valid {
		guest.Email = &email.String
	}
	return &guest, nil
}

// GuestOfCookie is the guest of a cookie value `<id>.<secret>` whose secret matches.
func (s *ShareDB) GuestOfCookie(ctx context.Context, cookie string) (*GuestRow, error) {
	return guestOfCookie(ctx, s.db, cookie)
}

func guestOfCookie(ctx context.Context, q querier, cookie string) (*GuestRow, error) {
	parts := strings.Split(cookie, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" || len(utf16.Encode([]rune(parts[1]))) != 64 {
		return nil, nil
	}
	var stored sql.NullString
	err := q.QueryRowContext(ctx, "SELECT secretHash FROM guests WHERE id=?", parts[0]).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !stored.Valid || subtle.ConstantTimeCompare([]byte(stored.String), []byte(hash(parts[1]))) != 1 {
		return nil, nil
	}
	return guestOf(ctx, q, parts[0])
}

// GuestOfEmail is the guest of a normalized email.
func (s *ShareDB) GuestOfEmail(ctx context.Context, email string) (*GuestRow, error) {
	return guestOfEmail(ctx, s.db, email)
}

func guestOfEmail(ctx context.Context, q querier, email string) (*GuestRow, error) {
	var id string
	err := q.QueryRowContext(ctx, "SELECT id FROM guests WHERE email=?", email).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return guestOf(ctx, q, id)
}

// Grants are a guest's grants, removed ones included.
func (s *ShareDB) Grants(ctx context.Context, id string) ([]GrantRow, error) {
	return grantsOf(ctx, s.db, id)
}

func grantsOf(ctx context.Context, q querier, id string) ([]GrantRow, error) {
	rows, err := q.QueryContext(ctx, "SELECT session,mode,linkId,removed,grantedAt FROM grants WHERE guest=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []GrantRow{}
	for rows.Next() {
		var grant GrantRow
		var link sql.NullString
		var removed int64
		if err := rows.Scan(&grant.Session, &grant.Mode, &link, &removed, &grant.GrantedAt); err != nil {
			return nil, err
		}
		if link.Valid {
			grant.LinkID = &link.String
		}
		grant.Removed = removed == 1
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// Remove blocks a guest's grant of session; true if it had one. A removed browser cannot join that session again.
func (s *ShareDB) Remove(ctx context.Context, session, guestID string) (bool, error) {
	removed := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		removed = false
		result, err := tx.ExecContext(ctx, "UPDATE grants SET removed=1 WHERE guest=? AND session=? AND removed=0", guestID, session)
		if err != nil {
			return err
		}
		changes, err := result.RowsAffected()
		if err != nil || changes == 0 {
			return err
		}
		removed = true
		return bump(ctx, tx, guestID)
	})
	return removed, err
}

// Bump bumps the revision of every guest of session (archive, restore); it returns their IDs.
func (s *ShareDB) Bump(ctx context.Context, session string) ([]string, error) {
	ids := []string{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		ids = []string{}
		rows, err := tx.QueryContext(ctx, "SELECT guest FROM grants WHERE session=?", session)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, id := range ids {
			if err := bump(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}

// Joined is what a join did: the guest, the cookie of a guest made now, and whether an existing guest's grants changed (its
// revision was bumped).
type Joined struct {
	Guest   string
	Cookie  string
	Changed bool
}

// Join joins link: existing (the browser's guest, by its cookie) gains the link's session, else a new guest is created with
// the cleaned name, made unique ("Alice (2)"), and a cookie. Joining a session again takes the link's mode.
func (s *ShareDB) Join(ctx context.Context, link Link, requestedName string, existing *GuestRow) (Joined, error) {
	var joined Joined
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		joined = Joined{}
		var grant *GrantRow
		if existing != nil {
			grants, err := grantsOf(ctx, tx, existing.ID)
			if err != nil {
				return err
			}
			for i := range grants {
				if grants[i].Session == link.Session {
					grant = &grants[i]
					break
				}
			}
		}
		if grant != nil && grant.Removed {
			return &JoinError{"You were removed from this session"}
		}
		if existing != nil && grant != nil && grant.Mode == link.Mode && grant.LinkID != nil && *grant.LinkID == link.ID {
			joined = Joined{Guest: existing.ID}
			return nil
		}
		var count int64
		if err := tx.QueryRowContext(ctx, "SELECT count(*) AS n FROM grants WHERE linkId=?", link.ID).Scan(&count); err != nil {
			return err
		}
		if count >= maxGuestsPerLink {
			return &JoinError{"Guest limit reached"}
		}
		var id, cookie string
		if existing != nil {
			id = existing.ID
		} else {
			id = uuid.NewString()
			secret, err := randomString(hex.EncodeToString)
			if err != nil {
				return err
			}
			name, err := uniqueName(ctx, tx, requestedName)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO guests(id,name,secretHash,lastSeen) VALUES(?,?,?,?)", id, name, hash(secret), time.Now().UnixMilli()); err != nil {
				return err
			}
			cookie = id + "." + secret
		}
		if err := grantTo(ctx, tx, id, link.Session, link.Mode, &link.ID); err != nil {
			return err
		}
		if existing != nil {
			if err := bump(ctx, tx, id); err != nil {
				return err
			}
		}
		joined = Joined{Guest: id, Cookie: cookie, Changed: existing != nil}
		return nil
	})
	return joined, err
}

// Shared is what sharing with a person did: the guest, and whether an existing guest changed.
type Shared struct {
	Guest   string
	Changed bool
}

// Share shares session with the person of email (normalized; name is how the owner wrote it), creating their guest first,
// and gives a removed person access again.
func (s *ShareDB) Share(ctx context.Context, session, email, name, mode string) (Shared, error) {
	var shared Shared
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		existing, err := guestOfEmail(ctx, tx, email)
		if err != nil {
			return err
		}
		var id string
		if existing != nil {
			id = existing.ID
		} else {
			id = uuid.NewString()
			if _, err := tx.ExecContext(ctx, "INSERT INTO guests(id,name,email,lastSeen) VALUES(?,?,?,?)", id, name, email, 0); err != nil {
				return err
			}
		}
		if err := grantTo(ctx, tx, id, session, mode, nil); err != nil {
			return err
		}
		if existing != nil {
			if err := bump(ctx, tx, id); err != nil {
				return err
			}
		}
		shared = Shared{Guest: id, Changed: existing != nil}
		return nil
	})
	return shared, err
}

// People are every grant to a person by email that is not removed, by name.
func (s *ShareDB) People(ctx context.Context) ([]PersonRow, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT guests.id,guests.name,grants.session,grants.mode FROM grants JOIN guests ON guests.id=grants.guest WHERE grants.removed=0 AND guests.email IS NOT NULL ORDER BY guests.name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := []PersonRow{}
	for rows.Next() {
		var person PersonRow
		if err := rows.Scan(&person.ID, &person.Name, &person.Session, &person.Mode); err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	return people, rows.Err()
}

// EmailGrants are every grant to a person by email that is not removed.
func (s *ShareDB) EmailGrants(ctx context.Context) ([]EmailGrant, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT guests.email,grants.session FROM grants JOIN guests ON guests.id=grants.guest WHERE grants.removed=0 AND guests.email IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []EmailGrant{}
	for rows.Next() {
		var grant EmailGrant
		if err := rows.Scan(&grant.Email, &grant.Session); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// Emails is every email with a grant that is not removed, on a session open accepts; sorted.
func (s *ShareDB) Emails(ctx context.Context, open func(session string) bool) ([]string, error) {
	grants, err := s.EmailGrants(ctx)
	if err != nil {
		return nil, err
	}
	return OpenEmails(grants, open), nil
}

// Seen records that a guest was seen now.
func (s *ShareDB) Seen(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE guests SET lastSeen=? WHERE id=?", time.Now().UnixMilli(), id)
	return err
}

// Close closes the database.
func (s *ShareDB) Close() error { return s.db.Close() }

func grantTo(ctx context.Context, q querier, guest, session, mode string, linkID *string) error {
	var link any
	if linkID != nil {
		link = *linkID
	}
	_, err := q.ExecContext(ctx,
		"INSERT INTO grants(guest,session,mode,linkId,removed,grantedAt) VALUES(?,?,?,?,0,?) ON CONFLICT(guest,session) DO UPDATE SET mode=excluded.mode, linkId=excluded.linkId, removed=0, grantedAt=excluded.grantedAt",
		guest, session, mode, link, time.Now().UnixMilli())
	return err
}

func bump(ctx context.Context, q querier, guest string) error {
	_, err := q.ExecContext(ctx, "UPDATE guests SET revision=revision+1 WHERE id=?", guest)
	return err
}

// strippedName is what the cleaned name drops: control, format and unassigned characters (V8's \p{C}) and angle brackets.
var strippedName = regexp.MustCompile(`[\p{C}<>]`)

const maxNameUnits = 40

// uniqueName is the cleaned name made unique among the guests ("Alice (2)").
func uniqueName(ctx context.Context, q querier, requested string) (string, error) {
	name := jsstring.Trim(strippedName.ReplaceAllString(norm.NFKC.String(requested), ""))
	name = sliceUnits(name, maxNameUnits)
	if name == "" {
		return "", &JoinError{"Choose a guest name"}
	}
	var count int64
	if err := q.QueryRowContext(ctx, "SELECT count(*) AS n FROM guests WHERE name=? OR name LIKE ?", name, name+" (%)").Scan(&count); err != nil {
		return "", err
	}
	if count > 0 {
		return fmt.Sprintf("%s (%d)", name, count+1), nil
	}
	return name, nil
}

// sliceUnits is `slice(0, n)`: at most n UTF-16 code units. A surrogate pair the limit falls inside is cut in two, and
// SQLite stores the lone surrogate as U+FFFD (UTF-8 has no form for it).
func sliceUnits(text string, n int) string {
	units := 0
	for i, r := range text {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return text[:i] + "\uFFFD"
			}
			return text[:i]
		}
		units += width
	}
	return text
}
