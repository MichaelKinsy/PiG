package clienttest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	searchcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/contract"
	searchtui "github.com/MichaelKinsy/PiG/apps/p2/plugins/search/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	subagentstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

const ctrlD = "\x04"

// p4Session is test/p2/tui-p4.test.ts `session(id, patch)`.
func p4Session(id string, patch func(*contract.Session)) contract.Session {
	session := fixtureSession()
	session.ID, session.Title, session.UpdatedAt = id, "Title "+id, time.Now().UnixMilli()-60_000
	if patch != nil {
		patch(&session)
	}
	return session
}

// p4SessionsStub is the Sessions stub recording every call; guests (owner false) see the catalog read-only.
func p4SessionsStub(c *calls, rows []contract.Session, owner bool) p2test.Stub {
	return p2test.Stub{Contract: contract.Sessions, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		if !owner {
			return p2test.StubMembers{Variant: "guest", State: map[string]any{"sessions": self.Cell(rowsOf(rows...))}}
		}
		cell := self.Cell(rowsOf(rows...))
		update := func(id any, patch func(*delta.JsonObject)) {
			_ = cell.Change(func(current any) any {
				var out []any
				for _, each := range current.([]any) {
					row := each.(*delta.JsonObject).Clone()
					if row.Value("id") == id {
						patch(row)
					}
					out = append(out, row)
				}
				return out
			})
		}
		return p2test.StubMembers{
			Variant: "owner",
			State:   map[string]any{"sessions": cell, "defaultCwd": "/work", "defaultEnv": nil, "operations": []any{}},
			Methods: map[string]p2test.StubMethod{
				"create": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("create", location{env: object.Value("env"), cwd: object.Value("cwd").(string)})
					c.record("created", object.Value("id"))
					created := p4Session(object.Value("id").(string), func(each *contract.Session) {
						each.Cwd = object.Value("cwd").(string)
						each.UpdatedAt = time.Now().UnixMilli()
					})
					_ = cell.Change(func(current any) any { return append(append([]any{}, current.([]any)...), created.JSON()) })
					return created.JSON(), nil
				},
				"rename": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("rename", [2]any{object.Value("id"), object.Value("title")})
					update(object.Value("id"), func(row *delta.JsonObject) { row.Set("title", object.Value("title")) })
					return nil, nil
				},
				"pin": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("pin", [2]any{object.Value("id"), object.Value("pinned")})
					update(object.Value("id"), func(row *delta.JsonObject) { row.Set("pinned", object.Value("pinned")) })
					return nil, nil
				},
				"archive": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("archive", object.Value("id"))
					update(object.Value("id"), func(row *delta.JsonObject) { row.Set("archivedAt", float64(time.Now().UnixMilli())) })
					return nil, nil
				},
				"restore": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("restore", object.Value("id"))
					update(object.Value("id"), func(row *delta.JsonObject) { row.Set("archivedAt", nil) })
					return nil, nil
				},
				"relocate": noop, "dismissOperation": noop, "clone": unused, "fork": unused,
			},
		}
	}}
}

type p4Setup struct {
	*tuitest.Topology
	calls *calls
}

// p4Stubs are the server stubs of the session selector and the chat, recording every call; rows is the catalog (read-only for a guest).
func p4Stubs(c *calls, rows []contract.Session, owner bool) []p2test.Stub {
	sessions := p4SessionsStub(c, rows, owner)
	subagents := p2test.Stub{Contract: subagentscontract.Subagents, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		child := delta.JsonObjectOf("name", "helper", "conversationId", float64(2), "model", "faux/faux-1", "thinkingLevel", "off", "tools", []any{}, "lastEntryId", float64(1))
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"children": delta.JsonObjectOf("s1", []any{child})}}
	}}
	search := p2test.Stub{Contract: searchcontract.Search, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{
			Variant: "owner", State: map[string]any{"rebuilds": []any{}},
			Methods: map[string]p2test.StubMethod{
				"search": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					query := args.(*delta.JsonObject).Value("query")
					hit := delta.JsonObjectOf("session", "s2", "conversationId", float64(1), "entryId", float64(1), "role", "user", "text", fmt.Sprintf("a %v in text", query), "title", "Title s2")
					return delta.JsonObjectOf("sessions", []any{}, "messages", []any{hit}), nil
				},
				"rebuildIndex": noop,
			},
		}
	}}
	// s1 works in its root and in its subagent's conversation; s3's worker failed; s2 has an unread answer.
	status := p2test.Stub{Contract: statuscontract.SessionStatus, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"statuses": delta.JsonObjectOf(
			"s1", delta.JsonObjectOf("running", true, "tasks", float64(2), "busy", []any{float64(1), float64(2)}),
			"s3", delta.JsonObjectOf("running", false, "tasks", float64(0), "busy", []any{}, "error", "spawn failed"),
		)}}
	}}
	reads := p2test.Stub{Contract: searchcontract.SessionReads, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
		return p2test.StubMembers{
			Variant: "owner", State: map[string]any{"answers": delta.JsonObjectOf("s2", float64(5)), "reads": delta.JsonObjectOf("s2", float64(3))},
			Methods: map[string]p2test.StubMethod{
				"markRead": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					c.record("markRead", [2]any{object.Value("session"), object.Value("through")})
					return nil, nil
				},
			},
		}
	}}
	return []p2test.Stub{sessions, subagents, search, status, reads}
}

// newP4Setup is the server stubs that record every call; rows is the catalog the owner sees.
func newP4Setup(t *testing.T, rows []contract.Session, screen string) *p4Setup {
	t.Helper()
	s := &p4Setup{calls: &calls{}}
	s.Topology = tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: p4Stubs(s.calls, rows, true), Worker: []p2test.Stub{chatWorker()},
		Facets:  []host.Facet{tui.CoreFacet, sessionstui.Facet, subagentstui.Facet, searchtui.Facet},
		Columns: 100,
	})
	s.Do(func() { s.Runtime.Navigate(screen, rows[0].ID, true, nil) })
	return s
}

func TestSessionSelectorScopesThreadedSubagentsMessageHitsArchiveAndRestoreRenamePin(t *testing.T) {
	rows := []contract.Session{
		p4Session("s1", func(s *contract.Session) { s.Title, s.UpdatedAt = "Alpha work", time.Now().UnixMilli()-1000 }),
		p4Session("s2", func(s *contract.Session) { s.Title, s.UpdatedAt = "Beta work", time.Now().UnixMilli()-5000 }),
		p4Session("s3", func(s *contract.Session) { s.Title, s.Cwd = "Elsewhere", "/other" }),
		p4Session("s4", func(s *contract.Session) { one := int64(1); s.Title, s.ArchivedAt = "Old archived", &one }),
	}
	s := newP4Setup(t, rows, "sessions")
	pollContains(t, s.Fixture, "Resume Session (Current Folder)")
	// The threaded rows wait for the subagents' children.
	pollScreen(t, s.Fixture, matches(`Alpha work[\s\S]*└─ helper[\s\S]*Beta work`))
	shown := s.Screen()
	if strings.Contains(shown, "Elsewhere") || strings.Contains(shown, "Old archived") {
		t.Fatalf("rows out of the scope show:\n%s", shown)
	}
	// Activity markers: working (the session, and the subagent whose conversation is busy), unread, a failed worker.
	pollScreen(t, s.Fixture, matches(`(?m)^ {2}◦ Beta work`))
	shown = s.Screen()
	if !matches(`(?m)^ {2}● Alpha work`)(shown) || !matches(`(?m)^ {2}● └─ helper`)(shown) {
		t.Fatalf("working markers:\n%s", shown)
	}
	s.Send("\t")
	pollContains(t, s.Fixture, "Elsewhere")
	if !matches(`(?m)^ {2}! Elsewhere`)(s.Screen()) {
		t.Fatalf("failed worker marker:\n%s", s.Screen())
	}
	// Literal message hits from Search, below the sessions.
	typed(s.Fixture, "needle")
	pollContains(t, s.Fixture, "Messages (literal matches)")
	if !matches(`Title s2 — a needle in text`)(s.Screen()) {
		t.Fatalf("message hit:\n%s", s.Screen())
	}
	s.Send(esc) // clears the query
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Messages (literal matches)") })
	// Down to Alpha work (first row is "+ New session"), archive with confirmation.
	s.Send(down)
	s.Send(ctrlD)
	pollContains(t, s.Fixture, `Archive "Alpha work"?`)
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("archive") == 1 })
	if got := s.calls.get("archive")[0]; got != "s1" {
		t.Fatalf("archive = %v", got)
	}
	// A list row.
	listed := func(title string) func(string) bool { return matches(`(?m)^(?: {2}|› )[●!◦ ] ` + title) }
	pollScreen(t, s.Fixture, func(screen string) bool { return !listed("Alpha work")(screen) })
	s.Send(ctrlA)
	pollContains(t, s.Fixture, "Archived Sessions")
	pollScreen(t, s.Fixture, listed("Alpha work"))
	s.Send(ctrlD) // restores in the archived scope
	tuitest.Poll(t, func() bool { return s.calls.count("restore") == 1 })
	s.Send(ctrlA)
	pollContains(t, s.Fixture, "Resume Session")
	// Rename and pin the selected session.
	pollScreen(t, s.Fixture, listed("Alpha work"))
	s.Send(down)
	s.Send(ctrlR)
	pollContains(t, s.Fixture, "Rename session")
	s.Send(ctrlA) // line start, then Ctrl+K: the prompt is empty
	s.Send(ctrlK)
	typed(s.Fixture, "Renamed")
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.calls.count("rename") == 1 })
	if got := s.calls.get("rename")[0].([2]any)[1]; got != "Renamed" {
		t.Fatalf("rename = %v", got)
	}
	pollContains(t, s.Fixture, "Renamed")
	s.Send(altP)
	tuitest.Poll(t, func() bool { return s.calls.count("pin") == 1 })
	// A subagent row opens its conversation: the (session, conversation) identity.
	pollContains(t, s.Fixture, "helper")
	lines := strings.Split(s.Screen(), "\n")
	selected, helper := -1, -1
	for i, line := range lines {
		if selected < 0 && strings.Contains(line, "›") {
			selected = i
		}
		if helper < 0 && strings.Contains(line, "helper") {
			helper = i
		}
	}
	for step := selected; step < helper; step++ {
		s.Send(down)
	}
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.screenOf() == "chat" })
	identity := ""
	s.Do(func() { identity = s.Runtime.Identity() })
	if identity != "s1#2" {
		t.Fatalf("identity = %q", identity)
	}
}

func (s *p4Setup) screenOf() string {
	name := ""
	s.Do(func() { name = s.Runtime.Screen })
	return name
}

func TestTheChatShowingASessionsMainConversationReadsItThroughItsLatestAnswerOnce(t *testing.T) {
	rows := []contract.Session{p4Session("s1", nil), p4Session("s2", func(s *contract.Session) { s.Title = "Beta work" })}
	s := newP4Setup(t, rows, "sessions")
	// The selector marks nothing; s2's unread answer shows as its marker.
	pollScreen(t, s.Fixture, matches(`(?m)^ {2}◦ Beta work`))
	if s.calls.count("markRead") != 0 {
		t.Fatal("the selector marked an answer read")
	}
	s.Do(func() { s.Runtime.Navigate("chat", "s2", true, nil) })
	tuitest.Poll(t, func() bool { return s.calls.count("markRead") == 1 })
	if got := s.calls.get("markRead")[0]; got != [2]any{"s2", float64(5)} {
		t.Fatalf("markRead = %v", got)
	}
	// Further changes while the stub's reads stay behind send no second call for the same answer.
	s.Do(func() { s.Runtime.NavigateScreen("chat") })
	time.Sleep(100 * time.Millisecond)
	if s.calls.count("markRead") != 1 {
		t.Fatalf("markRead calls = %d", s.calls.count("markRead"))
	}
}

var _ sync.Mutex
