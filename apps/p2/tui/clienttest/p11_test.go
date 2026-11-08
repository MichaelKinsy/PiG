package clienttest

// test/p2/tui-p11.test.ts: the web-parity panels in the TUI (P11h /agents and the subagent crumb, P11b chat mode, the later P11 groups below).

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	artifactscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/contract"
	artifactstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/tui"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	chattui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/tui"
	chatmodecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/contract"
	chatmodetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode/tui"
	sendfilecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/contract"
	sendfiletui "github.com/MichaelKinsy/PiG/apps/p2/plugins/send-file/tui"
	sessionstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/tui"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	statustui "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/tui"
	workspacecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	workspacetui "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/tui"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	subagentstui "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/tui"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

const (
	pageDown = "\x1b[6~"
	ctrlT    = "\x14"
	ctrlP    = "\x10"
	f2       = "\x1bOQ"
	ctrlU    = "\x15"
)

// p11Entry is an entry with data (no model message): a chat message or mention.
func p11Entry(id int, kind string, data *delta.JsonObject) durable.EntryRecord {
	entry := tuitest.Entry(id, kind)
	entry.Data = data
	return entry
}

// p11Child is test/p2/tui-p11.test.ts `child`.
func p11Child(name string, conversation int, extra ...any) *delta.JsonObject {
	child := delta.JsonObjectOf("name", name, "conversationId", float64(conversation), "model", "faux/faux-1", "thinkingLevel", "off", "tools", []any{"read"}, "lastEntryId", float64(1))
	for i := 0; i+1 < len(extra); i += 2 {
		child.Set(extra[i].(string), extra[i+1])
	}
	return child
}

type p11Options struct {
	views    func(conversation int) delta.JsonValue
	children []any
	chatMode bool
	viewer   bool
	server   []p2test.Stub
	worker   []p2test.Stub
	facets   []host.Facet
	role     string
	calls    *calls
}

type p11Setup struct {
	*tuitest.Topology
	calls *calls
	chat  *tuitest.ChatStub
	mode  *p2test.Cell
}

func p11Facets() []host.Facet {
	return []host.Facet{tui.CoreFacet, chattui.NewFacet(), sessionstui.Facet, statustui.Facet, subagentstui.Facet, chatmodetui.Facet}
}

func viewWithAgent(t testing.TB, entries ...durable.EntryRecord) delta.JsonValue {
	return tuitest.View(t, 1, entries, tuitest.DocsOf(t, "pi.agent", harness.AgentState{}), 0)
}

// newP11 is test/p2/tui-p11.test.ts `setup`.
func newP11(t *testing.T, options p11Options) *p11Setup {
	t.Helper()
	s := &p11Setup{calls: options.calls}
	if s.calls == nil {
		s.calls = &calls{}
	}
	views := options.views
	if views == nil {
		views = func(int) delta.JsonValue { return viewWithAgent(t) }
	}
	variant := "participant"
	if options.viewer {
		variant = "viewer"
	}
	chatOptions := tuitest.ChatStubOptions{Variant: variant, Views: views, Model: metaJSON()}
	if !options.viewer {
		chatOptions.Methods = map[string]p2test.StubMethod{
			"send": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				object := args.(*delta.JsonObject)
				record := delta.JsonObjectOf("text", object.Value("text"))
				if replyTo, ok := object.Value("replyTo").(float64); ok {
					record.Set("replyTo", replyTo)
				}
				s.calls.record("send", record)
				return delta.JsonObjectOf("submissionId", float64(1)), nil
			},
		}
	}
	s.chat = tuitest.NewChatStub(t, chatOptions)
	children := options.children
	if children == nil {
		children = []any{}
	}
	server := []p2test.Stub{
		tuitest.SessionsStub(tuitest.Session),
		{Contract: subagentscontract.Subagents, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"children": delta.JsonObjectOf("s", children)}}
		}},
		{Contract: statuscontract.SessionMetrics, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"clients": float64(1), "usage": nil}}
		}},
	}
	worker := []p2test.Stub{
		s.chat.Stub,
		tuitest.DraftsStub(),
		{Contract: subagentscontract.SubagentControl, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "participant", Methods: map[string]p2test.StubMethod{
				"archive": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					s.calls.record("archive", [2]any{object.Value("conversation"), object.Value("archived")})
					return nil, nil
				},
			}}
		}},
		{Contract: chatmodecontract.ChatMode, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			enabled := self.Cell(options.chatMode)
			s.mode = enabled
			name := self.Cell("Pi")
			return p2test.StubMembers{Variant: "owner", State: map[string]any{"enabled": enabled, "waiting": float64(0), "name": name}, Methods: map[string]p2test.StubMethod{
				"set": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					on := args.(*delta.JsonObject).Value("enabled").(bool)
					s.calls.record("setChatMode", on)
					return nil, enabled.Replace(on)
				},
				"rename": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					next := args.(*delta.JsonObject).Value("name")
					s.calls.record("renameAgent", next)
					return nil, name.Replace(next)
				},
			}}
		}},
	}
	facets := options.facets
	if facets == nil {
		facets = p11Facets()
	}
	s.Topology = tuitest.NewTopology(t, tuitest.TopologyOptions{
		Server: append(server, options.server...), Worker: append(worker, options.worker...), Facets: facets, Columns: 100, Role: roleOf(options.role),
	})
	// The topology already views the session (a second navigation would clear the notices that arrived).
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Connecting conversation") })
	return s
}

func (s *p11Setup) identity() (identity string) {
	s.Do(func() { identity = s.Runtime.Identity() })
	return identity
}

func (s *p11Setup) draft(identity string) (draft string) {
	s.Do(func() { draft = s.Runtime.Drafts[identity] })
	return draft
}

func (s *p11Setup) run(id string) (ran bool) {
	s.Do(func() { ran = s.Runtime.Ctx.Commands().Run(id, "") })
	return ran
}

func (s *p11Setup) waitCalls(t *testing.T, name string, count int) {
	t.Helper()
	tuitest.Poll(t, func() bool { return s.calls.count(name) >= count })
}

func TestAgentsOpensSubagentConversationsWithTheirOwnDraftsTheFooterCrumbArchive(t *testing.T) {
	answer := func(conversation int) delta.JsonValue {
		return viewWithAgent(t, tuitest.AnswerEntry(1, "answer in conversation "+strconv.Itoa(conversation), 1))
	}
	s := newP11(t, p11Options{views: answer, children: []any{
		p11Child("helper", 2), p11Child("old", 3, "archived", true), p11Child("classifier", 4, "kind", "agent"),
	}})
	typed(s.Fixture, "main draft")
	tuitest.Poll(t, func() bool { return s.draft("s#1") == "main draft" })
	s.run("p2.agents")
	// The children are followed from the server: the list shows them once they arrive.
	pollScreen(t, s.Fixture, matches(`(?s)Conversations · active.*main.*helper.*agent classifier`))
	shown := s.Screen()
	if strings.Contains(shown, "old (archived)") {
		t.Fatal("the archived subagent is listed")
	}
	s.Send(down)
	s.Send(enter)
	tuitest.Poll(t, func() bool { return s.identity() == "s#2" })
	pollContains(t, s.Fixture, "answer in conversation 2")
	mustMatch(t, `/work • Fixture › helper`, s.Screen())
	typed(s.Fixture, "child draft")
	tuitest.Poll(t, func() bool { return s.draft("s#2") == "child draft" })
	s.run("p2.agents.main")
	tuitest.Poll(t, func() bool { return s.identity() == "s#1" })
	pollContains(t, s.Fixture, "main draft")
	if got := s.draft("s#2"); got != "child draft" {
		t.Fatalf("the subagent's draft = %q", got)
	}
	// Archive the subagent from the list.
	s.run("p2.agents")
	pollContains(t, s.Fixture, "Conversations · active")
	s.Send(down)
	s.Send(ctrlD)
	s.waitCalls(t, "archive", 1)
	if got := s.calls.get("archive")[0]; got != [2]any{float64(2), true} {
		t.Fatalf("archive = %v", got)
	}
	s.Send(ctrlA) // Ctrl+A: archived ones too
	pollContains(t, s.Fixture, "old (archived)")
	mustMatch(t, `Conversations · with archived`, s.Screen())
}

func TestChatModeChipQuotedRepliesAndNotesChatModeOwnerReplyAtPiFirst(t *testing.T) {
	alice := func() *delta.JsonObject { return delta.JsonObjectOf("kind", "guest", "id", "alice", "name", "Alice") }
	s := newP11(t, p11Options{chatMode: true, views: func(int) delta.JsonValue {
		return viewWithAgent(t,
			p11Entry(1, "chat.message", delta.JsonObjectOf("author", alice(), "text", "is the parser done?", "attachments", []any{})),
			p11Entry(2, "chat.message", delta.JsonObjectOf("author", delta.JsonObjectOf("kind", "owner", "id", "owner", "name", "Owner"), "text", "almost", "attachments", []any{},
				"replyTo", delta.JsonObjectOf("entryId", float64(1), "author", alice(), "excerpt", "is the parser done?"))),
			p11Entry(3, "chat.mention", delta.JsonObjectOf("author", alice(), "text", "@pi summarize", "attachments", []any{}, "included", []any{float64(1), float64(2)})),
		)
	}})
	// Views arrive asynchronously: the whole transcript, not just its first entry.
	pollContains(t, s.Fixture, "with 2 chat messages")
	shown := s.Screen()
	mustMatch(t, `Alice \(guest, unverified name\)`, shown)
	mustMatch(t, `↪ Alice: is the parser done\?`, shown)
	mustMatch(t, `with 2 chat messages`, shown)
	mustMatch(t, ` Chat {2}messages go to the people here`, shown)
	// Replies: pick the newest message, the next send carries its entry.
	typed(s.Fixture, "/reply")
	s.Send(enter)
	pollContains(t, s.Fixture, "Reply to")
	s.Send(enter)
	pollContains(t, s.Fixture, "↪ Replying to Alice")
	typed(s.Fixture, "yes, done")
	s.Send(enter)
	s.waitCalls(t, "send", 1)
	if got := s.calls.get("send")[0].(*delta.JsonObject).Value("replyTo"); got != float64(3) {
		t.Fatalf("replyTo = %v", got)
	}
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "↪ Replying to") })
	// @pi is offered first while chat mode is on.
	typed(s.Fixture, "ask @p")
	pollContains(t, s.Fixture, "mention Pi")
	s.Send(tab)
	tuitest.Poll(t, func() bool { return s.draft("s#1") == "ask @pi " })
	s.Send(ctrlU)
	// The owner switches chat mode off; replies end with it.
	s.Do(func() { s.Runtime.Role = tuiruntime.RoleOwner; s.Runtime.Changed() })
	typed(s.Fixture, "/chat-mode")
	s.Send(enter)
	s.waitCalls(t, "setChatMode", 1)
	if got := s.calls.get("setChatMode")[0]; got != false {
		t.Fatalf("setChatMode = %v", got)
	}
	tuitest.Poll(t, noticeText(s.Fixture, "Chat mode off"))
	pollContains(t, s.Fixture, "[Chat off · /chat-mode]")
	if s.run("p2.chatMode.reply") {
		t.Fatal("replies run outside chat mode")
	}
}

var _ = sync.Mutex{}
var _ = chatcontract.Chat
var _ = ai.StopReasonStop
var _ = strings.Contains

func mustMatch(t testing.TB, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(`(?s)` + pattern).MatchString(text) {
		t.Fatalf("%q does not match:\n%s", pattern, text)
	}
}

// noticeText is a condition: a notice of any kind says text.
func noticeText(f *tuitest.Fixture, text string) func() bool {
	return func() bool {
		found := false
		f.Do(func() {
			for _, notice := range f.Runtime.Notices {
				if notice.Text == text {
					found = true
				}
			}
		})
		return found
	}
}

func noticeContaining(f *tuitest.Fixture, text string) func() bool {
	return func() bool {
		found := false
		f.Do(func() {
			for _, notice := range f.Runtime.Notices {
				if strings.Contains(notice.Text, text) {
					found = true
				}
			}
		})
		return found
	}
}

func countNotices(f *tuitest.Fixture, text string) (count int) {
	f.Do(func() {
		for _, notice := range f.Runtime.Notices {
			if notice.Text == text {
				count++
			}
		}
	})
	return count
}

func TestOwnerDiagnosticsNoticesOnceNoticesDismissesLogsFollowsSourcesWorkerRetryDevReload(t *testing.T) {
	c := &calls{}
	stubs, cells := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets()})
	g := &grants{}
	g.answer(s, tuiruntime.GrantResult{OK: true, Value: map[string]any{"message": "Reloaded chat, status"}})
	// Pending notices are summarized once; the failed worker is reported with its retry command.
	tuitest.Poll(t, noticeText(s.Fixture, "1 owner notice · /notices"))
	tuitest.Poll(t, noticeContaining(s.Fixture, "Session worker failed: worker exited with code 1"))
	// A new notice shows as it arrives, exactly once.
	_ = cells.Notices().Change(func(current any) any {
		return append(append([]any{}, current.([]any)...), delta.JsonObjectOf("id", float64(2), "time", float64(2), "source", "session:s", "message", "disk full"))
	})
	tuitest.Poll(t, noticeText(s.Fixture, "Worker s: disk full"))
	if n := countNotices(s.Fixture, "Worker s: disk full"); n != 1 {
		t.Fatalf("the notice showed %d times", n)
	}
	s.run("p2.notices")
	pollContains(t, s.Fixture, "Owner notices (2)")
	s.Send(ctrlD)
	s.waitCalls(t, "dismiss", 1)
	if got := s.calls.get("dismiss"); len(got) != 1 || got[0] != float64(1) {
		t.Fatalf("dismiss = %v", got)
	}
	pollContains(t, s.Fixture, "Owner notices (1)")
	s.Send(esc)
	// Logs: the viewed session's worker first, newest line selected; Tab moves to the next source.
	s.run("p2.logs")
	pollContains(t, s.Fixture, "Logs · Worker s (2 lines)")
	mustMatch(t, `→ last line of session:s`, s.Screen())
	s.Send(tab)
	pollContains(t, s.Fixture, "Logs · Server (2 lines)")
	if got := s.calls.get("logs"); len(got) != 2 || got[0] != "session:s" || got[1] != "server" {
		t.Fatalf("logs = %v", got)
	}
	s.Send(esc)
	s.run("p2.worker.retry")
	s.waitCalls(t, "retry", 1)
	if got := s.calls.get("retry"); len(got) != 1 || got[0] != "s" {
		t.Fatalf("retry = %v", got)
	}
	s.run("p2.dev.reload")
	tuitest.Poll(t, func() bool { return g.count() == 1 })
	if got := g.at(0); got[0] != "devCommand" || !equalPayload(got[1], map[string]any{"action": "reload"}) {
		t.Fatalf("grant = %v", got)
	}
	tuitest.Poll(t, noticeText(s.Fixture, "Reloaded chat, status"))
}

func equalPayload(got, want any) bool {
	return strings.TrimSpace(fmtJSON(got)) == strings.TrimSpace(fmtJSON(want))
}

func TestGuestsNeverAcquireOwnerObjectsAndCannotOpenOwnerPanels(t *testing.T) {
	c := &calls{}
	stubs, _ := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets(), role: "guest"})
	for _, id := range []string{"p2.logs", "p2.notices", "p2.worker.retry", "p2.dev.reload", "p2.envs", "p2.ports", "p2.devices", "p2.share"} {
		if s.run(id) {
			t.Fatalf("%s is owner-only", id)
		}
	}
	// Nothing is acquired behind the guest's back: a settled period shows no owner object held (the stubs record an acquisition on construction).
	s.Do(func() {})
	if c.count("noticesAcquired") != 0 || c.count("logs") != 0 {
		t.Fatalf("a guest acquired owner objects: notices %d, logs %d", c.count("noticesAcquired"), c.count("logs"))
	}
	s.Do(func() {
		for _, notice := range s.Runtime.Notices {
			if regexp.MustCompile(`forbidden|owner-only`).MatchString(notice.Text) {
				t.Errorf("a guest saw %q", notice.Text)
			}
		}
	})
}

func TestEnvsTestsAnEnvironmentAHostKeyIsTrustedOnlyAfterConfirmingItsFingerprints(t *testing.T) {
	c := &calls{}
	stubs, _ := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets()})
	s.run("p2.envs")
	pollContains(t, s.Fixture, "Environments (2)")
	// Opening the screen starts the environments' SSH connections (the web picker's warm-up).
	s.waitCalls(t, "warm", 1)
	pollScreen(t, s.Fixture, matches(`(?s)This device.*box {2}me@box\.example.*untested · 2 sessions`))
	s.Send(down) // past "This device"
	s.Send(ctrlT)
	s.waitCalls(t, "test", 1)
	if got := c.get("test"); len(got) != 1 || got[0] != "box" {
		t.Fatalf("test = %v", got)
	}
	pollContains(t, s.Fixture, "Testing box: done")
	// The unverified key: Ctrl+K reviews it (Enter moves sessions); declining trusts nothing, confirming sends exactly its lines.
	s.Send(down)
	s.Send(ctrlK)
	pollContains(t, s.Fixture, "Trust lab's host key?")
	mustMatch(t, `SHA256:abc`, s.Screen())
	s.Send(esc)
	pollContains(t, s.Fixture, "Environments (2)")
	if c.count("acceptHostKey") != 0 {
		t.Fatal("declining trusted the key")
	}
	s.Send(ctrlK)
	pollContains(t, s.Fixture, "Trust lab's host key?")
	typed(s.Fixture, "y")
	s.waitCalls(t, "acceptHostKey", 1)
	got := c.get("acceptHostKey")[0].(*delta.JsonObject)
	if got.Value("name") != "lab" || fmtJSON(got.Value("lines")) != "[box ssh-ed25519 AAAA]" {
		t.Fatalf("acceptHostKey = %v", got)
	}
}

func TestPortsShowsRadiusAddressesOnlyMakingAForwardPublicNeedsAConfirmation(t *testing.T) {
	c := &calls{}
	stubs, _ := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets()})
	s.run("p2.ports")
	pollContains(t, s.Fixture, "Ports (2)")
	mustMatch(t, `fwd--a{32}--x1\.radius\.example`, s.Screen())
	s.Send(down)
	pollContains(t, s.Fixture, "no address yet")
	if strings.Contains(s.Screen(), "evil.example") {
		t.Fatal("a non-Radius address is shown")
	}
	s.Send(ctrlP)
	pollContains(t, s.Fixture, "Make port 9000 public?")
	s.Send(esc)
	pollContains(t, s.Fixture, "Ports (2)")
	if c.count("setPublic") != 0 {
		t.Fatal("declining made it public")
	}
	s.Send(ctrlP)
	pollContains(t, s.Fixture, "Make port 9000 public?")
	typed(s.Fixture, "y")
	s.waitCalls(t, "setPublic", 1)
	got := c.get("setPublic")[0].(*delta.JsonObject)
	if got.Value("id") != "f2" || got.Value("public") != true {
		t.Fatalf("setPublic = %v", got)
	}
	s.Send(esc)
	// `open … public` confirms before anything is added.
	s.Do(func() { s.Runtime.Ctx.Commands().Run("p2.ports", "open 3000 api public") })
	pollContains(t, s.Fixture, "Make port 3000 public?")
	typed(s.Fixture, "y")
	s.waitCalls(t, "addPort", 1)
	added := c.get("addPort")[0].(*delta.JsonObject)
	if added.Value("port") != float64(3000) || added.Value("name") != "api" || added.Value("public") != true {
		t.Fatalf("addPort = %v", added)
	}
}

func TestDevicesMintsATokenAndSignsOutThroughTheLaunchersConsent(t *testing.T) {
	c := &calls{}
	stubs, _ := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets()})
	g := &grants{}
	g.answer(s, tuiruntime.GrantResult{Error: "denied"})
	s.run("p2.devices")
	pollContains(t, s.Fixture, "Generate a one-time sign-in token")
	s.Send(enter)
	pollContains(t, s.Fixture, "one-time-token-123")
	mustMatch(t, `full owner access`, s.Screen())
	s.Send(esc)
	pollContains(t, s.Fixture, "Sign out all devices")
	s.Send(down)
	s.Send(down)
	s.Send(enter)
	tuitest.Poll(t, func() bool { return g.count() == 1 })
	if got := g.at(0); got[0] != "signOut" || fmtJSON(got[1]) != "map[all:true]" {
		t.Fatalf("grant = %v", got)
	}
	tuitest.Poll(t, noticeText(s.Fixture, "Not signed out"))
}

// workspaceStub is test/p2/tui-p11.test.ts's Workspace: a reader whose `changes` counter the test bumps; branch is the repo's ("" = detached HEAD).
type workspaceStub struct {
	Stub    p2test.Stub
	mu      sync.Mutex
	branch  string
	changes []*p2test.Cell
}

func (w *workspaceStub) setBranch(branch string) {
	w.mu.Lock()
	w.branch = branch
	w.mu.Unlock()
}

// bump counts a change of the files in every live instance.
func (w *workspaceStub) bump() {
	w.mu.Lock()
	cells := append([]*p2test.Cell(nil), w.changes...)
	w.mu.Unlock()
	for _, cell := range cells {
		_ = cell.Change(func(current any) any { return current.(float64) + 1 })
	}
}

func newWorkspaceStub(c *calls) *workspaceStub {
	w := &workspaceStub{branch: "main"}
	entry := func(name, kind string, size float64) *delta.JsonObject {
		return delta.JsonObjectOf("name", name, "kind", kind, "size", size, "mtimeMs", float64(1))
	}
	w.Stub = p2test.Stub{Contract: workspacecontract.Workspace, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		counter := self.Cell(float64(0))
		w.mu.Lock()
		w.changes = append(w.changes, counter)
		w.mu.Unlock()
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"changes": counter}, Methods: map[string]p2test.StubMethod{
			"query": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
				query := args.(*delta.JsonObject)
				c.record("query", query)
				path, _ := query.Value("path").(string)
				switch query.Value("kind") {
				case "list":
					if path == "" {
						return delta.JsonObjectOf("kind", "list", "path", "", "truncated", false, "entries", []any{entry("README.md", "file", 12), entry("src", "directory", 0)}), nil
					}
					return delta.JsonObjectOf("kind", "list", "path", path, "truncated", false, "entries", []any{entry("main.ts", "file", 30)}), nil
				case "file":
					return delta.JsonObjectOf("kind", "file", "path", path, "size", float64(30), "binary", false, "truncated", false,
						"text", "// "+path+"\nconst answer = 42;\n\x1b]52;c;evil\x07export {};"), nil
				case "status":
					w.mu.Lock()
					branch := w.branch
					w.mu.Unlock()
					result := delta.JsonObjectOf("kind", "status", "repo", true, "head", "0123456789abcdef", "truncated", false,
						"changes", []any{delta.JsonObjectOf("path", "src/main.ts", "status", ".M")})
					if branch != "" {
						result.Set("branch", branch)
					}
					return result, nil
				case "diff":
					return delta.JsonObjectOf("kind", "diff", "path", path, "full", false, "truncated", false,
						"patch", "diff --git a/src/main.ts b/src/main.ts\n--- a/src/main.ts\n+++ b/src/main.ts\n@@ -1,2 +1,2 @@\n // src/main.ts\n-const answer = 41;\n+const answer = 42;\n"), nil
				case "search":
					return delta.JsonObjectOf("kind", "search", "text", query.Value("text"), "path", "", "truncated", false,
						"matches", []any{delta.JsonObjectOf("path", "src/main.ts", "line", float64(2), "text", "const answer = 42;")}), nil
				}
				return nil, errors.New("unexpected")
			},
		}}
	}}
	return w
}

func TestFilesChangesAndGrepReadTheWorkspaceParticipantsGuestsAndReadersCannot(t *testing.T) {
	c := &calls{}
	workspace := newWorkspaceStub(c)
	s := newP11(t, p11Options{calls: c, worker: []p2test.Stub{workspace.Stub}, facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, workspacetui.Facet}})
	// pi's footer: `cwd (branch)`, asked again when files change; a detached HEAD says so.
	pollContains(t, s.Fixture, "/work (main) • Fixture")
	workspace.setBranch("feature")
	workspace.bump()
	pollContains(t, s.Fixture, "/work (feature) • Fixture")
	workspace.setBranch("")
	workspace.bump()
	pollContains(t, s.Fixture, "/work (detached) • Fixture")
	workspace.setBranch("main")
	c.reset("query")
	s.run("p2.files")
	pollContains(t, s.Fixture, "Files: .")
	pollScreen(t, s.Fixture, matches(`(?s)src/.*README\.md`))
	s.Send(enter) // src/ (directories first)
	pollContains(t, s.Fixture, "Files: src")
	s.Send(down) // past ../
	s.Send(enter)
	pollContains(t, s.Fixture, "const answer = 42;")
	if strings.Contains(s.Screen(), "52;c;evil\x07") {
		t.Fatal("control sequences in file text are not stripped")
	}
	s.Send(esc)
	s.Send(esc)
	s.run("p2.changes")
	pollContains(t, s.Fixture, "Changes on main (1)")
	s.Send(enter)
	pollContains(t, s.Fixture, "Changes: src/main.ts")
	diff := s.Screen()
	mustMatch(t, `-2 const answer = 41;`, diff)
	mustMatch(t, `\+2 const answer = 42;`, diff)
	s.Send(esc)
	s.Send(esc)
	s.Do(func() { s.Runtime.Ctx.Commands().Run("p2.grep", "answer") })
	pollContains(t, s.Fixture, `Search "answer" (1)`)
	mustMatch(t, `src/main\.ts:2`, s.Screen())
	s.Send(enter)
	pollScreen(t, s.Fixture, func(screen string) bool { return strings.Contains(screen, "src/main.ts") && strings.Contains(screen, "1-3 of 3") })
	s.Send(esc)
	s.Send(esc)
	// The panels' own queries (the footer asks only when files change).
	var kinds []string
	for _, query := range c.get("query") {
		kinds = append(kinds, query.(*delta.JsonObject).Value("kind").(string))
	}
	if got, want := strings.Join(kinds, ","), "list,list,file,status,diff,search,file"; got != want {
		t.Fatalf("queries = %s, want %s", got, want)
	}
}

func TestViewOnlyGuestsGetNoWorkspaceCommandsAndNeverAcquireWorkspace(t *testing.T) {
	c := &calls{}
	acquired := p2test.Stub{Contract: workspacecontract.Workspace, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
		c.record("workspace", true)
		return p2test.StubMembers{Variant: "reader", State: map[string]any{"changes": float64(0)}, Methods: map[string]p2test.StubMethod{"query": unusedMethod}}
	}}
	s := newP11(t, p11Options{calls: c, viewer: true, role: "guest", worker: []p2test.Stub{acquired}, facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, workspacetui.Facet}})
	for _, id := range []string{"p2.files", "p2.changes", "p2.grep"} {
		ran := false
		s.Do(func() { ran = s.Runtime.Ctx.Commands().Run(id, "x") })
		if ran {
			t.Fatalf("%s needs send access", id)
		}
	}
	s.Do(func() {})
	if c.count("workspace") != 0 {
		t.Fatal("a view-only guest acquired Workspace")
	}
}

func TestPiDiffTurnsAUnifiedPatchIntoPisNumberedDiffLines(t *testing.T) {
	got := workspacetui.PiDiff("--- a/x\n+++ b/x\n@@ -3,3 +3,3 @@\n keep\n-old\n+new\n@@ -20 +20 @@\n-a\n+b\n\\ No newline at end of file\n")
	// Numbers are padded to the widest one, as pi's edit diffs are.
	if want := strings.Join([]string{"  3 keep", "- 4 old", "+ 4 new", "    ...", "-20 a", "+20 b"}, "\n"); got != want {
		t.Fatalf("PiDiff = %q, want %q", got, want)
	}
}

func TestShareCopiesLinksThroughTheLauncherConfirmsARemoteReadWriteLinkResetsLinksAndRemovesGuests(t *testing.T) {
	c := &calls{}
	stubs, _ := ownerStubs(c)
	s := newP11(t, p11Options{calls: c, server: stubs, facets: ownerFacets()})
	g := &grants{}
	g.answer(s, tuiruntime.GrantResult{OK: true})
	s.run("p2.share")
	pollContains(t, s.Fixture, "Share · 1 guest connected")
	shown := s.Screen()
	mustMatch(t, `Read-only link.*Read/write link.*Alice \(unverified name\)`, shown)
	mustMatch(t, `change files on the remote\s+environment "box"`, shown)
	link := func(i int) string { return fmtJSON(c.get("link")[i].(*delta.JsonObject)) }
	// Read-only: created on first use, then copied through the launcher's consent screen.
	s.Send(enter)
	tuitest.Poll(t, func() bool { return g.count() == 1 })
	if got := link(0); got != "map[mode:view]" {
		t.Fatalf("link = %s", got)
	}
	if got := g.at(0); got[0] != "copy" || fmtJSON(got[1]) != "map[label:Read-only share link text:https://share.test/join/tok-view-1]" {
		t.Fatalf("grant = %v", got)
	}
	tuitest.Poll(t, noticeText(s.Fixture, "Read-only link copied"))
	// Read/write on a remote environment: confirmed before the link exists; declining creates nothing.
	s.Send(down)
	s.Send(enter)
	pollContains(t, s.Fixture, "Create a read/write link?")
	s.Send(esc)
	pollContains(t, s.Fixture, "Share · 1 guest")
	if c.count("link") != 1 {
		t.Fatal("declining created a link")
	}
	s.Send(enter)
	pollContains(t, s.Fixture, "Create a read/write link?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return g.count() == 2 })
	if got := link(1); got != "map[mode:full remoteConfirmed:true]" {
		t.Fatalf("link = %s", got)
	}
	if got := g.at(1); got[0] != "copy" || fmtJSON(got[1]) != "map[label:Read/write share link text:https://share.test/join/tok-full-2]" {
		t.Fatalf("grant = %v", got)
	}
	// Reset the read-only link; remove the guest. Both confirmed.
	s.Send(up)
	s.Send(ctrlR)
	pollContains(t, s.Fixture, "New read-only link?")
	typed(s.Fixture, "y")
	tuitest.Poll(t, func() bool { return c.count("link") == 3 })
	if got := link(2); got != "map[mode:view reset:true]" {
		t.Fatalf("link = %s", got)
	}
	s.Send(down)
	s.Send(down)
	s.Send(ctrlD)
	pollContains(t, s.Fixture, "Remove Alice?")
	typed(s.Fixture, "y")
	s.waitCalls(t, "removeGuest", 1)
	if got := c.get("removeGuest"); len(got) != 1 || fmtJSON(got[0].(*delta.JsonObject)) != "map[guestId:g1]" {
		t.Fatalf("removeGuest = %v", got)
	}
	// A declined copy says nothing; a failed one points at the launcher's own message.
	g.answer(s, tuiruntime.GrantResult{Error: "failed"})
	s.Send(up)
	s.Send(up)
	s.Send(enter)
	tuitest.Poll(t, noticeContaining(s.Fixture, "launcher could not"))
}

func TestAViewOnlyGuestSeesUnderWhichNameItJoinedInsteadOfTheEditor(t *testing.T) {
	s := newP11(t, p11Options{viewer: true, role: "guest", facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet}})
	s.Do(func() {
		s.Runtime.Grant = &tuiruntime.Guest{Name: "Alice\x1b[31m", Sessions: map[string]string{}}
		s.Runtime.Changed()
	})
	pollScreen(t, s.Fixture, func(screen string) bool {
		return strings.Contains(screen, "Joined as Alice[31m (unverified) · read-only") || strings.Contains(screen, "Joined as Alice (unverified) · read-only")
	})
	if strings.Contains(s.Screen(), "\x1b[31m") {
		t.Fatal("an escape sequence of the name reached the screen")
	}
}

const fileID = "0b3f2c1e-1111-4222-8333-444455556666"

func resultEntry(id int, toolCallID, toolName string, details any, text string) durable.EntryRecord {
	return tuitest.Entry(id, "pi.tool-result", ai.ToolResultMessage{
		ToolCallID: toolCallID, ToolName: toolName, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: details, Timestamp: int64(id),
	})
}

func callEntry(id int, toolCallID, name string, args ai.JsonObject) durable.EntryRecord {
	return tuitest.Entry(id, "pi.assistant", tuitest.Assistant(ai.StopReasonToolUse, int64(id), ai.ToolCall{ID: toolCallID, Name: name, Arguments: args}))
}

// contentStubs are the worker objects of the artifact and sent-file panels.
func contentStubs(c *calls) []p2test.Stub {
	return []p2test.Stub{
		{Contract: artifactscontract.ArtifactList, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"artifacts": []any{
				delta.JsonObjectOf("name", "notes", "title", "Notes", "type", "markdown", "version", float64(2)),
				delta.JsonObjectOf("name", "page", "title", "Page", "type", "html", "version", float64(1)),
			}}}
		}},
		{Contract: artifactscontract.ArtifactContent, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", Methods: map[string]p2test.StubMethod{
				"read": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					name, version := object.Value("name").(string), object.Value("version")
					c.record("readArtifact", [2]any{name, version})
					if name == "notes" {
						return delta.JsonObjectOf("info", delta.JsonObjectOf("name", name, "title", "Notes", "type", "markdown", "version", version),
							"content", "# Heading\n\nSome **bold** text\x1b]52;c;evil\x07", "truncated", false), nil
					}
					return delta.JsonObjectOf("info", delta.JsonObjectOf("name", name, "title", "Page", "type", "html", "version", version), "content", "<h1>Page</h1>", "truncated", true), nil
				},
			}}
		}},
		{Contract: sendfilecontract.SentFiles, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", Methods: map[string]p2test.StubMethod{
				"preview": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					c.record("preview", args.(*delta.JsonObject).Value("id"))
					return delta.JsonObjectOf("name", "report.csv", "size", float64(12), "text", "a,b\n1,2", "truncated", false), nil
				},
			}}
		}},
	}
}

func TestArtifactsTheCardPreviewsTextArtifactsShowsMarkdownInlineWhenNarrowOpensInTheBrowserAndDownloads(t *testing.T) {
	c := &calls{}
	var lines []string
	for i := 1; i <= 12; i++ {
		lines = append(lines, "line "+strconv.Itoa(i))
	}
	s := newP11(t, p11Options{calls: c, worker: contentStubs(c), facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, artifactstui.Facet}, views: func(int) delta.JsonValue {
		return viewWithAgent(t,
			callEntry(1, "a1", "artifact", ai.JsonObject{"name": "notes", "title": "Notes", "type": "markdown", "content": strings.Join(lines, "\n")}),
			resultEntry(2, "a1", "artifact", map[string]any{"name": "notes", "title": "Notes", "type": "markdown", "version": 2}, "Saved"))
	}})
	g := &grants{}
	g.answer(s, tuiruntime.GrantResult{OK: true, Value: map[string]any{"saved": true}})
	pollContains(t, s.Fixture, "Notes · MARKDOWN · version 2 · /artifacts opens")
	mustMatch(t, `line 8\s*\n\s*\.\.\. \(4 more lines`, s.Screen())
	if strings.Contains(s.Screen(), "line 9") {
		t.Fatal("the card shows more than its preview")
	}
	s.run("p2.artifacts")
	pollContains(t, s.Fixture, "Artifacts (2)")
	mustMatch(t, `the browser needs its own sign-in`, s.Screen())
	// Too narrow for the side panel: Enter shows an artifact over the chat.
	s.Terminal.Resize(80, 24)
	s.Send(enter)
	pollScreen(t, s.Fixture, func(screen string) bool { return strings.Contains(screen, "Notes · MARKDOWN · version 2") && strings.Contains(screen, "Heading") })
	shown := s.Screen()
	for _, absent := range []string{"# Heading", "**bold**", "52;c;evil"} {
		if strings.Contains(shown, absent) {
			t.Fatalf("%q is on the screen:\n%s", absent, shown)
		}
	}
	if got := c.get("readArtifact"); len(got) != 1 || got[0] != [2]any{"notes", float64(2)} {
		t.Fatalf("readArtifact = %v", got)
	}
	s.Send(esc)
	pollContains(t, s.Fixture, "Artifacts (2)")
	s.Send(down)
	s.Send(enter)
	pollContains(t, s.Fixture, "Source only")
	mustMatch(t, `<h1>Page</h1>.*truncated`, s.Screen())
	s.Send(esc)
	pollContains(t, s.Fixture, "Artifacts (2)")
	s.Send("\x02") // Ctrl+B
	tuitest.Poll(t, func() bool { return g.count() == 1 })
	if got := g.at(0); got[0] != "openUrl" || fmtJSON(got[1]) != "map[kind:origin path:/artifact?session=s&name=page&version=1]" {
		t.Fatalf("grant = %v", got)
	}
	s.Send("\x13") // Ctrl+S
	tuitest.Poll(t, func() bool { return g.count() == 2 })
	if got := g.at(1); got[0] != "download" || fmtJSON(got[1]) != "map[kind:artifact name:page session:s version:1]" {
		t.Fatalf("grant = %v", got)
	}
	tuitest.Poll(t, noticeText(s.Fixture, "Downloaded Page"))
}

func TestArtifactsAnInlineCallRendersAllOfItsMarkdownInTheTranscriptInlineSVGKeepsTheCardLine(t *testing.T) {
	c := &calls{}
	markdown := []string{"# Inline heading", "", "Some **bold** text"}
	for i := 1; i <= 10; i++ {
		markdown = append(markdown, "- item "+strconv.Itoa(i))
	}
	svg := "<svg xmlns='http://www.w3.org/2000/svg'><text>svg-source</text></svg>"
	s := newP11(t, p11Options{calls: c, worker: contentStubs(c), facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, artifactstui.Facet}, views: func(int) delta.JsonValue {
		return viewWithAgent(t,
			callEntry(1, "a1", "artifact", ai.JsonObject{"name": "notes", "title": "Notes", "type": "markdown", "content": strings.Join(markdown, "\n"), "inline": true}),
			resultEntry(2, "a1", "artifact", map[string]any{"name": "notes", "title": "Notes", "type": "markdown", "version": 3, "inline": true}, "Saved"),
			callEntry(3, "a2", "artifact", ai.JsonObject{"name": "flow", "title": "Flow", "type": "svg", "content": svg, "inline": true}),
			resultEntry(4, "a2", "artifact", map[string]any{"name": "flow", "title": "Flow", "type": "svg", "version": 1, "inline": true}, "Saved"))
	}})
	s.Terminal.Resize(100, 50)
	pollScreen(t, s.Fixture, func(screen string) bool {
		return strings.Contains(screen, "Flow · SVG · version 1 · /artifacts opens") && strings.Contains(screen, "Notes · MARKDOWN")
	})
	shown := s.Screen()
	mustMatch(t, `Notes · MARKDOWN · version 3`, shown)
	if !strings.Contains(shown, "Inline heading") || strings.Contains(shown, "# Inline heading") {
		t.Fatal("Markdown is not rendered")
	}
	if !strings.Contains(shown, "bold") || strings.Contains(shown, "**bold**") {
		t.Fatal("the emphasis is not rendered")
	}
	if !strings.Contains(shown, "item 10") || strings.Contains(shown, "more lines") {
		t.Fatal("not all of it, the collapsed preview")
	}
	if strings.Contains(shown, "svg-source") {
		t.Fatal("HTML and SVG run only in a browser")
	}
	if c.count("readArtifact") != 0 {
		t.Fatal("the call's own content, no read")
	}
}

func TestArtifactsEnterShowsOneInTheSidePanelWhichFollowsNewVersionsScrollsWhenFocusedHidesWhenNarrow(t *testing.T) {
	c := &calls{}
	var rows []string
	for i := 1; i <= 60; i++ {
		rows = append(rows, "row "+strconv.Itoa(i))
	}
	var list *p2test.Cell
	version := func(n float64) *delta.JsonObject {
		return delta.JsonObjectOf("name", "notes", "title", "Notes", "type", "markdown", "version", n)
	}
	worker := []p2test.Stub{
		{Contract: artifactscontract.ArtifactList, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, self p2test.StubSelf) p2test.StubMembers {
			// One entry per version, as the worker lists them.
			list = self.Cell([]any{version(1), version(2)})
			return p2test.StubMembers{Variant: "reader", State: map[string]any{"artifacts": list}}
		}},
		{Contract: artifactscontract.ArtifactContent, Factory: func(_ *delta.JsonObject, _ p2runtime.Principal, _ p2test.StubSelf) p2test.StubMembers {
			return p2test.StubMembers{Variant: "reader", Methods: map[string]p2test.StubMethod{
				"read": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					object := args.(*delta.JsonObject)
					number := object.Value("version")
					c.record("readArtifact", [2]any{"notes", number})
					heading := "Version three"
					if number == float64(2) {
						heading = "Version two"
					}
					return delta.JsonObjectOf("info", delta.JsonObjectOf("name", "notes", "title", "Notes", "type", "markdown", "version", number),
						"content", "# "+heading+"\n\n"+strings.Join(rows, "\n\n"), "truncated", false), nil
				},
			}}
		}},
	}
	s := newP11(t, p11Options{calls: c, worker: worker, facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, artifactstui.Facet}})
	s.run("p2.artifacts")
	pollContains(t, s.Fixture, "Artifacts (2)")
	s.Send(down) // the second row: version 2 of the same artifact
	s.Send(enter)
	pollScreen(t, s.Fixture, func(screen string) bool { return strings.Contains(screen, "Notes · MARKDOWN · v2") && strings.Contains(screen, "Version two") })
	state := func() (current string, focused, contextPanel, contextFocused, captures bool) {
		s.Do(func() {
			current, focused = s.Runtime.Ctx.Panel().Current(), s.Runtime.Ctx.Panel().Focused()
			contexts := s.Runtime.Contexts()
			contextPanel, contextFocused, captures = contexts["panel"], contexts["panelFocused"], s.Runtime.CapturesViewportInput()
		})
		return
	}
	current, focused, contextPanel, _, captures := state()
	if current != "artifact:notes" || !contextPanel {
		t.Fatalf("panel = %q, panel context %v", current, contextPanel)
	}
	// Beside the chat, behind its bar; the editor keeps the focus.
	mustMatch(t, `│ +Version two`, s.Screen())
	if focused || captures {
		t.Fatal("the panel took the focus")
	}
	s.Send(f2)
	_, focused, _, contextFocused, captures := state()
	if !focused || !captures || !contextFocused {
		t.Fatalf("F2 did not focus the panel: %v %v %v", focused, captures, contextFocused)
	}
	s.Send("x") // typing stays out of the editor
	s.Send(pageDown)
	s.Send(pageDown)
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Version two") })
	s.Send(esc)
	current, focused, _, _, _ = state()
	if focused || current != "artifact:notes" {
		t.Fatalf("Escape only gives the focus back: %q %v", current, focused)
	}
	// A new version replaces the shown one.
	_ = list.Change(func(current any) any { return append(append([]any{}, current.([]any)...), version(3)) })
	pollContains(t, s.Fixture, "Notes · MARKDOWN · v3")
	if got := c.get("readArtifact"); len(got) != 2 || got[0] != [2]any{"notes", float64(2)} || got[1] != [2]any{"notes", float64(3)} {
		t.Fatalf("readArtifact = %v", got)
	}
	// Too narrow for a split: hidden, and it cannot take the focus.
	s.Terminal.Resize(80, 24)
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Notes · MARKDOWN · v3") })
	if _, _, contextPanel, _, _ := state(); contextPanel {
		t.Fatal("a hidden panel is a context")
	}
	s.Send(f2)
	if _, focused, _, _, _ := state(); focused {
		t.Fatal("a hidden panel took the focus")
	}
	s.Terminal.Resize(100, 24)
	pollContains(t, s.Fixture, "Notes · MARKDOWN · v3")
	s.run("p2.panel.close")
	pollScreen(t, s.Fixture, func(screen string) bool { return !strings.Contains(screen, "Notes · MARKDOWN · v3") })
	if current, _, _, _, _ := state(); current != "" {
		t.Fatalf("the panel is still %q", current)
	}
}

func TestSentFilesTheCardDownloadsPreviewsTextSavesAndOpensThroughTheLauncher(t *testing.T) {
	c := &calls{}
	s := newP11(t, p11Options{calls: c, worker: contentStubs(c), facets: []host.Facet{tui.CoreFacet, chattui.NewFacet(), statustui.Facet, sendfiletui.Facet}, views: func(int) delta.JsonValue {
		return viewWithAgent(t,
			callEntry(1, "f1", "send_file", ai.JsonObject{"path": "out/report.csv"}),
			resultEntry(2, "f1", "send_file", map[string]any{"id": fileID, "name": "report.csv", "size": 12}, "Download ready"),
			callEntry(3, "f2", "send_file", ai.JsonObject{"path": "evil"}),
			resultEntry(4, "f2", "send_file", map[string]any{"id": "../../etc", "name": "passwd", "size": 1}, "Download ready"))
	}})
	g := &grants{}
	g.answer(s, tuiruntime.GrantResult{OK: true, Value: map[string]any{"saved": true}})
	pollContains(t, s.Fixture, "report.csv · 12B · ready to download · /downloads saves")
	s.run("p2.downloads")
	pollContains(t, s.Fixture, "Sent files (1)")
	if strings.Contains(s.Screen(), "passwd") {
		t.Fatal("an invalid snapshot ID is offered")
	}
	s.Send(enter)
	pollScreen(t, s.Fixture, func(screen string) bool { return strings.Contains(screen, "report.csv · 12B") && strings.Contains(screen, "1,2") })
	if got := c.get("preview"); len(got) != 1 || got[0] != fileID {
		t.Fatalf("preview = %v", got)
	}
	s.Send(esc)
	pollContains(t, s.Fixture, "Sent files (1)")
	s.Send("\x13") // Ctrl+S
	tuitest.Poll(t, func() bool { return g.count() == 1 })
	if got := g.at(0); got[0] != "download" || fmtJSON(got[1]) != "map[id:"+fileID+" kind:file name:report.csv session:s]" {
		t.Fatalf("grant = %v", got)
	}
	s.Send("\x02") // Ctrl+B
	tuitest.Poll(t, func() bool { return g.count() == 2 })
	if got := g.at(1); got[0] != "openUrl" || fmtJSON(got[1]) != "map[kind:origin path:/download?session=s&id="+fileID+"]" {
		t.Fatalf("grant = %v", got)
	}
}
