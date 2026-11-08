// Package tui is the sessions screen: pi's `/resume` session selector (coding-agent interactive/components/session-selector.ts) on p2's data, with the
// adaptations of p2-tui-port.md section 1.17:
//   - one line per session (activity marker, title, env:cwd in the "all" scope, age); Tab switches the current env:cwd / all sessions; the marker is the web
//     drawer's dot: working, a failed worker, else an unread answer (owner); the chat showing a session's main conversation reads it through its latest answer,
//     as the web does;
//   - local fuzzy filtering over title, env:cwd and ID; message hits come from `Search` (owner), in a separate "Messages" group of literal matches; pi's phrases
//     and `re:` are unsupported (the hint says so);
//   - sorts: threaded (sessions with their subagent conversations nested, `Subagents`), recent, relevance;
//   - pi's named filter is a pinned filter; delete means archive (confirmed), restored from the archived scope (Ctrl+A);
//   - rename and pin (owner). Selecting a subagent opens its conversation: the (session, conversation) identity.
//
// `+ New session` asks for a working directory, Tab choosing the environment; `/new [env:]path` creates one directly; `/cd [env:]path` relocates the viewed
// session (the web's Relocate); `/name`, `/session` live here and in the status plugin. With projects (`projects.json`, owner) and an empty search, the list is
// grouped: each project's row (its places probed in the background; Enter creates a session in the project at the first reachable place, or the one picked with
// left/right) with its sessions below it, then "Other" ("+ New session" and the sessions of no project). The grouped list shows every session; Tab's scope
// (current folder / all) applies to the flat lists. `/move` moves the viewed session within its project (move.go).
//
// Ports apps/p2/plugins/sessions/tui.ts.
//
// Forced Go differences: every method runs on the owner loop; calls and waits run on goroutines that post their continuation with TuiContext.Post. The rows are
// computed again at each render and input (Pi's `rebuild`), from what the followed members last gave.
package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	projectscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	searchcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Slot is the screen's slot name.
const Slot = "sessions"

type sortMode string

const (
	sortThreaded  sortMode = "threaded"
	sortRecent    sortMode = "recent"
	sortRelevance sortMode = "relevance"
)

var sorts = []sortMode{sortThreaded, sortRecent, sortRelevance}

type rowKind int

const (
	rowProject rowKind = iota
	rowNew
	rowSession
	rowChild
	rowHeading
	rowMessage
)

// row is one line of the list.
type row struct {
	kind    rowKind
	project projectscontract.Project
	prefix  string
	indent  string
	text    string
	isError bool
	session contract.Session
	child   runtime.SessionChild
	hit     runtime.SessionHit
}

// now is the clock of the ages.
var now = time.Now

// age is pi's `formatSessionDate`.
func age(timestamp int64) string {
	minutes := (now().UnixMilli() - timestamp) / 60000
	if minutes < 1 {
		return "now"
	}
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh", hours)
	}
	days := hours / 24
	switch {
	case days < 7:
		return fmt.Sprintf("%dd", days)
	case days < 30:
		return fmt.Sprintf("%dw", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dy", days/365)
}

func where(session contract.Session) string {
	if session.Env != nil && *session.Env != "" {
		return *session.Env + ":" + session.Cwd
	}
	return session.Cwd
}

// operation is a lifecycle operation of the catalog (`Sessions.operations`).
type operation struct {
	id, kind, phase, session, target, err string
	copying                               bool
}

type operationText struct{ running, failed, done string }

// operationTexts: how operations read in the selector and on status lines, by kind.
var operationTexts = map[string]operationText{
	"relocate": {"moving…", "Move failed", "Session moved"},
	"clone":    {"cloning…", "Clone failed", "Clone ready"},
	"fork":     {"forking…", "Fork failed", "Fork ready"},
	"archive":  {"archiving…", "Archive failed", "Session archived"},
}

// rowGuess is a change to one session's row while its call is in flight (a rename, a pin, an archive or restore, a move).
type rowGuess struct {
	id         string
	title      *string
	pinned     *bool
	archivedAt **int64
	location   *p2runtime.LocationInput
}

func applyRow(rows []contract.Session, guess rowGuess) []contract.Session {
	out := make([]contract.Session, len(rows))
	for i, each := range rows {
		if each.ID == guess.id {
			if guess.title != nil {
				each.Title = *guess.title
			}
			if guess.pinned != nil {
				each.Pinned = *guess.pinned
			}
			if guess.archivedAt != nil {
				each.ArchivedAt = *guess.archivedAt
			}
			if guess.location != nil {
				each.Env, each.Cwd = guess.location.Env, guess.location.Path
			}
		}
		out[i] = each
	}
	return out
}

func sessionOf(value any) contract.Session { return contract.SessionFromJSON(value) }

func operationOf(value any) operation {
	op := operation{
		id: p2tui.JStr(p2tui.JField(value, "id")), kind: p2tui.JStr(p2tui.JField(value, "kind")), phase: p2tui.JStr(p2tui.JField(value, "phase")),
		session: p2tui.JStr(p2tui.JField(value, "session")), target: p2tui.JStr(p2tui.JField(value, "target")),
		err: p2tui.JStr(p2tui.JField(value, "error")), copying: p2tui.JBool(p2tui.JField(value, "copying")),
	}
	return op
}

func projectOf(value any) projectscontract.Project {
	project := projectscontract.Project{Name: p2tui.JStr(p2tui.JField(value, "name"))}
	for _, each := range p2tui.JList(p2tui.JField(value, "places")) {
		place := projectscontract.ProjectPlace{Path: p2tui.JStr(p2tui.JField(each, "path"))}
		if env, ok := p2tui.JField(each, "env").(string); ok {
			place.Env = &env
		}
		project.Places = append(project.Places, place)
	}
	return project
}

// numbers reads a record of numbers.
func numbers(value any) map[string]int64 {
	out := map[string]int64{}
	if object, ok := value.(*delta.JsonObject); ok {
		for key, each := range object.All() {
			if number, ok := each.(float64); ok {
				out[key] = int64(number)
			}
		}
	}
	return out
}

type status struct {
	busy  []int
	error bool
}

func statusesOf(value any) map[string]status {
	out := map[string]status{}
	if object, ok := value.(*delta.JsonObject); ok {
		for key, each := range object.All() {
			current := status{error: p2tui.JField(each, "error") != nil}
			for _, id := range p2tui.JList(p2tui.JField(each, "busy")) {
				current.busy = append(current.busy, int(p2tui.JNum(id)))
			}
			out[key] = current
		}
	}
	return out
}

// Surface is the sessions screen.
type Surface struct {
	rt    *runtime.TuiRuntime
	scope *chordtui.TuiScope

	// Search is the search box; Prompt is the rename prompt.
	Search *pitui.TextInput
	Prompt *pitui.TextInput
	focus  pitui.Component
	// location is the "+ New session" prompt.
	location *LocationInput

	received  []contract.Session
	shownRows func() []contract.Session
	editing   chordtui.OptimisticAdd[rowGuess]

	operations []operation
	children   map[string][]runtime.SessionChild
	statuses   map[string]status
	answers    map[string]int64
	reads      map[string]int64
	marked     string
	hits       []runtime.SessionHit
	hitsQuery  string

	sources      []runtime.TuiSessionSource
	sourcesTable *p2runtime.KeyedTable[runtime.TuiSessionSource]
	sourcesRead  func() (any, bool)
	stopSources  []func()
	searchSeq    int

	service     *host.Reference
	marks       *host.Reference
	projectsRef *host.Reference
	owner       bool
	defaultCwd  string
	defaultEnv  *string
	mode        string
	renaming    *contract.Session
	confirming  *contract.Session
	status      *struct {
		text    string
		isError bool
	}
	scopeAll     bool
	archived     bool
	pinnedOnly   bool
	showIDs      bool
	sort         sortMode
	selectedIdx  int
	visible      []row
	projectList  []projectscontract.Project
	label        string
	failure      string
	hasFailure   bool
	memberships  map[string]string
	probes       *PlaceProbes
	picked       map[string]int
	openAttempt  int
	shown        bool
	viewed       string
	removeListen func()
	disposeOnce  sync.Once
	stops        []func()
}

// New is the sessions screen of rt.
func New(rt *runtime.TuiRuntime) *Surface {
	s := &Surface{
		rt: rt, Search: pitui.NewInput(pitui.InputOptions{}), Prompt: pitui.NewInput(pitui.InputOptions{}),
		children: map[string][]runtime.SessionChild{}, statuses: map[string]status{}, answers: map[string]int64{}, reads: map[string]int64{},
		defaultCwd: "~", mode: "list", sort: sortThreaded, memberships: map[string]string{}, picked: map[string]int{}, label: "this device",
	}
	s.focus = s.Search
	s.scope = chordtui.NewTuiScope(context.Background(), rt.Host, rt.Ctx.RequestRender)
	s.probes = NewPlaceProbes(rt.Ctx, func() { rt.Ctx.RequestRender() })
	s.shownRows, s.editing, _ = chordtui.Optimistic(s.scope, func() []contract.Session { return s.received }, applyRow)
	s.Prompt.OnSubmit = func(string) { s.submitPrompt() }
	s.sourcesRead = chordtui.Registry(s.scope, runtime.TuiSessionSources.Registry)
	s.viewed = rt.Session
	s.removeListen = rt.AddListener(s.changed)
	s.startSessions()
	s.startStatuses()
	return s
}

func (s *Surface) ctx() runtime.TuiContext { return s.rt.Ctx }

// follow follows a reference's member; apply runs on the owner loop.
func (s *Surface) follow(ref *host.Reference, member string, apply func(value delta.JsonValue)) {
	source := ref.Member(member)
	if source == nil {
		return
	}
	stop := p2tui.FollowMember(source, func(value delta.JsonValue) {
		s.rt.Post(func() {
			apply(value)
			s.rt.Ctx.RequestRender()
		})
	})
	s.stops = append(s.stops, stop)
}

// whenLive calls setup on the owner loop with the variant of ref once it is live.
func (s *Surface) whenLive(ref *host.Reference, setup func(variant string)) {
	go func() {
		if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
			return
		}
		variant, _, _ := ref.Variant()
		s.rt.Post(func() { setup(variant) })
	}()
}

func (s *Surface) startSessions() {
	ref, err := s.scope.Hold(contract.Sessions, delta.JsonObjectOf())
	if err != nil {
		return
	}
	s.service = ref
	s.whenLive(ref, func(variant string) {
		s.owner = variant == "owner"
		if variant != "owner" && variant != "guest" {
			return
		}
		s.follow(ref, "sessions", func(value delta.JsonValue) {
			var rows []contract.Session
			for _, each := range p2tui.JList(value) {
				rows = append(rows, sessionOf(each))
			}
			s.received = rows
			s.followMains(rows)
		})
		if variant != "owner" {
			return
		}
		// Unread answers are the owner's (any device); guests see none.
		if marks, err := s.scope.Hold(searchcontract.SessionReads, delta.JsonObjectOf()); err == nil {
			s.marks = marks
			s.whenLive(marks, func(v string) {
				if v != "owner" {
					return
				}
				s.follow(marks, "answers", func(value delta.JsonValue) { s.answers = numbers(value); s.markShownRead() })
				s.follow(marks, "reads", func(value delta.JsonValue) { s.reads = numbers(value); s.markShownRead() })
			})
		}
		s.followProjects()
		s.follow(ref, "operations", func(value delta.JsonValue) {
			var ops []operation
			for _, each := range p2tui.JList(value) {
				ops = append(ops, operationOf(each))
			}
			s.followOperations(ops)
		})
		s.follow(ref, "defaultCwd", func(value delta.JsonValue) {
			s.defaultCwd = "~"
			if text, ok := value.(string); ok && text != "" {
				s.defaultCwd = text
			}
		})
		s.follow(ref, "defaultEnv", func(value delta.JsonValue) {
			s.defaultEnv = nil
			if text, ok := value.(string); ok {
				s.defaultEnv = &text
			}
		})
	})
}

// startStatuses: owners and guests are readers (a guest's view holds its granted sessions, without error texts).
func (s *Surface) startStatuses() {
	ref, err := s.scope.Hold(statuscontract.SessionStatus, delta.JsonObjectOf())
	if err != nil {
		return
	}
	s.whenLive(ref, func(variant string) {
		if variant == "reader" {
			s.follow(ref, "statuses", func(value delta.JsonValue) { s.statuses = statusesOf(value) })
		}
	})
}

// followProjects follows the owner's projects: new places are probed at once while the selector is shown.
func (s *Surface) followProjects() {
	if s.projectsRef != nil {
		return
	}
	ref, err := s.scope.Hold(projectscontract.Projects, delta.JsonObjectOf())
	if err != nil {
		return
	}
	s.projectsRef = ref
	s.whenLive(ref, func(variant string) {
		if variant != "owner" {
			return
		}
		s.follow(ref, "projects", func(value delta.JsonValue) {
			var projects []projectscontract.Project
			for _, each := range p2tui.JList(value) {
				projects = append(projects, projectOf(each))
			}
			s.projectList = projects
			if s.shown {
				s.probes.Probe(s.places())
			}
		})
		s.follow(ref, "failure", func(value delta.JsonValue) {
			text, ok := value.(string)
			s.failure, s.hasFailure = text, ok
		})
		s.follow(ref, "local", func(value delta.JsonValue) { s.label = p2tui.JStr(value) })
		s.follow(ref, "sessions", func(value delta.JsonValue) {
			s.memberships = map[string]string{}
			if object, ok := value.(*delta.JsonObject); ok {
				for key, each := range object.All() {
					s.memberships[key] = p2tui.JStr(each)
				}
			}
		})
	})
}

func (s *Surface) places() []projectscontract.ProjectPlace {
	var places []projectscontract.ProjectPlace
	for _, project := range s.projectList {
		places = append(places, project.Places...)
	}
	return places
}

// changed is the runtime's listener: "current folder" follows the viewed session; showing the selector probes the projects' places again.
func (s *Surface) changed() {
	s.markShownRead()
	shown := s.rt.Screen == Slot
	if shown && !s.shown {
		s.picked = map[string]int{}
		s.probes.Restart(s.places())
	}
	s.shown = shown
	if s.rt.Session != s.viewed {
		s.viewed = s.rt.Session
	}
	s.syncSources()
}

// syncSources subscribes to the optional sources (subagents, message search) of the plugins that have them, again when the registry changed.
func (s *Surface) syncSources() {
	value, _ := s.sourcesRead()
	table, _ := value.(*p2runtime.KeyedTable[runtime.TuiSessionSource])
	if table == s.sourcesTable {
		return
	}
	s.sourcesTable = table
	for _, stop := range s.stopSources {
		stop()
	}
	s.stopSources = nil
	s.sources = nil
	s.children = map[string][]runtime.SessionChild{}
	if table == nil {
		return
	}
	for _, key := range table.Keys() {
		source, _ := table.Get(key)
		s.sources = append(s.sources, source)
		if source.Children != nil {
			s.stopSources = append(s.stopSources, source.Children(s.rt, func(children map[string][]runtime.SessionChild) {
				s.rt.Post(func() {
					s.children = children
					s.rt.Ctx.RequestRender()
				})
			}))
		}
	}
}

// markShownRead: the chat showing a session's main conversation reads it through its latest answer (the web's rule, owner).
func (s *Surface) markShownRead() {
	rt := s.rt
	if s.marks == nil || !s.owner || rt.Screen != "chat" || !rt.HasSession || rt.Conversation != rt.MainOf(rt.Session) {
		return
	}
	if variant, _, _ := s.marks.Variant(); variant != "owner" {
		return
	}
	session := rt.Session
	latest := s.answers[session]
	key := fmt.Sprintf("%s:%d", session, latest)
	if latest <= s.reads[session] || key == s.marked {
		return
	}
	s.marked = key
	marks := s.marks
	go func() {
		_, _ = marks.Call("markRead", delta.JsonObjectOf("session", session, "through", float64(latest)), context.Background())
	}()
}

// Projects is the owner's projects, in projects.json's order (`/move` reads them too).
func (s *Surface) Projects() []projectscontract.Project { return s.projectList }

// LocalLabel is this device's name in place labels: the server's hostname.
func (s *Surface) LocalLabel() string { return s.label }

// ProjectOf is the project session id belongs to, by name (perhaps no longer in projects.json).
func (s *Surface) ProjectOf(id string) (string, bool) {
	name, ok := s.memberships[id]
	return name, ok
}

// followOperations announces what changed in the lifecycle operations: a failure (as an error line until `/dismiss-failed`), and a running operation that
// ended without one (a move done, a copy ready).
func (s *Surface) followOperations(operations []operation) {
	before := map[string]operation{}
	for _, each := range s.operations {
		before[each.id] = each
	}
	s.operations = operations
	for _, op := range operations {
		if prior, ok := before[op.id]; op.phase == "failed" && (!ok || prior.phase != "failed") {
			s.rt.Notify(operationTexts[op.kind].failed+": "+render.LineText(op.err)+" · /dismiss-failed", runtime.NoticeError)
		}
	}
	for _, prior := range s.operations0(before) {
		if prior.phase == "running" && !slices.ContainsFunc(operations, func(each operation) bool { return each.id == prior.id }) {
			s.rt.Notify(operationTexts[prior.kind].done, runtime.NoticeInfo)
		}
	}
}

// operations0 is before's operations in a stable order.
func (s *Surface) operations0(before map[string]operation) []operation {
	ids := make([]string, 0, len(before))
	for id := range before {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]operation, len(ids))
	for i, id := range ids {
		out[i] = before[id]
	}
	return out
}

// operationText is what a row says about an operation on it: failed, running, or a copy into it whose files are copied.
func (s *Surface) operationText(session string) string {
	var found *operation
	find := func(match func(operation) bool) {
		if found != nil {
			return
		}
		for i := range s.operations {
			if match(s.operations[i]) {
				found = &s.operations[i]
				return
			}
		}
	}
	find(func(each operation) bool { return each.phase == "failed" && each.session == session })
	find(func(each operation) bool { return each.session == session })
	find(func(each operation) bool { return each.phase == "running" && each.target == session && each.copying })
	if found == nil {
		return ""
	}
	if found.phase == "failed" {
		return operationTexts[found.kind].failed + " "
	}
	if found.session == session {
		return operationTexts[found.kind].running + " "
	}
	return "copying files… "
}

// DismissFailed is `/dismiss-failed`: forgets every failed operation (their lines go from every client's list).
func (s *Surface) DismissFailed() {
	catalog := s.Catalog()
	var failed []operation
	for _, op := range s.operations {
		if op.phase == "failed" {
			failed = append(failed, op)
		}
	}
	if catalog == nil || len(failed) == 0 {
		s.rt.Notify("No failed operations", runtime.NoticeInfo)
		return
	}
	for _, op := range failed {
		id := op.id
		s.call(catalog, "dismissOperation", delta.JsonObjectOf("operation", id))
	}
}

// call runs a call off the loop; its failure is shown as an error line.
func (s *Surface) call(ref *host.Reference, method string, args delta.JsonValue) {
	go func() {
		if _, err := ref.Call(method, args, context.Background()); err != nil {
			s.rt.Post(func() { s.rt.Fail(err) })
		}
	}()
}

func (s *Surface) rows() []contract.Session { return s.shownRows() }

// Catalog is the owner's live catalog, which every change goes through; nil otherwise.
func (s *Surface) Catalog() *host.Reference {
	if s.service == nil || !s.owner || p2runtime.StatusOf(s.service) != host.RefStateLive {
		return nil
	}
	return s.service
}

// edit changes a row: shown at once, done once the server took it (the selector's status, else a status line); a refused change rolls back and its error shows
// as a status line.
func (s *Surface) edit(guess rowGuess, method string, args delta.JsonValue, done string) {
	catalog := s.Catalog()
	if catalog == nil {
		return
	}
	go func() {
		err := s.editing(guess, func() error {
			_, err := catalog.Call(method, args, context.Background())
			return err
		})
		s.rt.Post(func() {
			if err != nil {
				s.rt.Fail(err)
			} else if s.rt.Screen == Slot {
				s.setStatus(done, false)
			} else {
				s.rt.Notify(done, runtime.NoticeInfo)
			}
		})
	}()
}

// current is the viewed session, which "current folder" means.
func (s *Surface) current() *contract.Session {
	return s.Session(s.rt.Session)
}

// Session is a session of the catalog by ID (the viewed one for `/cd`); nil for none.
func (s *Surface) Session(id string) *contract.Session {
	if id == "" {
		return nil
	}
	for _, each := range s.rows() {
		if each.ID == id {
			each := each
			return &each
		}
	}
	return nil
}

func envEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Surface) inScope(session contract.Session) bool {
	if (session.ArchivedAt != nil) != s.archived {
		return false
	}
	if s.pinnedOnly && !session.Pinned {
		return false
	}
	if s.scopeAll || s.grouped() {
		return true
	}
	env, cwd := s.defaultEnv, s.defaultCwd
	if current := s.current(); current != nil {
		env, cwd = current.Env, current.Cwd
	}
	return envEqual(session.Env, env) && session.Cwd == cwd
}

// grouped is whether the list is grouped by project: the owner's active sessions without a search, with projects. It shows every session, so Tab's scope does
// not apply.
func (s *Surface) grouped() bool {
	return s.owner && !s.archived && len(s.projectList) > 0 && strings.TrimSpace(s.Search.GetValue()) == ""
}

func byRecent(a, b contract.Session) int {
	if a.Pinned != b.Pinned {
		if a.Pinned {
			return -1
		}
		return 1
	}
	switch {
	case a.UpdatedAt > b.UpdatedAt:
		return -1
	case a.UpdatedAt < b.UpdatedAt:
		return 1
	}
	return 0
}

func sortedRecent(sessions []contract.Session) []contract.Session {
	out := slices.Clone(sessions)
	slices.SortStableFunc(out, byRecent)
	return out
}

// rebuild recomputes the rows shown (pi's `filterSessions`).
func (s *Surface) rebuild() {
	s.syncSources()
	query := strings.TrimSpace(s.Search.GetValue())
	var candidates []contract.Session
	for _, each := range s.rows() {
		if s.inScope(each) {
			candidates = append(candidates, each)
		}
	}
	var rows []row
	creating := s.owner && !s.archived && query == ""
	if creating && s.hasFailure {
		rows = append(rows, row{kind: rowHeading, text: s.failure, isError: true})
	}
	switch {
	case s.grouped():
		rows = s.groupRows(rows, sortedRecent(candidates))
	case creating:
		rows = append(rows, row{kind: rowNew})
		rows = s.sessionRows(rows, sortedRecent(candidates), "")
	default:
		matched := candidates
		if query != "" {
			matched = pitui.FuzzyFilter(candidates, query, func(session contract.Session) string {
				return session.Title + " " + where(session) + " " + session.ID
			})
		}
		if !(query != "" && s.sort == sortRelevance) {
			matched = sortedRecent(matched)
		}
		rows = s.sessionRows(rows, matched, "")
	}
	if query != "" && len(s.hits) > 0 && s.hitsQuery == query {
		rows = append(rows, row{kind: rowHeading, text: "Messages (literal matches)"})
		for _, hit := range s.hits {
			rows = append(rows, row{kind: rowMessage, hit: hit})
		}
	}
	s.visible = rows
	s.selectedIdx = min(s.selectedIdx, max(0, len(rows)-1))
	if s.selectedIdx < len(rows) && rows[s.selectedIdx].kind == rowHeading {
		s.selectedIdx++
	}
}

// groupRows is the grouped list (empty search, owner): each project's row with its sessions below it, in projects.json's order, then "Other": "+ New session"
// and the sessions of no project, or of one projects.json no longer has.
func (s *Surface) groupRows(rows []row, sessions []contract.Session) []row {
	grouped := map[string]bool{}
	for _, project := range s.projectList {
		grouped[project.Name] = true
		rows = append(rows, row{kind: rowProject, project: project})
		var members []contract.Session
		for _, each := range sessions {
			if name, ok := s.ProjectOf(each.ID); ok && name == project.Name {
				members = append(members, each)
			}
		}
		rows = s.sessionRows(rows, members, "  ")
	}
	rows = append(rows, row{kind: rowHeading, text: "Other"}, row{kind: rowNew, prefix: "  "})
	var others []contract.Session
	for _, each := range sessions {
		if name, _ := s.ProjectOf(each.ID); !grouped[name] {
			others = append(others, each)
		}
	}
	return s.sessionRows(rows, others, "  ")
}

// sessionRows are session rows indented by indent; in the threaded sort without a search, each with its named subagent conversations below it (one-shot agents
// and archived ones are not listed).
func (s *Surface) sessionRows(rows []row, sessions []contract.Session, indent string) []row {
	threaded := s.sort == sortThreaded && strings.TrimSpace(s.Search.GetValue()) == ""
	for _, session := range sessions {
		rows = append(rows, row{kind: rowSession, session: session, prefix: indent})
		if !threaded {
			continue
		}
		kids := s.children[session.ID]
		for i, child := range kids {
			prefix := "├─ "
			if i == len(kids)-1 {
				prefix = "└─ "
			}
			rows = append(rows, row{kind: rowChild, session: session, child: child, prefix: prefix, indent: indent})
		}
	}
	return rows
}

// queueSearch asks for message hits for the query, debounced (Search is literal and bounded, section 1.17).
func (s *Surface) queueSearch() {
	s.searchSeq++
	seq := s.searchSeq
	query := strings.TrimSpace(s.Search.GetValue())
	if query == "" {
		s.hits, s.hitsQuery = nil, ""
		return
	}
	var search func(*runtime.TuiRuntime, string, bool) ([]runtime.SessionHit, error)
	for _, source := range s.sources {
		if source.Search != nil {
			search = source.Search
			break
		}
	}
	if search == nil || !s.owner {
		return
	}
	archived := s.archived
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.rt.Post(func() {
			if seq != s.searchSeq || s.rt.Ctx == nil {
				return
			}
			go func() {
				hits, err := search(s.rt, query, archived)
				if err != nil {
					return
				}
				s.rt.Post(func() {
					if seq != s.searchSeq || strings.TrimSpace(s.Search.GetValue()) != query {
						return
					}
					s.hits, s.hitsQuery = hits[:min(len(hits), 40)], query
					s.rt.Ctx.RequestRender()
				})
			}()
		})
	}()
}

func (s *Surface) setStatus(text string, isError bool) {
	s.status = &struct {
		text    string
		isError bool
	}{text, isError}
	s.rt.Ctx.RequestRender()
}

// Contexts are the context terms (section 6.3): the selector's own keys apply only while its list has the keys.
func (s *Surface) Contexts() []string {
	var terms []string
	if s.mode == "list" && s.confirming == nil {
		terms = append(terms, "selector")
	} else {
		terms = append(terms, "creating")
	}
	input := s.Prompt
	switch {
	case s.mode == "list":
		input = s.Search
	case s.mode == "create" && s.location != nil:
		input = s.location.Input
	}
	terms = append(terms, "editorFocused")
	if input.GetValue() == "" {
		terms = append(terms, "editorEmpty")
	}
	return terms
}

// Focus is the component with the focus.
func (s *Surface) Focus() pitui.Component { return s.focus }

func (s *Surface) selected() *row {
	if s.selectedIdx < 0 || s.selectedIdx >= len(s.visible) {
		return nil
	}
	return &s.visible[s.selectedIdx]
}

// Command runs a named command of the screen.
func (s *Surface) Command(name string) {
	if name != "new" || s.Catalog() == nil {
		return
	}
	s.mode = "create"
	env, path := s.defaultEnv, s.defaultCwd
	if current := s.current(); current != nil {
		env, path = current.Env, current.Cwd
	}
	s.location = NewLocationInput(s.ctx(), LocationInputOptions{
		Title: "New session", Action: "creates", Env: env, Path: path,
		OnSubmit: func(location p2runtime.LocationInput) { s.create(location, "") },
		OnCancel: s.closePrompt,
	})
	s.location.SetFocused(s.rt.IsFocused())
	s.focus = s.location
	s.rt.Changed()
}

// followMains keeps the runtime's main conversations; a `/tree` switch (here or on another device) moves a TUI viewing the previous main conversation to the
// new one.
func (s *Surface) followMains(rows []contract.Session) {
	rt := s.rt
	for _, each := range rows {
		before, had := rt.Mains[each.ID]
		rt.Mains[each.ID] = each.Main
		if had && before != each.Main && rt.HasSession && rt.Session == each.ID && rt.Conversation == before {
			main := each.Main
			rt.Navigate(rt.Screen, each.ID, true, &main)
		}
	}
}

// create creates a session and opens it at once: this TUI chooses its ID, and the conversation shows loading until the session's worker runs. From a project row
// it belongs to project, recorded first (`Projects.assign`) so its row never shows under "Other". A refused creation comes back to the selector with the error.
func (s *Surface) create(location p2runtime.LocationInput, project string) {
	catalog := s.Catalog()
	if catalog == nil {
		return
	}
	var projects *host.Reference
	if s.projectsRef != nil {
		if variant, _, _ := s.projectsRef.Variant(); variant == "owner" && p2runtime.StatusOf(s.projectsRef) == host.RefStateLive {
			projects = s.projectsRef
		}
	}
	id := uuid.NewString()
	if s.mode == "create" {
		s.closePrompt()
	}
	s.rt.Navigate("chat", id, true, nil)
	go func() {
		var err error
		if project != "" && projects != nil {
			_, err = projects.Call("assign", delta.JsonObjectOf("session", id, "project", project), context.Background())
		}
		if err == nil {
			args := delta.JsonObjectOf("id", id, "cwd", location.Path)
			if location.Env != nil {
				args.Set("env", *location.Env)
			} else {
				args.Set("env", nil)
			}
			_, err = catalog.Call("create", args, context.Background())
		}
		if err != nil {
			s.rt.Post(func() {
				if s.rt.HasSession && s.rt.Session == id {
					s.rt.Navigate("sessions", "", false, nil)
				}
				s.rt.Fail(err)
			})
		}
	}()
}

// CreateIn is `/new [env:]path`: a session there (no prefix: this device, as the footer writes it; a relative path is below the home directory), or without an
// argument in the viewed session's env:cwd; opened at once (pi's `/new`).
func (s *Surface) CreateIn(argument string) {
	current := s.current()
	if argument == "" {
		location := p2runtime.LocationInput{Env: s.defaultEnv, Path: s.defaultCwd}
		if current != nil {
			location = p2runtime.LocationInput{Env: current.Env, Path: current.Cwd}
		}
		s.create(location, "")
		return
	}
	go func() {
		names := EnvNames(s.ctx())
		s.rt.Post(func() {
			location, err := p2runtime.ParseLocation(argument, names, p2runtime.LocationFallback{})
			if err != nil {
				s.rt.Fail(err)
				return
			}
			s.create(location, "")
		})
	}()
}

func (s *Surface) submitPrompt() {
	value := strings.TrimSpace(s.Prompt.GetValue())
	if s.mode != "rename" || s.renaming == nil || value == "" {
		return
	}
	s.RenameSession(s.renaming.ID, value)
	s.closePrompt()
}

// RenameSession renames session id (the selector's prompt, `/name`): the new title shows at once.
func (s *Surface) RenameSession(id, value string) {
	title := jsSlice(value, contract.MaxTitle)
	s.edit(rowGuess{id: id, title: &title}, "rename", delta.JsonObjectOf("id", id, "title", title), "Session name: "+render.LineText(title))
}

// jsSlice is value.slice(0, n) over UTF-16 code units.
func jsSlice(value string, n int) string {
	units := 0
	for i, r := range value {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			return value[:i]
		}
		units += width
	}
	return value
}

func (s *Surface) closePrompt() {
	s.mode = "list"
	s.renaming = nil
	s.location = nil
	s.focus = s.Search
	s.rt.Changed()
}

// The selector's actions (registered as commands below, so the dispatcher routes their keys here).

// ToggleSort cycles the sort.
func (s *Surface) ToggleSort() {
	index := slices.Index(sorts, s.sort)
	s.sort = sorts[(index+1)%len(sorts)]
}

// TogglePinned flips the pinned filter.
func (s *Surface) TogglePinned() {
	s.pinnedOnly = !s.pinnedOnly
	s.selectedIdx = 0
}

// ToggleArchived switches between active and archived sessions.
func (s *Surface) ToggleArchived() {
	s.archived = !s.archived
	s.confirming = nil
	s.selectedIdx = 0
	s.queueSearch()
}

// ToggleIDs shows or hides session IDs.
func (s *Surface) ToggleIDs() { s.showIDs = !s.showIDs }

// Rename opens the rename prompt for the selected session.
func (s *Surface) Rename() {
	current := s.selected()
	if !s.owner || current == nil || current.kind != rowSession {
		return
	}
	session := current.session
	s.mode = "rename"
	s.renaming = &session
	FillInput(s.Prompt, session.Title)
	s.focus = s.Prompt
	s.rt.Changed()
}

// Pin pins or unpins the selected session.
func (s *Surface) Pin() {
	current := s.selected()
	if current == nil || current.kind != rowSession {
		return
	}
	id := current.session.ID
	pinned := !current.session.Pinned
	done := "Unpinned"
	if pinned {
		done = "Pinned"
	}
	s.edit(rowGuess{id: id, pinned: &pinned}, "pin", delta.JsonObjectOf("id", id, "pinned", pinned), done)
}

// Archive is delete = archive (pi asks to confirm); in the archived scope it restores.
func (s *Surface) Archive() {
	current := s.selected()
	if !s.owner || current == nil || current.kind != rowSession {
		return
	}
	if s.archived {
		id := current.session.ID
		var none *int64
		s.edit(rowGuess{id: id, archivedAt: &none}, "restore", delta.JsonObjectOf("id", id), "Session restored")
		return
	}
	session := current.session
	s.confirming = &session
	s.rt.Ctx.RequestRender()
}

// target is the place Enter on project's row uses: the one picked with left/right, else the first reachable one.
func (s *Surface) target(project projectscontract.Project) *projectscontract.ProjectPlace {
	if index, ok := s.picked[project.Name]; ok && index < len(project.Places) {
		return &project.Places[index]
	}
	for i := range project.Places {
		if s.probes.State(project.Places[i]).Kind == PlaceReachable {
			return &project.Places[i]
		}
	}
	return nil
}

// pickPlace: left/right on a project row: the previous or next place becomes Enter's.
func (s *Surface) pickPlace(project projectscontract.Project, delta int) {
	target := s.target(project)
	index := 0
	if delta > 0 {
		index = -1
	}
	if target != nil {
		for i := range project.Places {
			if &project.Places[i] == target {
				index = i
			}
		}
	}
	count := len(project.Places)
	s.picked[project.Name] = ((index+delta)%count + count) % count
}

// openProject is Enter on a project row: a session at the picked place, else at the first reachable place in preference order, waiting for the probes still
// running; the reasons when none is reachable. A later Enter, or leaving the selector, abandons the wait.
func (s *Surface) openProject(project projectscontract.Project) {
	s.openAttempt++
	attempt := s.openAttempt
	var chosen *projectscontract.ProjectPlace
	if index, ok := s.picked[project.Name]; ok && index < len(project.Places) {
		chosen = &project.Places[index]
	}
	s.setStatus("Checking "+render.LineText(project.Name)+"'s places…", false)
	places := project.Places
	if chosen != nil {
		places = []projectscontract.ProjectPlace{*chosen}
	}
	label := s.LocalLabel()
	go func() {
		var failures []string
		for _, place := range places {
			state := s.probes.Result(place)
			stale := make(chan bool, 1)
			s.rt.Post(func() { stale <- attempt != s.openAttempt || s.rt.Screen != Slot })
			if <-stale {
				return
			}
			if state.Kind == PlaceReachable {
				s.rt.Post(func() {
					s.status = nil
					s.create(p2runtime.LocationInput{Env: place.Env, Path: state.Path}, project.Name)
				})
				return
			}
			if problem := PlaceProblem(projectscontract.PlaceLabel(place, label), state); problem != "" {
				failures = append(failures, problem)
			}
		}
		s.rt.Post(func() {
			if attempt == s.openAttempt && s.rt.Screen == Slot {
				s.setStatus("No reachable place: "+render.LineText(strings.Join(failures, " · ")), true)
			}
		})
	}()
}

func (s *Surface) open(current *row) {
	if current == nil {
		return
	}
	rt := s.rt
	switch current.kind {
	case rowProject:
		s.openProject(current.project)
	case rowNew:
		s.Command("new")
	case rowSession:
		main := current.session.Main
		rt.Navigate("chat", current.session.ID, true, &main)
	case rowChild:
		conversation := current.child.ConversationID
		rt.Navigate("chat", current.session.ID, true, &conversation)
	case rowMessage:
		conversation := current.hit.ConversationID
		rt.Navigate("chat", current.hit.Session, true, &conversation)
	}
}

func (s *Surface) move(step int) {
	if len(s.visible) == 0 {
		return
	}
	index := s.selectedIdx
	for range len(s.visible) {
		index = ((index+step)%len(s.visible) + len(s.visible)) % len(s.visible)
		if s.visible[index].kind != rowHeading {
			break
		}
	}
	s.selectedIdx = index
}

// HandleInput routes keys.
func (s *Surface) HandleInput(data string) {
	s.rebuild()
	s.handleInput(data)
	s.rebuild()
}

func (s *Surface) handleInput(data string) {
	keys := s.ctx().Keys()
	s.status = nil
	if s.mode == "create" && s.location != nil {
		s.location.HandleInput(data)
		return
	}
	if s.mode != "list" {
		if keys.Matches(data, "tui.select.cancel") {
			s.closePrompt()
		} else {
			s.Prompt.HandleInput(data)
		}
		s.rt.Ctx.RequestRender()
		return
	}
	if s.confirming != nil {
		if keys.Matches(data, "tui.select.confirm") {
			id := s.confirming.ID
			at := now().UnixMilli()
			archivedAt := &at
			s.edit(rowGuess{id: id, archivedAt: &archivedAt}, "archive", delta.JsonObjectOf("id", id), "Session archived")
		}
		s.confirming = nil
		s.rt.Ctx.RequestRender()
		return
	}
	// Left/right pick a project row's place: those rows show only while the search is empty, whose cursor stays put.
	current := s.selected()
	pick := 0
	switch {
	case keys.Matches(data, "tui.editor.cursorLeft"):
		pick = -1
	case keys.Matches(data, "tui.editor.cursorRight"):
		pick = 1
	}
	switch {
	case keys.Matches(data, "tui.select.up"):
		s.move(-1)
	case keys.Matches(data, "tui.select.down"):
		s.move(1)
	case keys.Matches(data, "tui.select.pageUp"):
		s.move(-10)
	case keys.Matches(data, "tui.select.pageDown"):
		s.move(10)
	case keys.Matches(data, "tui.select.confirm"):
		s.open(s.selected())
	case keys.Matches(data, "tui.select.cancel"):
		if s.Search.GetValue() != "" {
			s.Search.SetValue("")
			s.selectedIdx = 0
			s.queueSearch()
		} else if s.rt.HasSession {
			s.rt.Navigate("chat", s.rt.Session, true, nil)
		}
	case current != nil && current.kind == rowProject && pick != 0:
		s.pickPlace(current.project, pick)
	case keys.Matches(data, "tui.input.tab"):
		if s.grouped() {
			return
		}
		s.scopeAll = !s.scopeAll
		s.selectedIdx = 0
	default:
		grouped := s.grouped()
		s.Search.HandleInput(data)
		// A search typed in the grouped list (which shows every session) searches every session; Tab narrows it.
		if grouped && !s.grouped() {
			s.scopeAll = true
		}
		s.selectedIdx = 0
		s.queueSearch()
	}
	s.rt.Ctx.RequestRender()
}

func truncate(text string, width int, ellipsis string) string {
	return widthx.TruncateToWidth(text, width, ellipsis, false)
}

func (s *Surface) header(width int) []string {
	t := s.rt.Theme().Current()
	// The grouped list has no scope: every session shows.
	grouped := s.grouped()
	title := "Resume Session (Current Folder)"
	switch {
	case s.archived:
		title = "Archived Sessions"
	case grouped:
		title = "Resume Session"
	case s.scopeAll:
		title = "Resume Session (All)"
	}
	scope := ""
	switch {
	case grouped:
	case s.scopeAll:
		scope = t.Fg("muted", "○ Current Folder | ") + t.Fg("accent", "◉ All") + "  "
	default:
		scope = t.Fg("accent", "◉ Current Folder") + t.Fg("muted", " | ○ All") + "  "
	}
	sortLabel := map[sortMode]string{sortThreaded: "Threaded", sortRecent: "Recent", sortRelevance: "Relevance"}[s.sort]
	pinned := "All"
	if s.pinnedOnly {
		pinned = "Only"
	}
	right := truncate(scope+t.Fg("muted", "Pinned: ")+t.Fg("accent", pinned)+"  "+t.Fg("muted", "Sort: ")+t.Fg("accent", sortLabel), width, "")
	left := truncate(t.Bold(title), max(0, width-widthx.VisibleWidth(right)-1), "")
	line := left + strings.Repeat(" ", max(0, width-widthx.VisibleWidth(left)-widthx.VisibleWidth(right))) + right
	sep := t.Fg("muted", " · ")
	ctx := s.ctx()
	var hints []string
	switch {
	case s.confirming != nil:
		hints = []string{
			t.Fg("error", fmt.Sprintf("Archive \"%s\"? %s confirm · %s cancel", render.LineText(s.confirming.Title), ctx.KeyText("tui.select.confirm"), ctx.KeyText("tui.select.cancel"))),
			"",
		}
	case s.status != nil:
		color := theme.ThemeColor("accent")
		if s.status.isError {
			color = "error"
		}
		hints = []string{t.Fg(color, s.status.text), ""}
	default:
		hint := func(id, label string) string {
			if ctx.KeyText(id) != "" {
				return render.KeyHint(ctx, id, label)
			}
			return ""
		}
		place := ""
		if current := s.selected(); current != nil && current.kind == rowProject && ctx.KeyText("tui.editor.cursorLeft") != "" {
			place = t.Fg("dim", ctx.KeyText("tui.editor.cursorLeft")+"/"+ctx.KeyText("tui.editor.cursorRight")) + t.Fg("muted", " place")
		}
		tab := ""
		if !grouped {
			tab = hint("tui.input.tab", "scope")
		}
		active := "archived"
		if s.archived {
			active = "active"
		}
		ids := "(off)"
		if s.showIDs {
			ids = "(on)"
		}
		second := []string{hint("app.session.toggleSort", "sort"), hint("app.session.toggleNamedFilter", "pinned"), hint("app.session.toggleArchived", active)}
		if s.owner {
			verb := "archive"
			if s.archived {
				verb = "restore"
			}
			second = append(second, hint("app.session.delete", verb), hint("app.session.rename", "rename"), hint("p2.session.pin", "pin"))
		}
		second = append(second, hint("app.session.togglePath", "ids "+ids))
		hints = []string{joinNonEmpty(sep, tab, place, t.Fg("muted", "literal message search; no regex or phrases")), joinNonEmpty(sep, second...)}
	}
	out := []string{line}
	for _, text := range hints {
		out = append(out, truncate(text, width, "…"))
	}
	return out
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

// marker is a row's activity marker, two columns wide on every row so titles do not shift: working (accent dot), a failed worker (error), an unread answer
// (muted ring), else blank. A subagent row is working when its conversation is.
func (s *Surface) marker(current row) string {
	t := s.rt.Theme().Current()
	if current.kind != rowSession && current.kind != rowChild {
		return "  "
	}
	id := current.session.ID
	st := s.statuses[id]
	if current.kind == rowChild {
		if slices.Contains(st.busy, current.child.ConversationID) {
			return t.Fg("accent", "● ")
		}
		return "  "
	}
	switch {
	case len(st.busy) > 0:
		return t.Fg("accent", "● ")
	case st.error:
		return t.Fg("error", "! ")
	case s.answers[id] > s.reads[id]:
		return t.Fg("muted", "◦ ")
	}
	return "  "
}

func (s *Surface) rowLine(current row, selected bool, width int) string {
	t := s.rt.Theme().Current()
	cursor := "  "
	if selected {
		cursor = t.Fg("accent", "› ")
	}
	var left, right string
	switch current.kind {
	case rowNew:
		left = t.Fg("accent", "+ New session")
	case rowHeading:
		color := theme.ThemeColor("muted")
		if current.isError {
			color = "error"
		}
		return truncate(t.Fg(color, "  "+render.LineText(current.text)), width, "")
	case rowProject:
		target := s.target(current.project)
		var places []string
		for i := range current.project.Places {
			place := current.project.Places[i]
			label := render.LineText(projectscontract.PlaceLabel(place, s.LocalLabel()))
			mark := PlaceMark[s.probes.State(place).Kind]
			if target != nil && &current.project.Places[i] == target {
				label = t.Fg("accent", label)
			}
			places = append(places, label+" "+t.Fg(theme.ThemeColor(mark.Color), mark.Text))
		}
		left = render.LineText(current.project.Name) + "   " + strings.Join(places, "   ")
	case rowMessage:
		title := current.hit.Title
		if title == "" {
			title = "Untitled"
		}
		left = render.LineText(title) + t.Fg("muted", " — "+render.LineText(current.hit.Text))
		right = current.hit.Role
	case rowChild:
		left = t.Fg("dim", current.prefix) + t.Fg("accent", render.LineText(current.child.Name))
		right = fmt.Sprintf("%s conv %d", render.LineText(current.child.Model), current.child.ConversationID)
	default:
		session := current.session
		isCurrent := s.rt.HasSession && session.ID == s.rt.Session
		title := session.Title
		if title == "" {
			title = "Untitled"
		}
		text := render.LineText(title)
		color := theme.ThemeColor("")
		switch {
		case s.confirming != nil && s.confirming.ID == session.ID:
			color = "error"
		case isCurrent:
			color = "accent"
		case session.Pinned:
			color = "warning"
		}
		// A project projects.json no longer has: the session waits under "Other", dimmed, with the old name.
		project, has := s.ProjectOf(session.ID)
		gone := has && !slices.ContainsFunc(s.projectList, func(each projectscontract.Project) bool { return each.Name == project })
		switch {
		case gone:
			left = t.Fg("dim", text+" ("+render.LineText(project)+")")
		case color != "":
			left = t.Fg(color, text)
		default:
			left = text
		}
		right = s.operationText(session.ID)
		if s.scopeAll || s.grouped() {
			right += render.LineText(where(session)) + " "
		}
		if s.showIDs {
			right += session.ID + " "
		}
		right += age(session.UpdatedAt)
	}
	// A group's rows are indented before their activity marker; a project row is its group's heading, without one.
	indent := ""
	switch current.kind {
	case rowSession, rowNew:
		indent = current.prefix
	case rowChild:
		indent = current.indent
	}
	lead := cursor + indent
	if current.kind != rowProject {
		lead += s.marker(current)
	}
	available := max(10, width-widthx.VisibleWidth(lead)-widthx.VisibleWidth(right)-2)
	styled := truncate(left, available, "…")
	if selected {
		styled = t.Bold(styled)
	}
	leftPart := lead + styled
	line := leftPart + strings.Repeat(" ", max(1, width-widthx.VisibleWidth(leftPart)-widthx.VisibleWidth(right))) + t.Fg("dim", right)
	if selected {
		line = t.Bg("selectedBg", line)
	}
	return truncate(line, width, "…")
}

// Render draws the screen.
func (s *Surface) Render(width int) []string {
	s.rebuild()
	t := s.rt.Theme().Current()
	if s.mode == "create" && s.location != nil {
		return s.location.Render(width)
	}
	if s.mode != "list" {
		lines := []string{t.Fg("accent", "Rename session")}
		lines = append(lines, s.Prompt.Render(width)...)
		return append(lines, t.Fg("dim", s.ctx().KeyText("tui.select.confirm")+" saves · "+s.ctx().KeyText("tui.select.cancel")+" cancels"))
	}
	lines := append(s.header(width), "")
	lines = append(lines, s.Search.Render(width)...)
	lines = append(lines, "")
	if len(s.visible) == 0 {
		empty := "  No sessions in current folder. Press Tab to view all."
		switch {
		case s.archived:
			empty = "  No archived sessions."
		case s.scopeAll:
			empty = "  No sessions found"
		}
		return append(lines, t.Fg("muted", empty))
	}
	maxVisible := max(3, s.rt.TUI.Terminal().Rows()-14)
	start := max(0, min(s.selectedIdx-maxVisible/2, len(s.visible)-maxVisible))
	end := min(start+maxVisible, len(s.visible))
	for index := start; index < end; index++ {
		current := s.visible[index]
		lines = append(lines, s.rowLine(current, index == s.selectedIdx, width))
		if index == s.selectedIdx && current.kind == rowProject {
			// The selected project's unusable places, with why: a mark alone does not say.
			var problems []string
			for _, place := range current.project.Places {
				if problem := PlaceProblem(projectscontract.PlaceLabel(place, s.LocalLabel()), s.probes.State(place)); problem != "" {
					problems = append(problems, problem)
				}
			}
			if len(problems) > 0 {
				lines = append(lines, truncate(t.Fg("muted", "    "+render.LineText(strings.Join(problems, " · "))), width, "…"))
			}
		}
	}
	if start > 0 || end < len(s.visible) {
		lines = append(lines, t.Fg("muted", fmt.Sprintf("  (%d/%d)", s.selectedIdx+1, len(s.visible))))
	}
	return lines
}

// Invalidate drops the cached renders.
func (s *Surface) Invalidate() {
	s.Search.Invalidate()
	s.Prompt.Invalidate()
	if s.location != nil {
		s.location.Invalidate()
	}
}

// Dispose releases what the screen holds.
func (s *Surface) Dispose() {
	s.disposeOnce.Do(func() {
		s.removeListen()
		s.searchSeq++
		for _, stop := range s.stopSources {
			stop()
		}
		for _, stop := range s.stops {
			stop()
		}
		s.scope.Dispose()
	})
}
