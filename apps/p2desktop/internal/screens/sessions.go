// Package screens holds the native client's screens as state and actions: what each reads from its Chord scope and what
// it calls, with no drawing. A view (shell/mygoshell/native) is a function of what these return, so each is tested
// against the real in-process p2 server on any machine, without a display.
package screens

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
)

// Row is one session as the sessions screen shows it: the catalog row, its worker's status (the `p2.session-status` reader)
// and the lifecycle operation on it (`Sessions.operations`), as the web drawer's SessionRow reads them.
type Row struct {
	sessionscontract.Session
	// Location is where the session's tools run: its directory (home shortened), prefixed by its environment.
	Location string
	// Busy: a conversation of the session has foreground work.
	Busy bool
	// Error is why its worker failed ("": none).
	Error string
	// Operation is what a lifecycle operation says ("Moving…", "Clone failed"); OperationID names it (to dismiss a failed one).
	Operation       string
	OperationFailed bool
	OperationID     string
	OperationError  string
}

// Group is a header of the list: a location and how many sessions it holds.
type Group struct {
	Label string
	Env   *string
	Cwd   string
	Count int
}

// Item is one line of the sessions list: a group's header (Group set) or a session (Row set).
type Item struct {
	Group *Group
	Row   *Row
}

// ShortPath is the web's shortPath: the home directory as `~`.
func ShortPath(path string) string {
	const termux = "/data/data/com.termux/files/home"
	if strings.HasPrefix(path, termux) {
		return "~" + path[len(termux):]
	}
	if strings.HasPrefix(path, "/home/") {
		rest := path[len("/home/"):]
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return "~" + rest[slash:]
		}
		return "~"
	}
	return path
}

// LocationLabel is the web's locationLabel: `env:cwd` for an environment, else the short path.
func LocationLabel(env *string, cwd string) string {
	if env != nil && *env != "" {
		return *env + ":" + cwd
	}
	return ShortPath(cwd)
}

// Ago is the age of a time as the drawer writes it: now, 5m, 3h, 2d, then the date.
func Ago(then, now time.Time) string {
	seconds := now.Sub(then).Seconds()
	switch {
	case seconds < 60:
		return "now"
	case seconds < 3600:
		return itoa(int(seconds/60)) + "m"
	case seconds < 86400:
		return itoa(int(seconds/3600)) + "h"
	case seconds < 604800:
		return itoa(int(seconds/86400)) + "d"
	}
	return then.Format("Jan 2")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for ; n > 0; n /= 10 {
		i--
		digits[i] = byte('0' + n%10)
	}
	return string(digits[i:])
}

var operationLabels = map[string][2]string{
	"relocate": {"Moving…", "Move failed"},
	"clone":    {"Cloning…", "Clone failed"},
	"fork":     {"Forking…", "Fork failed"},
	"archive":  {"Archiving…", "Archive failed"},
}

// operation is one entry of `Sessions.operations`.
type operation struct {
	phase, id, kind, session, target, err string
	copying                               bool
}

func operationsOf(value any) []operation {
	list, _ := value.([]any)
	out := make([]operation, 0, len(list))
	for _, each := range list {
		o, _ := each.(*delta.JsonObject)
		if o == nil {
			continue
		}
		op := operation{phase: str(o.Value("phase")), id: str(o.Value("id")), kind: str(o.Value("kind")), session: str(o.Value("session")),
			target: str(o.Value("target")), err: str(o.Value("error"))}
		op.copying, _ = o.Value("copying").(bool)
		out = append(out, op)
	}
	return out
}

func str(v any) string { s, _ := v.(string); return s }

// status is one session's entry of `p2.session-status`'s statuses.
type status struct {
	busy  bool
	error string
}

func statusesOf(value any) map[string]status {
	all, _ := value.(*delta.JsonObject)
	if all == nil {
		return nil
	}
	out := make(map[string]status, len(all.Keys()))
	for _, id := range all.Keys() {
		entry, _ := all.Value(id).(*delta.JsonObject)
		if entry == nil {
			continue
		}
		busy, _ := entry.Value("busy").([]any)
		out[id] = status{busy: len(busy) > 0, error: str(entry.Value("error"))}
	}
	return out
}

// operationOf is what a session's row says about operations, as the web's operationOf: a failed one of it first (with Dismiss),
// else one running on it, else a copy into it whose files are still being copied.
func operationOf(ops []operation, session string) (label string, failed bool, id, why string) {
	for _, op := range ops {
		if op.phase == "failed" && op.session == session {
			return operationLabels[op.kind][1], true, op.id, op.err
		}
	}
	for _, op := range ops {
		if op.session == session {
			return operationLabels[op.kind][0], false, op.id, ""
		}
	}
	for _, op := range ops {
		if op.phase == "running" && op.target == session && op.copying {
			return "Copying files…", false, op.id, ""
		}
	}
	return "", false, "", ""
}

// BuildItems is the sessions list for the state it is given, a pure function: sessions of the archive or not (archived),
// matching query (title or location, case-insensitive), grouped by location, the most recently active location first,
// pinned sessions first inside a group and then the most recently updated, as the web drawer orders them.
func BuildItems(sessions []sessionscontract.Session, statuses map[string]status, ops []operation, query string, archived bool) []Item {
	query = strings.ToLower(strings.TrimSpace(query))
	type group struct {
		label  string
		env    *string
		cwd    string
		rows   []Row
		latest int64
	}
	groups := map[string]*group{}
	var order []*group
	for _, s := range sessions {
		if (s.ArchivedAt != nil) != archived {
			continue
		}
		location := LocationLabel(s.Env, s.Cwd)
		if query != "" && !strings.Contains(strings.ToLower(s.Title), query) && !strings.Contains(strings.ToLower(location), query) {
			continue
		}
		g := groups[location]
		if g == nil {
			g = &group{label: location, env: s.Env, cwd: s.Cwd}
			groups[location] = g
			order = append(order, g)
		}
		row := Row{Session: s, Location: location}
		if st, ok := statuses[s.ID]; ok {
			row.Busy, row.Error = st.busy, st.error
		}
		row.Operation, row.OperationFailed, row.OperationID, row.OperationError = operationOf(ops, s.ID)
		g.rows = append(g.rows, row)
		if s.UpdatedAt > g.latest {
			g.latest = s.UpdatedAt
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].latest > order[j].latest })
	var items []Item
	for _, g := range order {
		sort.SliceStable(g.rows, func(i, j int) bool {
			if g.rows[i].Pinned != g.rows[j].Pinned {
				return g.rows[i].Pinned
			}
			return g.rows[i].UpdatedAt > g.rows[j].UpdatedAt
		})
		items = append(items, Item{Group: &Group{Label: g.label, Env: g.env, Cwd: g.cwd, Count: len(g.rows)}})
		for i := range g.rows {
			items = append(items, Item{Row: &g.rows[i]})
		}
	}
	return items
}

// Sessions is the sessions screen: the catalog as the owner sees it, every session's live status, the lifecycle
// operations, and the owner's actions, each shown at once on the rows (an optimistic edit) until its call answers.
type Sessions struct {
	scope   *tui.TuiScope
	ref     *host.Reference
	ctx     context.Context
	rows    func() ([]sessionscontract.Session, bool)
	shown   func() []sessionscontract.Session
	add     tui.OptimisticAdd[rowGuess]
	pending func() bool
	status  func() (delta.JsonValue, bool)
	ops     func() (delta.JsonValue, bool)
	cwd     func() (delta.JsonValue, bool)

	mu      sync.Mutex
	lastErr error
	// raw memo of the catalog conversion: the rows of the array last read.
	memoKey []any
	memoVal []sessionscontract.Session
}

// rowGuess is a change to one session's row while its call is in flight.
type rowGuess struct {
	id     string
	change func(*sessionscontract.Session)
}

// OpenSessions holds the sessions screen's references on scope: Sessions (the owner's variant), SessionStatus.
func OpenSessions(h *surfacehost.Host, scope *tui.TuiScope) (*Sessions, error) {
	ref, err := scope.Hold(sessionscontract.Sessions, delta.NewJsonObject(0))
	if err != nil {
		return nil, err
	}
	statusRef, err := scope.Hold(statuscontract.SessionStatus, delta.NewJsonObject(0))
	if err != nil {
		return nil, err
	}
	s := &Sessions{scope: scope, ref: ref, ctx: h.Context()}
	raw := tui.WatchMember(scope, ref.Member("sessions"))
	s.rows = func() ([]sessionscontract.Session, bool) {
		value, ok := raw()
		if !ok {
			return nil, false
		}
		list, _ := value.([]any)
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(list) == len(s.memoKey) && (len(list) == 0 || &list[0] == &s.memoKey[0]) && s.memoVal != nil {
			return s.memoVal, true
		}
		out := make([]sessionscontract.Session, len(list))
		for i, each := range list {
			out[i] = sessionscontract.SessionFromJSON(each)
		}
		s.memoKey, s.memoVal = list, out
		return out, true
	}
	s.shown, s.add, s.pending = tui.Optimistic(scope, func() []sessionscontract.Session { rows, _ := s.rows(); return rows }, applyGuess)
	s.status = tui.WatchMember(scope, statusRef.Member("statuses"))
	s.ops = tui.WatchMember(scope, ref.Member("operations"))
	s.cwd = tui.WatchMember(scope, ref.Member("defaultCwd"))
	return s, nil
}

func applyGuess(rows []sessionscontract.Session, guess rowGuess) []sessionscontract.Session {
	out := make([]sessionscontract.Session, len(rows))
	copy(out, rows)
	for i := range out {
		if out[i].ID == guess.id {
			guess.change(&out[i])
		}
	}
	return out
}

// Ready says the catalog has arrived.
func (s *Sessions) Ready() bool { _, ok := s.rows(); return ok }

// Count is how many sessions the catalog holds (archived included).
func (s *Sessions) Count() int { return len(s.shown()) }

// Items is the list for query and archived.
func (s *Sessions) Items(query string, archived bool) []Item {
	var statuses map[string]status
	if v, ok := s.status(); ok {
		statuses = statusesOf(v)
	}
	var ops []operation
	if v, ok := s.ops(); ok {
		ops = operationsOf(v)
	}
	return BuildItems(s.shown(), statuses, ops, query, archived)
}

// DefaultCwd is the directory suggested for a new session ("": not known yet).
func (s *Sessions) DefaultCwd() string {
	v, _ := s.cwd()
	return str(v)
}

// LastError is why the last action was refused (nil: none); ClearError forgets it.
func (s *Sessions) LastError() error { s.mu.Lock(); defer s.mu.Unlock(); return s.lastErr }

// ClearError forgets the last error.
func (s *Sessions) ClearError() { s.mu.Lock(); s.lastErr = nil; s.mu.Unlock() }

func (s *Sessions) fail(err error) {
	if err != nil {
		s.mu.Lock()
		s.lastErr = err
		s.mu.Unlock()
	}
}

// act shows change on row id at once, calls method, and records its refusal.
func (s *Sessions) act(id string, change func(*sessionscontract.Session), method string, args delta.JsonValue) error {
	err := s.add(rowGuess{id: id, change: change}, func() error {
		_, err := s.ref.Call(method, args, s.ctx)
		return err
	})
	s.fail(err)
	return err
}

// Rename sets a session's title.
func (s *Sessions) Rename(id, title string) error {
	return s.act(id, func(r *sessionscontract.Session) { r.Title = title }, "rename", delta.JsonObjectOf("id", id, "title", title))
}

// Pin pins or unpins a session.
func (s *Sessions) Pin(id string, pinned bool) error {
	return s.act(id, func(r *sessionscontract.Session) { r.Pinned = pinned }, "pin", delta.JsonObjectOf("id", id, "pinned", pinned))
}

// Archive archives a session.
func (s *Sessions) Archive(id string) error {
	now := time.Now().UnixMilli()
	return s.act(id, func(r *sessionscontract.Session) { r.ArchivedAt = &now }, "archive", delta.JsonObjectOf("id", id))
}

// Restore brings an archived session back.
func (s *Sessions) Restore(id string) error {
	return s.act(id, func(r *sessionscontract.Session) { r.ArchivedAt = nil }, "restore", delta.JsonObjectOf("id", id))
}

// Clone copies a session as it is now.
func (s *Sessions) Clone(id string) error {
	_, err := s.ref.Call("clone", delta.JsonObjectOf("id", id), s.ctx)
	s.fail(err)
	return err
}

// Dismiss removes a failed operation's line.
func (s *Sessions) Dismiss(operation string) error {
	_, err := s.ref.Call("dismissOperation", delta.JsonObjectOf("operation", operation), s.ctx)
	s.fail(err)
	return err
}

// Create makes a session in cwd (title may be empty), and returns its ID.
func (s *Sessions) Create(cwd, title string) (string, error) {
	args := delta.JsonObjectOf("cwd", cwd)
	if title != "" {
		args.Set("title", title)
	}
	created, err := s.ref.Call("create", args, s.ctx)
	if err != nil {
		s.fail(err)
		return "", err
	}
	row, _ := created.(*delta.JsonObject)
	if row == nil {
		return "", nil
	}
	return str(row.Value("id")), nil
}

// Pending says an action is in flight.
func (s *Sessions) Pending() bool { return s.pending() }
