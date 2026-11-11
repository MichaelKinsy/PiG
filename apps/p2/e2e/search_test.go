// Ports test/p2/search.test.ts and test/p2/rebuild-index.test.ts (Pi main c23b9ec1): search (root messages indexed by
// acknowledged catch-up from the worker, titles and directories, archive scope, an index that outlives workers and server
// restarts) and Pi Mobile's rebuildIndex.

package e2e_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	searchcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/search"
)

// searchFixture is a server with an owner holding `Sessions` and `Search`.
type searchFixture struct {
	t        *testing.T
	fixture  *p2test.Fixture
	owner    host.Host
	sessions *host.Reference
	search   *host.Reference
}

func newSearchFixture(t *testing.T, options hosts.ServerOptions) *searchFixture {
	t.Helper()
	fixture := p2test.StartFixture(t, options)
	owner := fixture.Browser()
	sessions, err := p2test.HoldIn(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	search, err := p2test.HoldIn(owner, searchcontract.Search, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	return &searchFixture{t: t, fixture: fixture, owner: owner, sessions: sessions, search: search}
}

func (f *searchFixture) create(title string) string {
	f.t.Helper()
	args := delta.JsonObjectOf("cwd", f.fixture.Workspace)
	if title != "" {
		args.Set("title", title)
	}
	created, err := f.sessions.Call("create", args, p2test.Background)
	if err != nil {
		f.t.Fatal(err)
	}
	return object(created).Value("id").(string)
}

func (f *searchFixture) chat(session string, conversation float64) *host.Reference {
	f.t.Helper()
	chat, err := p2test.HoldIn(f.owner, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", conversation), "participant")
	if err != nil {
		f.t.Fatal(err)
	}
	return chat
}

func (f *searchFixture) send(chat *host.Reference, text, id string) {
	f.t.Helper()
	if _, err := chat.Call("send", delta.JsonObjectOf("text", text, "requestId", id), p2test.Background); err != nil {
		f.t.Fatal(err)
	}
}

func (f *searchFixture) query(query string, archived ...bool) *delta.JsonObject {
	f.t.Helper()
	args := delta.JsonObjectOf("query", query)
	if len(archived) > 0 {
		args.Set("archived", archived[0])
	}
	results, err := f.search.Call("search", args, p2test.Background)
	if err != nil {
		f.t.Fatal(err)
	}
	return object(results)
}

// found polls a search until predicate holds (indexing is asynchronous).
func (f *searchFixture) found(query string, predicate func(*delta.JsonObject) bool, archived ...bool) *delta.JsonObject {
	f.t.Helper()
	var last *delta.JsonObject
	if err := p2test.Poll(func() (bool, error) {
		args := delta.JsonObjectOf("query", query)
		if len(archived) > 0 {
			args.Set("archived", archived[0])
		}
		results, err := f.search.Call("search", args, p2test.Background)
		if err != nil {
			return false, err
		}
		last = object(results)
		return predicate(last), nil
	}, 20*time.Second, "search "+query); err != nil {
		f.t.Fatal(err)
	}
	return last
}

func hitCount(count int) func(*delta.JsonObject) bool {
	return func(results *delta.JsonObject) bool { return len(listOf(results.Value("messages"))) == count }
}

func (f *searchFixture) cursor(session string, conversation ...float64) float64 {
	f.t.Helper()
	args := delta.JsonObjectOf("session", session)
	if len(conversation) > 0 {
		args.Set("conversation", conversation[0])
	}
	cursor, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		index, err := ctx.Get(searchcontract.SearchIndex, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return index.Call("cursor", args, ctx)
	}, p2test.Background, host.RootOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	return cursor.(float64)
}

// onIndex calls method of the server's own `SearchIndex`.
func (f *searchFixture) onIndex(method string, args *delta.JsonObject) {
	f.t.Helper()
	if _, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		index, err := ctx.Get(searchcontract.SearchIndex, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return index.Call(method, args, ctx)
	}, p2test.Background, host.RootOptions{}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *searchFixture) stopWorker(session string) {
	f.t.Helper()
	if _, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		workers, err := ctx.Get(p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return workers.Call("stop", delta.JsonObjectOf("session", session), ctx)
	}, p2test.Background, host.RootOptions{}); err != nil {
		f.t.Fatal(err)
	}
}

func rolesOf(results *delta.JsonObject) []string {
	var roles []string
	for _, each := range listOf(results.Value("messages")) {
		roles = append(roles, object(each).Value("role").(string))
	}
	if len(roles) == 2 && roles[0] > roles[1] {
		roles[0], roles[1] = roles[1], roles[0]
	}
	return roles
}

func TestSearchFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := newSearchFixture(t, hosts.ServerOptions{})
	session, other := f.create("Zebra migration"), f.create("Unrelated")

	t.Run("finds root user and assistant messages, never the sender block", func(t *testing.T) {
		chat := f.chat(session, 1)
		f.send(chat, "please index quokka pancakes", "q")
		results := f.found("quokka", hitCount(2))
		if roles := rolesOf(results); strings.Join(roles, ",") != "assistant,user" {
			t.Fatalf("roles = %v", roles)
		}
		for _, each := range listOf(results.Value("messages")) {
			hit := object(each)
			if hit.Value("session") != session || hit.Value("conversationId") != float64(1) || hit.Value("title") != "Zebra migration" || !strings.Contains(hit.Value("text").(string), "quokka") {
				t.Fatalf("hit = %v", hit)
			}
		}
		if got := listOf(f.query("sender").Value("messages")); len(got) != 0 {
			t.Fatalf("sender: %v", got)
		}
		if got := listOf(f.query("owner").Value("messages")); len(got) != 0 {
			t.Fatalf("the author header is indexed: %v", got)
		}
		// Every term must match, literally (no FTS syntax).
		if got := listOf(f.query("quokka absent-term").Value("messages")); len(got) != 0 {
			t.Fatalf("absent term: %v", got)
		}
		if got := listOf(f.query(`quokka" OR "x`).Value("messages")); len(got) != 0 {
			t.Fatalf("FTS syntax: %v", got)
		}
		// The cursor reached the newest entry: the whole range was acknowledged.
		view, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool { return len(listOf(object(value).Value("entries"))) > 0 }, 0)
		if err != nil {
			t.Fatal(err)
		}
		newest := 0.0
		for _, each := range listOf(object(view).Value("entries")) {
			newest = max(newest, object(each).Value("id").(float64))
		}
		if err := p2test.Poll(func() (bool, error) { return f.cursor(session) >= newest, nil }, 10*time.Second, "cursor"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("matches titles and working directories", func(t *testing.T) {
		results := f.query("zebra")
		if got := listOf(results.Value("sessions")); len(got) != 1 || got[0] != session {
			t.Fatalf("sessions = %v", got)
		}
		if got := listOf(results.Value("messages")); len(got) != 0 {
			t.Fatalf("messages = %v", got)
		}
		all := stringsOf(f.query(f.fixture.Workspace[len(f.fixture.Workspace)-12:]).Value("sessions"))
		if len(all) != 2 || !contains(all, session) || !contains(all, other) {
			t.Fatalf("by directory = %v", all)
		}
	})

	t.Run("catches up after the index lost a range", func(t *testing.T) {
		f.onIndex("reset", delta.JsonObjectOf("session", session))
		if got := listOf(f.query("quokka").Value("messages")); len(got) != 0 {
			t.Fatalf("after the reset: %v", got)
		}
		// The next start of the worker scans from the server's cursor (now 0) again.
		f.stopWorker(session)
		chat := f.chat(session, 1)
		f.found("quokka", hitCount(2))
		f.send(chat, "a second wombat", "w")
		f.found("wombat", hitCount(2))
	})

	t.Run("searches archived sessions separately", func(t *testing.T) {
		if _, err := f.sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
			t.Fatal(err)
		}
		if got := listOf(f.query("quokka").Value("messages")); len(got) != 0 {
			t.Fatalf("active: %v", got)
		}
		if got := listOf(f.query("quokka", true).Value("messages")); len(got) != 2 {
			t.Fatalf("archived: %v", got)
		}
		if got := listOf(f.query("zebra", true).Value("sessions")); len(got) != 1 || got[0] != session {
			t.Fatalf("archived sessions: %v", got)
		}
		if _, err := f.sessions.Call("restore", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("limits a search to the given sessions before ranking and the hit limit (a sidebar list's own sessions)", func(t *testing.T) {
		// 45 sessions whose message ranks above the listed one's: an unscoped search's 41 hits leave it out.
		crowd := make([]string, 45)
		for index := range crowd {
			crowd[index] = f.create("Crowd " + strconv.Itoa(index))
		}
		listed := f.create("Listed")
		for _, id := range append(append([]string{}, crowd...), listed) {
			text := "pangolin pangolin pangolin"
			if id == listed {
				text = "pangolin facts, among other things"
			}
			f.onIndex("index", delta.JsonObjectOf("session", id, "conversation", float64(1), "messages", []any{
				delta.JsonObjectOf("conversationId", float64(1), "entryId", float64(1), "role", "user", "timestamp", float64(time.Now().UnixMilli()), "text", text),
			}, "through", float64(1)))
		}
		everywhere := listOf(f.query("pangolin").Value("messages"))
		if len(everywhere) != 41 {
			t.Fatalf("an unscoped search has %d hits, want 41", len(everywhere))
		}
		for _, each := range everywhere {
			if object(each).Value("session") == listed {
				t.Fatal("the unscoped search's 41 hits include the listed session")
			}
		}
		within := func(query string) *delta.JsonObject {
			results, err := f.search.Call("search", delta.JsonObjectOf("query", query, "sessions", []any{listed}), p2test.Background)
			if err != nil {
				t.Fatal(err)
			}
			return object(results)
		}
		if hits := listOf(within("pangolin").Value("messages")); len(hits) != 1 || object(hits[0]).Value("session") != listed {
			t.Fatalf("scoped hits = %v, want the listed session's alone", hits)
		}
		// Titles too: only the listed sessions'.
		if titles := listOf(within("crowd").Value("sessions")); len(titles) != 0 {
			t.Fatalf("scoped titles = %v, want none", titles)
		}
		for _, id := range append(append([]string{}, crowd...), listed) {
			if _, err := f.sessions.Call("archive", delta.JsonObjectOf("id", id), p2test.Background); err != nil {
				t.Fatal(err)
			}
		}
	})

	t.Run("indexes subagent messages with independent cursors", func(t *testing.T) {
		chat := f.chat(other, 1)
		f.send(chat, "subagent", "child")
		children, err := p2test.HoldIn(f.owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		listed, err := p2test.Until(children.Member("children"), func(value delta.JsonValue) bool { return len(listOf(object(value).Value(other))) > 0 }, 0)
		if err != nil {
			t.Fatal(err)
		}
		id := object(listOf(object(listed).Value(other))[0]).Value("conversationId").(float64)
		f.found("hello helper", func(results *delta.JsonObject) bool {
			for _, each := range listOf(results.Value("messages")) {
				if object(each).Value("conversationId") == id {
					return true
				}
			}
			return false
		})
		sub := f.chat(other, id)
		f.send(sub, "child axolotl", "ax")
		results := f.found("axolotl", hitCount(2))
		for _, each := range listOf(results.Value("messages")) {
			if hit := object(each); hit.Value("session") != other || hit.Value("conversationId") != id {
				t.Fatalf("hit = %v", hit)
			}
		}
		if f.cursor(other, id) <= 0 || f.cursor(other, 1) <= 0 {
			t.Fatal("a conversation has no cursor")
		}
		sub.Release()
		children.Release()
	})

	t.Run("derives a default title once and advances activity without undoing renames", func(t *testing.T) {
		fresh := f.create("")
		freshRow := func() *delta.JsonObject {
			rows, _ := f.sessions.Member("sessions").Value()
			for _, each := range listOf(rows) {
				if object(each).Value("id") == fresh {
					return object(each)
				}
			}
			return nil
		}
		created := freshRow().Value("updatedAt").(float64)
		chat := f.chat(fresh, 1)
		f.send(chat, "first capybara prompt", "first")
		f.found("capybara", hitCount(2))
		initial, err := p2test.Until(f.sessions.Member("sessions"), func(value delta.JsonValue) bool {
			for _, each := range listOf(value) {
				if row := object(each); row.Value("id") == fresh && row.Value("title") == "first capybara prompt" && row.Value("updatedAt").(float64) > created {
					return true
				}
			}
			return false
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		var timestamp float64
		for _, each := range listOf(initial) {
			if row := object(each); row.Value("id") == fresh {
				timestamp = row.Value("updatedAt").(float64)
			}
		}
		if _, err := f.sessions.Call("rename", delta.JsonObjectOf("id", fresh, "title", "Explicit title"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.send(chat, "second platypus prompt", "second")
		f.found("platypus", hitCount(2))
		if _, err := p2test.Until(f.sessions.Member("sessions"), func(value delta.JsonValue) bool {
			for _, each := range listOf(value) {
				if row := object(each); row.Value("id") == fresh && row.Value("title") == "Explicit title" && row.Value("updatedAt").(float64) > timestamp {
					return true
				}
			}
			return false
		}, 0); err != nil {
			t.Fatal(err)
		}
		f.onIndex("reset", delta.JsonObjectOf("session", fresh))
		f.stopWorker(fresh)
		f.chat(fresh, 1)
		rebuilt := f.found("capybara", hitCount(2))
		for _, each := range listOf(rebuilt.Value("messages")) {
			if object(each).Value("title") != "Explicit title" {
				t.Fatalf("hit = %v", each)
			}
		}
	})

	t.Run("takes index batches only from session workers", func(t *testing.T) {
		feed, err := f.owner.Hold(p2test.Background, searchcontract.SearchFeed, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, feed), data.CodeForbidden)
	})
}

// Pi main b30d54b8a: SearchIndex.mentions answers the sessions with a message matching a query at or after `since`, each
// with the time of its latest one (rollups' `mentions`); a query with no searchable word answers none.
func TestSearchIndexMentionsAnswersSessionsWithTheTimeOfTheirLatestMatch(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := newSearchFixture(t, hosts.ServerOptions{})
	first, second := f.create("First"), f.create("Second")
	for at, id := range []string{first, second} {
		f.onIndex("index", delta.JsonObjectOf("session", id, "conversation", float64(1), "messages", []any{
			delta.JsonObjectOf("conversationId", float64(1), "entryId", float64(1), "role", "user", "timestamp", float64(1000*(at+1)), "text", "axolotl pond"),
			delta.JsonObjectOf("conversationId", float64(1), "entryId", float64(2), "role", "user", "timestamp", float64(1000*(at+1)+500), "text", "axolotl again"),
		}, "through", float64(2)))
	}
	mentions := func(args *delta.JsonObject) *delta.JsonObject {
		var found delta.JsonValue
		if _, err := f.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			index, err := ctx.Get(searchcontract.SearchIndex, delta.NewJsonObject(0), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			found, err = index.Call("mentions", args, ctx)
			return nil, err
		}, p2test.Background, host.RootOptions{}); err != nil {
			t.Fatal(err)
		}
		return object(found)
	}
	all := mentions(delta.JsonObjectOf("query", "axolotl"))
	if all.Len() != 2 || all.Value(first) != float64(1500) || all.Value(second) != float64(2500) {
		t.Fatalf("mentions = %v", all)
	}
	since := mentions(delta.JsonObjectOf("query", "axolotl", "since", float64(2000)))
	if since.Len() != 1 || since.Value(second) != float64(2500) {
		t.Fatalf("mentions since 2000 = %v", since)
	}
	if none := mentions(delta.JsonObjectOf("query", "!!!")); none.Len() != 0 {
		t.Fatalf("a query without a word mentions %v", none)
	}
}

func TestSearchAcrossRestartsServesTheIndexWithoutStartingWorkers(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := newSearchFixture(t, hosts.ServerOptions{})
	session := f.create("")
	chat := f.chat(session, 1)
	f.send(chat, "persistent narwhal", "n")
	f.found("narwhal", hitCount(2))
	f.search.Release()
	f.sessions.Release()
	chat.Release()
	restarted := restart(t, f.fixture)
	conn, err := p2test.Dial(restarted.Port, p2test.SignInLocalOwner(t, restarted.Port, restarted.DataDir).OwnerHeaders(nil))
	if err != nil {
		t.Fatal(err)
	}
	client, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p2test.HoldIn(client, searchcontract.Search, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	results, err := again.Call("search", delta.JsonObjectOf("query", "narwhal"), p2test.Background)
	if err != nil || len(listOf(object(results).Value("messages"))) != 2 {
		t.Fatalf("after the restart: %v, %v", results, err)
	}
	workers, err := restarted.Host.Get(p2test.Background, p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer workers.Release()
	states, err := workers.Member("workers").Value()
	if err != nil || len(object(states).Keys()) != 0 {
		t.Fatalf("a worker was started: %v, %v", states, err)
	}
}

func TestRebuildIndexReIndexesActiveAndArchivedSessions(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := newSearchFixture(t, hosts.ServerOptions{})
	session := f.create("Indexed")
	chat := f.chat(session, 1)
	f.send(chat, "pangolin rebuild words", "one")
	if _, err := p2test.Until(chat.Member("view"), func(value delta.JsonValue) bool {
		encoded, _ := chordjson.Marshal(value)
		return strings.Contains(string(encoded), "Echo: pangolin rebuild words") && !running(value)
	}, 0); err != nil {
		t.Fatal(err)
	}
	hits := func(archived bool) int {
		count := 0
		for _, each := range listOf(f.query("pangolin", archived).Value("messages")) {
			if object(each).Value("session") == session {
				count++
			}
		}
		return count
	}
	if err := p2test.Poll(func() (bool, error) { return hits(false) == 2, nil }, 20*time.Second, "indexed"); err != nil {
		t.Fatal(err)
	}
	rebuilt := func() {
		t.Helper()
		if _, err := p2test.Until(f.search.Member("rebuilds"), func(value delta.JsonValue) bool { return len(listOf(value)) == 0 }, 0); err != nil {
			t.Fatal(err)
		}
	}
	chat.Release()

	// Emptied directly, the index stays empty until rebuilt.
	f.onIndex("reset", delta.JsonObjectOf("session", session))
	if hits(false) != 0 {
		t.Fatal("the reset index has hits")
	}
	// The rebuild is a job: the call answers once it started, `rebuilds` shows it running until the scan committed.
	if _, err := f.search.Call("rebuildIndex", delta.JsonObjectOf("session", session), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(f.search.Member("rebuilds"), func(value delta.JsonValue) bool {
		for _, each := range listOf(value) {
			if object(each).Value("session") == session {
				return true
			}
		}
		return false
	}, 0); err != nil {
		t.Fatal(err)
	}
	rebuilt()
	if hits(false) != 2 {
		t.Fatalf("hits after the rebuild = %d, want 2", hits(false))
	}

	// An archived session: its worker starts only to scan (no resumed work) and stops again.
	if _, err := f.sessions.Call("archive", delta.JsonObjectOf("id", session), p2test.Background); err != nil {
		t.Fatal(err)
	}
	f.onIndex("reset", delta.JsonObjectOf("session", session))
	if hits(true) != 0 {
		t.Fatal("the reset index has archived hits")
	}
	if _, err := f.search.Call("rebuildIndex", delta.JsonObjectOf("session", session), p2test.Background); err != nil {
		t.Fatal(err)
	}
	rebuilt()
	if hits(true) != 2 {
		t.Fatalf("archived hits after the rebuild = %d, want 2", hits(true))
	}
	workers, err := f.fixture.Server.Host.Get(p2test.Background, p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer workers.Release()
	if _, err := p2test.Until(workers.Member("workers"), func(value delta.JsonValue) bool {
		state := object(object(value).Value(session))
		return state != nil && state.Value("running") == false
	}, 0); err != nil {
		t.Fatal(err)
	}
	// Still archived: an ordinary start is refused.
	refused, err := f.owner.Hold(p2test.Background, chatcontract.Chat, delta.JsonObjectOf("session", session, "conversation", float64(1)), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A failed start leaves its holders waiting (the public error is fixed); the status names the reason.
	if failure, err := p2test.FailureOf(refused); err != nil || !strings.Contains(failure.Error(), "failed to start") {
		t.Fatalf("a refused start: %v, %v", failure, err)
	}
	if _, err := p2test.Until(workers.Member("workers"), func(value delta.JsonValue) bool {
		message, _ := object(object(value).Value(session)).Value("error").(string)
		return strings.Contains(strings.ToLower(message), "archived")
	}, 0); err != nil {
		t.Fatal(err)
	}
	refused.Release()
	if _, err := f.search.Call("rebuildIndex", delta.JsonObjectOf("session", "nope"), p2test.Background); err == nil || !strings.Contains(err.Error(), "Session not found") {
		t.Fatalf("rebuild of an unknown session: %v", err)
	}
}
