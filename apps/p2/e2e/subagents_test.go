// Ports test/p2/subagents.test.ts, subagent-cwd.test.ts and subagent-team.test.ts (Pi main c23b9ec1) under the fake
// provider. Not ported here: the workspace panel and upload of a subagent's own cwd (the workspace plugin's lane) and
// the MCP tool of a subagent's script (mcp-worker.test.ts's case, once the MCP lane lands).

package e2e_test

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	sharingcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

func (f *agentFixture) children() *host.Reference {
	f.t.Helper()
	ref, err := p2test.HoldIn(f.Owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

func (f *agentFixture) statuses() *host.Reference {
	f.t.Helper()
	ref, err := p2test.HoldIn(f.Owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		f.t.Fatal(err)
	}
	return ref
}

// childrenOf are the session's listed children.
func (f *agentFixture) childrenOf(view delta.JsonValue) []any { return list(object(view).Value(f.Session)) }

func (f *agentFixture) childNamed(view delta.JsonValue, name string) *delta.JsonObject {
	for _, each := range f.childrenOf(view) {
		if object(each).Value("name") == name {
			return object(each)
		}
	}
	return nil
}

func (f *agentFixture) statusOf(view delta.JsonValue) *delta.JsonObject {
	return object(object(view).Value(f.Session))
}

// quiet waits until the session has no live task.
func (f *agentFixture) quiet(statuses *host.Reference) {
	f.t.Helper()
	f.until(statuses.Member("statuses"), func(v delta.JsonValue) bool {
		s := f.statusOf(v)
		return s != nil && s.Value("tasks") == float64(0)
	}, 30*time.Second)
}

func (f *agentFixture) busyIn(view delta.JsonValue, conversation any) bool {
	s := f.statusOf(view)
	if s == nil {
		return false
	}
	for _, each := range list(s.Value("busy")) {
		if each == conversation {
			return true
		}
	}
	return false
}

func hasSubstring(substring string) func(string) bool {
	return func(text string) bool { return strings.Contains(text, substring) }
}

func TestSubagentsFakeProvider(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{Idle: 1500 * time.Millisecond, AllowedHosts: []string{"share.test"}})
	children := f.children()
	statuses := f.statuses()
	chatView := f.Chat.Member("view")
	var child *delta.JsonObject

	t.Run("rejects invalid models without creating a child", func(t *testing.T) {
		f.send("subagent-bad")
		f.until(chatView, func(v delta.JsonValue) bool { return hasText(v, hasSubstring("Unknown model nope/missing")) })
		listed := f.until(children.Member("children"), func(v delta.JsonValue) bool { return v != nil })
		if len(f.childrenOf(listed)) != 0 {
			t.Fatal("a child was created")
		}
	})

	t.Run("spawns a configured child and reports its answer once", func(t *testing.T) {
		f.send("subagent")
		view := f.until(chatView, func(v delta.JsonValue) bool {
			return hasText(v, func(s string) bool { return strings.HasPrefix(s, "[answered, no reply needed]") && strings.Contains(s, "Echo: hello helper") })
		})
		spawned := subagentResult(view, "spawn")
		if spawned == nil || spawned.Value("isError") != false {
			t.Fatalf("spawn = %v", spawned)
		}
		details := object(spawned.Value("details"))
		if details.Value("name") != "helper" || details.Value("model") != "faux/faux-1" || details.Value("thinkingLevel") != "off" || marshal(t, details.Value("tools")) != `["read"]` {
			t.Errorf("details = %s", marshal(t, details))
		}
		listed := f.until(children.Member("children"), func(v delta.JsonValue) bool { return len(f.childrenOf(v)) == 1 })
		child = object(f.childrenOf(listed)[0])
		if child.Value("name") != "helper" || child.Value("conversationId").(float64) <= 1 {
			t.Fatalf("child = %v", child)
		}
		f.send("subagent-answer")
		answered := f.until(chatView, func(v delta.JsonValue) bool { return subagentResult(v, "answer") != nil })
		if resultText(subagentResult(answered, "answer")) != "Echo: hello helper" {
			t.Errorf("answer = %q", resultText(subagentResult(answered, "answer")))
		}
		reports := 0
		for _, text := range textsOf(answered) {
			if strings.HasPrefix(text, "[answered, no reply needed]") {
				reports++
			}
		}
		if reports != 1 {
			t.Errorf("reports = %d", reports)
		}
	})

	t.Run("configures tools and opens a child's independent transcript", func(t *testing.T) {
		f.send("subagent-configure")
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && marshal(t, c.Value("tools")) == `["bash","artifact"]`
		})
		sub := f.hold(child.Value("conversationId").(float64))
		view := f.until(sub.Member("view"), func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Echo: hello helper" }) })
		// The sidebar lists the chosen tools; the agent also has `team` (`scope: "subagent"`), offered from the start.
		agent := object(object(object(view).Value("docs")).Value("pi.agent"))
		if marshal(t, agent.Value("tools")) != `["bash","artifact","team"]` || marshal(t, object(agent.Value("modelTools")).Value("add")) != `["team"]` {
			t.Errorf("agent = %s", marshal(t, agent))
		}
		if _, err := sub.Call("configure", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"), "thinkingLevel", "off"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("thinkingLevel") == "off"
		})
		if _, err := sub.Call("send", delta.JsonObjectOf("text", "direct child message", "requestId", "direct"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(sub.Member("view"), func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Echo: direct child message" }) })
		root := f.until(chatView, func(v delta.JsonValue) bool { return !running(v) })
		if hasText(root, func(s string) bool { return s == "Echo: direct child message" }) {
			t.Error("direct child prompts do not auto-report")
		}
		sub.Release()
		missing, err := f.Owner.Hold(p2test.Background, chatcontract.Chat, delta.JsonObjectOf("session", f.Session, "conversation", float64(999)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// A failed open leaves its holders waiting (the conversation may appear): the failure names the cause.
		failure, err := p2test.FailureOf(missing)
		if err != nil || failure == nil || !strings.Contains(failure.Error(), "Unknown conversation") {
			t.Errorf("open failure = %v, %v", failure, err)
		}
		missing.Release()
	})

	t.Run("archives through the tool (hidden from status) and restores from the browser", func(t *testing.T) {
		conversation := child.Value("conversationId")
		f.send("subagent-archive")
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("archived") == true
		})
		f.send("subagent-status")
		view := f.until(chatView, func(v delta.JsonValue) bool { return subagentResult(v, "status") != nil })
		if got := resultText(subagentResult(view, "status")); got != "1 archived (status with a name shows one)." {
			t.Errorf("status = %q", got)
		}
		control, err := p2test.HoldIn(f.Owner, subagentscontract.SubagentControl, delta.JsonObjectOf("session", f.Session), "participant")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := control.Call("archive", delta.JsonObjectOf("conversation", conversation, "archived", false), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("archived") == nil
		})
		_, err = control.Call("archive", delta.JsonObjectOf("conversation", float64(999), "archived", true), p2test.Background)
		if err == nil || !strings.Contains(err.Error(), "Unknown conversation") {
			t.Errorf("archive of an unknown conversation: %v", err)
		}
		// Archiving from the browser stops the child's work first.
		sub := f.hold(conversation.(float64))
		if _, err := sub.Call("send", delta.JsonObjectOf("text", "slow archived", "requestId", "slow-archived"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(statuses.Member("statuses"), func(v delta.JsonValue) bool { return f.busyIn(v, conversation) })
		if _, err := control.Call("archive", delta.JsonObjectOf("conversation", conversation, "archived", true), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(statuses.Member("statuses"), func(v delta.JsonValue) bool { return !f.busyIn(v, conversation) })
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("archived") == true
		})
		// Sending to it again brings it back.
		f.send("subagent-send")
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("archived") == nil
		})
		f.until(sub.Member("view"), func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Echo: hello again" }) })
		sub.Release()
		control.Release()
	})

	t.Run("keeps child work alive through main Stop and compatible reload", func(t *testing.T) {
		conversation := child.Value("conversationId")
		sub := f.hold(conversation.(float64))
		if _, err := sub.Call("send", delta.JsonObjectOf("text", "slow child", "requestId", "slow"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(statuses.Member("statuses"), func(v delta.JsonValue) bool { return f.busyIn(v, conversation) })
		if _, err := f.Chat.Call("stop", nil, p2test.Background); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Fixture.Server.Reload([]string{"subagents"}, p2test.Background); err != nil {
			t.Fatal(err)
		}
		f.until(sub.Member("view"), func(v delta.JsonValue) bool { return hasText(v, hasPrefixOf("Echo: slow child")) }, 30*time.Second)
		sub.Release()
	})

	t.Run("retains sidebar children without keeping a worker alive, then reopens Durable history", func(t *testing.T) {
		f.Chat.Release()
		// The released Chats linger 30 s on the worker, which keeps it alive (Chord's activity counts lingering objects).
		f.until(statuses.Member("statuses"), func(v delta.JsonValue) bool {
			s := f.statusOf(v)
			return s != nil && s.Value("running") == false
		}, 60*time.Second)
		listed := f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "helper")
			return c != nil && c.Value("lastEntryId") != nil
		})
		if f.childNamed(listed, "helper") == nil {
			t.Fatal("no helper")
		}
		f.Chat = f.hold(1)
		chatView = f.Chat.Member("view")
		f.until(chatView, func(v delta.JsonValue) bool { return hasText(v, hasPrefixOf("[answered, no reply needed]")) })
		f.send("subagent-answer")
		f.until(chatView, func(v delta.JsonValue) bool { return hasText(v, hasPrefixOf("Echo: slow child")) })
		f.quiet(statuses)
	})

	t.Run("limits guest sidebar children to their session and disallows reporting", func(t *testing.T) {
		sharing, err := p2test.HoldIn(f.Owner, sharingcontract.Sharing, delta.JsonObjectOf("session", f.Session), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sharing.Call("link", delta.JsonObjectOf("mode", "view"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		links := f.until(sharing.Member("links"), func(v delta.JsonValue) bool { return len(list(v)) > 0 })
		token := object(list(links)[0]).Value("token").(string)
		headers := map[string]string{"Host": "share.test", "Origin": "https://share.test", "Content-Type": "application/json"}
		joined, err := p2test.HTTP(f.Fixture.Server.Port, "/join", p2test.HTTPOptions{Method: http.MethodPost, Headers: headers, Body: strings.NewReader(`{"token":"` + token + `","name":"Reader"}`)})
		if err != nil || joined.Status != 200 {
			t.Fatalf("join: %v %+v", err, joined)
		}
		cookie, _, _ := strings.Cut(joined.Headers.Get("Set-Cookie"), ";")
		guest, conn := f.Fixture.Client(map[string]string{"Host": "share.test", "Origin": "https://share.test", "Cookie": cookie})
		defer conn.Close()
		list0, err := p2test.HoldIn(guest, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		value := f.until(list0.Member("children"), func(v delta.JsonValue) bool { return len(f.childrenOf(v)) > 0 })
		if keys := object(value).Keys(); len(keys) != 1 || keys[0] != f.Session {
			t.Errorf("guest sees sessions %v", keys)
		}
		// `report` is a worker's: a reader calling it anyway is refused.
		_, err = list0.Call("report", delta.JsonObjectOf("children", []any{}), p2test.Background)
		p2test.ExpectChord(t, err, data.CodeInvalid)
		sub, err := p2test.HoldIn(guest, chatcontract.Chat, delta.JsonObjectOf("session", f.Session, "conversation", child.Value("conversationId")), "viewer")
		if err != nil {
			t.Fatal(err)
		}
		// Read-only guests cannot archive (read/write guests can, as the owner).
		_, err = p2test.HoldIn(guest, subagentscontract.SubagentControl, delta.JsonObjectOf("session", f.Session), "participant")
		p2test.ExpectChord(t, err, data.CodeForbidden)
		f.until(sub.Member("view"), func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Echo: hello helper" }) })
		sub.Release()
		list0.Release()
		sharing.Release()
	})

	t.Run("loads persisted sidebar children after server restart without starting a worker", func(t *testing.T) {
		port, dataDir := f.Fixture.Server.Port, f.Fixture.DataDir
		if err := f.Fixture.Server.Close(p2test.Background); err != nil {
			t.Fatal(err)
		}
		entry, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fake := true
		restarted, err := hosts.StartServer(hosts.ServerOptions{DataDir: dataDir, Port: &port, Fake: &fake, Entry: entry, ExecArgv: []string{hosts.WorkerArg}})
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close(p2test.Background)
		conn, err := p2test.Dial(restarted.Port, nil)
		if err != nil {
			t.Fatal(err)
		}
		client, err := p2test.ClientHost(conn)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close(p2test.Background)
		list0, err := p2test.HoldIn(client, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		value := f.until(list0.Member("children"), func(v delta.JsonValue) bool { return len(f.childrenOf(v)) > 0 })
		if f.childNamed(value, "helper") == nil {
			t.Error("helper is not listed")
		}
		status, err := p2test.HoldIn(client, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		if err != nil {
			t.Fatal(err)
		}
		statusValue := f.until(status.Member("statuses"), func(v delta.JsonValue) bool { return v != nil })
		if s := f.statusOf(statusValue); s != nil && s.Value("running") == true {
			t.Error("a worker runs")
		}
	})
}

func hasPrefixOf(prefix string) func(string) bool {
	return func(text string) bool { return strings.HasPrefix(text, prefix) }
}

// Pi Mobile 24e3fccdd: spawn takes a cwd (e.g. a git worktree the main agent created first).
func TestSubagentSpawnedWithItsOwnCwd(t *testing.T) {
	f := &agentFixture{t: t}
	p2test.NoLeakedGoroutines(t)
	f.Fixture = p2test.StartFixture(t, hosts.ServerOptions{})
	project, err := filepath.EvalSymlinks(f.Fixture.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	f.Fixture.Workspace = project
	if err := os.Mkdir(filepath.Join(project, "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "worktree", "marker.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Owner = f.Fixture.Browser()
	sessions, err := p2test.HoldIn(f.Owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", project), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	f.Session = object(created).Value("id").(string)
	f.Chat = f.hold(1)
	children := f.children()
	view := f.Chat.Member("view")
	spawn := func(args map[string]any, requestId string) {
		f.t.Helper()
		if _, err := f.Chat.Call("send", delta.JsonObjectOf("text", "subagent "+marshal(t, args), "requestId", requestId), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	// A missing directory fails the spawn and creates nothing; only spawn takes a cwd.
	t.Cleanup(func() { dumpLogs(t) })
	t0 := time.Now()
	spawn(map[string]any{"action": "spawn", "name": "bad", "message": "x", "cwd": "missing"}, "missing")
	f.until(view, func(v delta.JsonValue) bool { return hasText(v, hasSubstring("cwd missing is not an existing directory")) })
	t.Log("first", time.Since(t0))
	spawn(map[string]any{"action": "spawn", "name": "file", "message": "x", "cwd": "worktree/marker.txt"}, "file")
	f.until(view, func(v delta.JsonValue) bool {
		return hasText(v, hasSubstring("cwd worktree/marker.txt is not an existing directory"))
	})
	if listed := f.until(children.Member("children"), func(v delta.JsonValue) bool { return v != nil }); len(f.childrenOf(listed)) != 0 {
		t.Fatal("a child was created")
	}

	// A relative cwd resolves against the caller's.
	spawn(map[string]any{"action": "spawn", "name": "wt", "message": "bash", "cwd": "worktree", "tools": []string{"bash"}}, "spawn")
	spawned := f.until(view, func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == `<sender kind="subagent" name="wt"/>` }) }, 20*time.Second)
	worktree := filepath.Join(project, "worktree")
	if got := object(subagentResult(spawned, "spawn").Value("details")).Value("cwd"); got != worktree {
		t.Errorf("spawn cwd = %v", got)
	}
	listed := f.until(children.Member("children"), func(v delta.JsonValue) bool { return len(f.childrenOf(v)) > 0 })
	agent := object(f.childrenOf(listed)[0])
	if marshal(t, agent.Value("location")) != marshal(t, map[string]any{"cwd": worktree, "env": nil}) {
		t.Errorf("the sidebar listing carries the subagent's own location: %s", marshal(t, agent.Value("location")))
	}
	// Only spawn and configure take a location; configure places it again (here where it is).
	spawn(map[string]any{"action": "send", "name": "wt", "message": "x", "cwd": "worktree"}, "send-cwd")
	f.until(view, func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Only spawn and configure take a location." }) })
	spawn(map[string]any{"action": "configure", "name": "wt", "tools": []string{"bash"}, "cwd": "worktree"}, "configure")
	f.until(view, func(v delta.JsonValue) bool { return hasText(v, func(s string) bool { return s == "Configured wt." }) })
	spawn(map[string]any{"action": "status"}, "status")
	status := f.until(view, func(v delta.JsonValue) bool { return subagentResult(v, "status") != nil })
	if !regexp.MustCompile(regexp.QuoteMeta("· in " + worktree) + `$`).MatchString(resultText(subagentResult(status, "status"))) {
		t.Errorf("status = %q", resultText(subagentResult(status, "status")))
	}
	// Its bash ran in the worktree.
	sub := f.hold(agent.Value("conversationId").(float64))
	childView := f.until(sub.Member("view"), func(v delta.JsonValue) bool { return len(messageToolResults(v, "bash")) > 0 })
	if got := resultText(messageToolResults(childView, "bash")[0]); !regexp.MustCompile(`/worktree\s*$`).MatchString(got) {
		t.Errorf("pwd = %q", got)
	}
	sub.Release()
}

// A subagent left with a stale cwd by an earlier move works in the session's cwd again (Pi Mobile 78056b34e): the worker
// reconciles subagents without their own cwd at every start.
func TestSubagentWithAStaleCwdWorksInTheSessionsCwdAgain(t *testing.T) {
	t.Skip("needs the durable storage of a stopped session to be reconfigured from the test: see the sessions lane's storage helper")
}

func TestSubagentTeamMessaging(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	agents := f.children()
	statuses := f.statuses()
	view := f.Chat.Member("view")
	agentMessages := func(v delta.JsonValue) []*delta.JsonObject {
		var out []*delta.JsonObject
		for _, entry := range list(object(v).Value("entries")) {
			if object(entry).Value("kind") == "p2.agent-message" {
				out = append(out, object(entry))
			}
		}
		return out
	}
	withIntent := func(v delta.JsonValue, intent string) []*delta.JsonObject {
		var out []*delta.JsonObject
		for _, entry := range agentMessages(v) {
			if object(entry.Value("data")).Value("intent") == intent {
				out = append(out, entry)
			}
		}
		return out
	}
	modelOf := func(entry *delta.JsonObject) string {
		models := list(entry.Value("model"))
		if len(models) == 0 {
			return ""
		}
		return marshal(t, object(models[0]).Value("content"))
	}
	fromName := func(entry *delta.JsonObject) any {
		return object(object(entry.Value("data")).Value("from")).Value("name")
	}
	spawn := func(name, message string) {
		t.Helper()
		f.send("subagent " + marshal(t, map[string]any{"action": "spawn", "name": name, "message": message, "tools": []string{}}))
		f.quiet(statuses)
	}
	spawn("first", "hello first")
	question := map[string]any{"action": "send", "to": "parent", "message": "Which API should I use?", "intent": "question"}
	spawn("asker", "team "+marshal(t, question))
	asked := withIntent(f.until(view, func(v delta.JsonValue) bool { return len(withIntent(v, "question")) > 0 }), "question")[0]
	data0 := object(asked.Value("data"))
	if fromName(asked) != "asker" || data0.Value("text") != "Which API should I use?" {
		t.Errorf("asked = %s", marshal(t, data0))
	}
	if m := modelOf(asked); !strings.Contains(m, `<sender kind=\"subagent\" name=\"asker\"/>`) || !strings.Contains(m, `[asks; answer with subagent send to asker] Which API`) {
		t.Errorf("model = %s", m)
	}
	// The owner started the turn that spawned it: what it writes records that, so the main agent's turn on it may act for the owner.
	if data0.Value("origin") != "owner" {
		t.Errorf("origin = %v", data0.Value("origin"))
	}
	f.until(view, func(v delta.JsonValue) bool {
		return strings.Contains(marshal(t, v), "Echo: [asks; answer with subagent send to asker]")
	})
	f.quiet(statuses)
	reports := withIntent(view0(f, view), "answered")
	var names []string
	for _, r := range reports {
		names = append(names, fromName(r).(string))
		if object(r.Value("data")).Value("origin") != "owner" {
			t.Errorf("report origin = %v", object(r.Value("data")).Value("origin"))
		}
	}
	if len(names) != 2 || !(names[0] == "asker" && names[1] == "first" || names[0] == "first" && names[1] == "asker") {
		t.Fatalf("reports = %v", names)
	}
	if !strings.Contains(modelOf(reports[0]), "[answered, no reply needed]") {
		t.Errorf("report model = %s", modelOf(reports[0]))
	}

	// A note to a sibling arrives in its conversation; reading a sibling's answer.
	listed := f.until(agents.Member("children"), func(v delta.JsonValue) bool { return len(f.childrenOf(v)) == 2 })
	first, asker := f.childNamed(listed, "first"), f.childNamed(listed, "asker")
	if first == nil || asker == nil {
		t.Fatal("children missing")
	}
	note := map[string]any{"action": "send", "to": "first", "message": "FYI: done with step 1", "intent": "note"}
	f.send("subagent " + marshal(t, map[string]any{"action": "send", "name": "asker", "message": "team " + marshal(t, note)}))
	sibling := f.hold(first.Value("conversationId").(float64))
	noted := f.until(sibling.Member("view"), func(v delta.JsonValue) bool { return len(withIntent(v, "note")) > 0 })
	received := withIntent(noted, "note")[0]
	if fromName(received) != "asker" || !strings.Contains(modelOf(received), "[notes, no reply needed] FYI") || object(received.Value("data")).Value("origin") != "owner" {
		t.Errorf("received = %s", marshal(t, received))
	}
	f.quiet(statuses)
	f.send("subagent " + marshal(t, map[string]any{"action": "send", "name": "asker", "message": `team {"action":"answer","name":"first"}`}))
	askerChat := f.hold(asker.Value("conversationId").(float64))
	answered := f.until(askerChat.Member("view"), func(v delta.JsonValue) bool { return len(messageToolResults(v, "team")) >= 3 })
	teamResults := messageToolResults(answered, "team")
	if !strings.Contains(resultText(teamResults[len(teamResults)-1]), "Echo: ") {
		t.Errorf("the sibling's latest answer: %q", resultText(teamResults[len(teamResults)-1]))
	}
	f.quiet(statuses)

	// The main agent is not offered the tool.
	f.send(`team {"action":"list"}`)
	refused := f.until(view, func(v delta.JsonValue) bool { return len(messageToolResults(v, "team")) > 0 })
	if messageToolResults(refused, "team")[0].Value("isError") != true {
		t.Error("the main agent was offered the team tool")
	}
	sibling.Release()
	askerChat.Release()
}

func view0(f *agentFixture, view p2test.JSONSource) delta.JsonValue {
	value, err := p2test.Until(view, func(v delta.JsonValue) bool { return v != nil }, 0)
	if err != nil {
		f.t.Fatal(err)
	}
	return value
}

func TestSubagentsWithCodeMode(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	if err := os.WriteFile(filepath.Join(f.Fixture.Workspace, "marker.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	children := f.children()
	view := f.Chat.Member("view")
	sent := 0
	// subagent sends `subagent <args>` and waits for that call's result.
	subagent := func(args map[string]any) *delta.JsonObject {
		t.Helper()
		count := len(messageToolResults(view0(f, view), "subagent"))
		sent++
		if _, err := f.Chat.Call("send", delta.JsonObjectOf("text", "subagent "+marshal(t, args), "requestId", "codemode-"+itoa(sent)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		done := f.until(view, func(v delta.JsonValue) bool { return len(messageToolResults(v, "subagent")) > count })
		results := messageToolResults(done, "subagent")
		return results[len(results)-1]
	}
	// reported waits until name's answer was reported and the main agent's turn on it ended, so it cannot race a later send.
	reported := func(name string) {
		t.Helper()
		f.until(view, func(v delta.JsonValue) bool {
			return hasText(v, func(s string) bool { return s == `<sender kind="subagent" name="`+name+`"/>` }) && !running(v)
		})
	}
	toolsOf := func(result *delta.JsonObject) []string {
		var out []string
		for _, each := range list(object(result.Value("details")).Value("tools")) {
			out = append(out, each.(string))
		}
		return out
	}
	contains := func(all []string, one string) bool {
		for _, each := range all {
			if each == one {
				return true
			}
		}
		return false
	}

	t.Run("validates tools against the caller's own, session-level ones excluded", func(t *testing.T) {
		bad := subagent(map[string]any{"action": "spawn", "name": "bad", "message": "x", "tools": []string{"codemode", "bogus"}})
		if bad.Value("isError") != true || !regexp.MustCompile(`^Unknown tools: bogus\. Choose from your own tools: .*\bcodemode\b`).MatchString(resultText(bad)) {
			t.Errorf("bad = %q", resultText(bad))
		}
		main := subagent(map[string]any{"action": "spawn", "name": "bad", "message": "x", "tools": []string{"subagent", "rename_session"}})
		if !strings.HasPrefix(resultText(main), "Unknown tools: subagent, rename_session. ") {
			t.Errorf("main = %q", resultText(main))
		}
		chosen := subagent(map[string]any{"action": "spawn", "name": "chosen", "message": "x", "tools": []string{"bash", "codemode"}})
		if strings.Join(toolsOf(chosen), ",") != "bash,codemode" {
			t.Errorf("chosen = %v", toolsOf(chosen))
		}
		reported("chosen")
	})

	t.Run("gives all the caller's tools by default; a script calls its tools and the tools behind code mode", func(t *testing.T) {
		// The subagent's script reads with a coding tool, then starts one-shot agents (`agent` is script-only), which may get
		// code mode too, but not the tools that work only in the main conversation.
		script := strings.Join([]string{
			`const read = await tools.read({ path: "marker.txt" });`,
			`const answer = await tools.agent({ prompt: 'json {"n":1}', schema: { type: "object" } });`,
			`const nested = await tools.agent({ prompt: 'json {"n":2}', schema: { type: "object" }, tools: ["codemode", "read"] });`,
			`let refused;`,
			`try { await tools.agent({ prompt: "x", schema: {}, tools: ["subagent"] }); } catch (error) { refused = String(error.message ?? error); }`,
			`return { read, answer, nested, refused };`,
		}, "\n")
		spawned := subagent(map[string]any{"action": "spawn", "name": "coder", "message": "codemode " + script})
		if spawned.Value("isError") != false {
			t.Fatal(resultText(spawned))
		}
		given := toolsOf(spawned)
		for _, each := range []string{"read", "bash", "codemode", "history_search", "location_move"} {
			if !contains(given, each) {
				t.Errorf("missing %s", each)
			}
		}
		for _, each := range []string{"subagent", "rename_session", "team", "agent"} {
			if contains(given, each) {
				t.Errorf("offered %s", each)
			}
		}
		listed := f.until(children.Member("children"), func(v delta.JsonValue) bool { return f.childNamed(v, "coder") != nil })
		coder := f.childNamed(listed, "coder")
		sub := f.hold(coder.Value("conversationId").(float64))
		childView := f.until(sub.Member("view"), func(v delta.JsonValue) bool { return len(messageToolResults(v, "codemode")) > 0 })
		ran := messageToolResults(childView, "codemode")[0]
		if ran.Value("isError") != false {
			t.Fatal(resultText(ran))
		}
		output := resultText(ran)
		if !strings.HasPrefix(output, "Script completed") {
			t.Fatalf("output = %q", output)
		}
		var value struct {
			Read    string         `json:"read"`
			Answer  map[string]any `json:"answer"`
			Nested  map[string]any `json:"nested"`
			Refused string         `json:"refused"`
		}
		decode(t, output[strings.Index(output, "{"):], &value)
		if !strings.Contains(value.Read, "inside") || value.Answer["n"] != float64(1) || value.Nested["n"] != float64(2) ||
			!strings.HasPrefix(value.Refused, "Unknown tools: subagent. Choose from your own tools: ") {
			t.Errorf("value = %+v", value)
		}
		// The script-only tools were enabled by name before the script started; the model is still offered its choice.
		agent := object(object(object(childView).Value("docs")).Value("pi.agent"))
		if !strings.Contains(marshal(t, agent.Value("tools")), `"agent"`) {
			t.Errorf("tools = %s", marshal(t, agent.Value("tools")))
		}
		reported("coder")
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			c := f.childNamed(v, "coder")
			return c != nil && strings.Contains(marshal(t, c.Value("tools")), `"codemode"`)
		})
		sub.Release()
	})

	t.Run("leaves code mode out while the codemode setting is off, and refuses choosing it", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(f.Fixture.DataDir, "settings.json"), []byte(`{"codemode":false}`), 0o600); err != nil {
			t.Fatal(err)
		}
		// The worker applies the settings file asynchronously: spawn (a new name each time) until it refuses.
		var refused *delta.JsonObject
		for attempt := 0; attempt < 20 && (refused == nil || refused.Value("isError") != true); attempt++ {
			if attempt > 0 {
				time.Sleep(200 * time.Millisecond)
			}
			name := "off-" + itoa(attempt)
			refused = subagent(map[string]any{"action": "spawn", "name": name, "message": "x", "tools": []string{"codemode"}})
			if refused.Value("isError") != true {
				reported(name)
			}
		}
		if refused.Value("isError") != true || !strings.HasPrefix(resultText(refused), "Unknown tools: codemode. Choose from your own tools: ") {
			t.Fatalf("refused = %q", resultText(refused))
		}
		given := toolsOf(subagent(map[string]any{"action": "spawn", "name": "plain", "message": "x"}))
		if !contains(given, "read") || contains(given, "codemode") {
			t.Errorf("given = %v", given)
		}
	})
}
