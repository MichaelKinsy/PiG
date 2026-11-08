// Ports test/p2/locations.test.ts (Pi main c23b9ec1), its second case: "main agents list and move; subagents are placed, and
// move only with the tool; guests are refused". The first case (schemas and defaults) is plugins/locations/tool_test.go.

package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	locationscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/locations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sharing"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

// everyResult are the tool results of a conversation view (of the tool when named).
func everyResult(view delta.JsonValue, tool string) []*delta.JsonObject {
	var out []*delta.JsonObject
	for _, message := range messages(view) {
		if message.Value("role") == "toolResult" && (tool == "" || message.Value("toolName") == tool) {
			out = append(out, message)
		}
	}
	return out
}

func TestLocationToolsMainAgentsListAndMoveSubagentsArePlacedAndMoveOnlyWithTheToolGuestsAreRefused(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	f := &agentFixture{t: t}
	f.Fixture = p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"share.test"}})
	root, err := filepath.EvalSymlinks(f.Fixture.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	start, next, own, other := filepath.Join(root, "start"), filepath.Join(root, "next"), filepath.Join(root, "own"), filepath.Join(root, "other")
	for _, directory := range []string{start, filepath.Join(start, "alpha"), filepath.Join(start, "beta"), next, own, other} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.Owner = f.Fixture.Browser()
	sessions, err := p2test.HoldIn(f.Owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", start), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	f.Session = object(created).Value("id").(string)
	f.Chat = f.hold(1)
	statuses, children := f.statuses(), f.children()

	serial := 0
	// call sends message to target and returns the tool results it added, once the session is idle.
	call := func(message string, target *host.Reference, tool string) []*delta.JsonObject {
		t.Helper()
		before := len(everyResult(f.until(target.Member("view"), func(v delta.JsonValue) bool { return v != nil }), tool))
		serial++
		if _, err := target.Call("send", delta.JsonObjectOf("text", message, "requestId", "locations-"+strconv.Itoa(serial)), p2test.Background); err != nil {
			t.Fatal(err)
		}
		view := f.until(target.Member("view"), func(v delta.JsonValue) bool { return len(everyResult(v, tool)) > before && idle(v) }, 30*time.Second)
		f.quiet(statuses)
		return everyResult(view, tool)[before:]
	}
	callMain := func(message string) []*delta.JsonObject { return call(message, f.Chat, "") }
	child := func(name string) *delta.JsonObject {
		t.Helper()
		view := f.until(children.Member("children"), func(v delta.JsonValue) bool { return f.childNamed(v, name) != nil })
		return f.childNamed(view, name)
	}
	locationOf := func(name string) any { return child(name).Value("location") }
	cwdOf := func(name string) any {
		if location := object(locationOf(name)); location != nil {
			return location.Value("cwd")
		}
		return nil
	}
	untilCwd := func(name, cwd string) {
		t.Helper()
		f.until(children.Member("children"), func(v delta.JsonValue) bool {
			agent := f.childNamed(v, name)
			return agent != nil && object(agent.Value("location")) != nil && object(agent.Value("location")).Value("cwd") == cwd
		})
	}
	matches := func(pattern, text string) {
		t.Helper()
		if !regexp.MustCompile(pattern).MatchString(text) {
			t.Errorf("%q does not match %s", text, pattern)
		}
	}
	subagent := func(args map[string]any) string { return "subagent " + marshal(t, args) }

	// The main agent lists environments and folders.
	envs := callMain("location_envs {}")[0]
	if envs.Value("isError") != false {
		t.Fatalf("location_envs: %s", resultText(envs))
	}
	matches("You work at "+regexp.QuoteMeta(start), resultText(envs))
	matches(`- local: this device \(where p2 runs\) · you are here`, resultText(envs))
	dirs := callMain("location_dirs " + marshal(t, map[string]any{"location": "."}))[0]
	if dirs.Value("isError") != false {
		t.Fatalf("location_dirs: %s", resultText(dirs))
	}
	matches(`^.*start.*\nalpha/\nbeta/$`, resultText(dirs))
	missing := callMain("location_dirs " + marshal(t, map[string]any{"location": "nowhere:/x"}))[0]
	if missing.Value("isError") != true {
		t.Fatal("an unknown environment was listed")
	}
	matches(`Unknown environment "nowhere"`, resultText(missing))

	// It places subagents: its own location (env and cwd) and tools; without them a subagent follows the session.
	callMain(subagent(map[string]any{"action": "spawn", "name": "placed", "message": "hi", "env": nil, "cwd": "../own", "tools": []string{"bash", "location_move"}}))
	if got := marshal(t, locationOf("placed")); got != marshal(t, map[string]any{"cwd": own, "env": nil}) {
		t.Fatalf("placed location = %s", got)
	}
	if got := marshal(t, child("placed").Value("tools")); got != `["bash","location_move"]` {
		t.Fatalf("placed tools = %s", got)
	}
	callMain(subagent(map[string]any{"action": "spawn", "name": "plain", "message": "hi", "tools": []string{"bash"}}))
	if locationOf("plain") != nil {
		t.Fatalf("plain location = %v", locationOf("plain"))
	}
	// Reconfigured while idle: another directory, then following the session again, then back.
	callMain(subagent(map[string]any{"action": "configure", "name": "placed", "cwd": other}))
	untilCwd("placed", other)
	callMain(subagent(map[string]any{"action": "configure", "name": "placed", "followSession": true}))
	f.until(children.Member("children"), func(v delta.JsonValue) bool {
		agent := f.childNamed(v, "placed")
		return agent != nil && agent.Value("location") == nil
	})
	bad := callMain(subagent(map[string]any{"action": "configure", "name": "placed", "cwd": "nowhere"}))[0]
	matches(`cwd nowhere is not an existing directory`, resultText(bad))
	callMain(subagent(map[string]any{"action": "configure", "name": "placed", "cwd": own}))
	untilCwd("placed", own)

	// A subagent without the location tools cannot move; one given location_move moves only itself, at once.
	plain := f.hold(child("plain").Value("conversationId").(float64))
	sendTo := func(name, message string) {
		t.Helper()
		callMain(subagent(map[string]any{"action": "send", "name": name, "message": message}))
	}
	sendTo("plain", "location_move "+marshal(t, map[string]any{"location": other}))
	refused := f.until(plain.Member("view"), func(v delta.JsonValue) bool { return len(everyResult(v, "location_move")) > 0 })
	if everyResult(refused, "location_move")[0].Value("isError") != true || locationOf("plain") != nil {
		t.Fatalf("a subagent without the tool moved: %v", locationOf("plain"))
	}
	// A working subagent's location does not change under it.
	if _, err := plain.Call("send", delta.JsonObjectOf("text", "slow work", "requestId", "slow-plain"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	f.until(plain.Member("view"), func(v delta.JsonValue) bool { return !idle(v) })
	busy := callMain(subagent(map[string]any{"action": "configure", "name": "plain", "cwd": other}))[0]
	matches(`plain is working: stop it or wait before changing its location`, resultText(busy))
	if _, err := plain.Call("stop", delta.NewJsonObject(0), p2test.Background); err != nil {
		t.Fatal(err)
	}
	f.quiet(statuses)
	if locationOf("plain") != nil {
		t.Fatalf("plain location = %v", locationOf("plain"))
	}
	mover := f.hold(child("placed").Value("conversationId").(float64))
	sendTo("placed", "location_move "+marshal(t, map[string]any{"location": other}))
	moved := f.until(mover.Member("view"), func(v delta.JsonValue) bool { return len(everyResult(v, "location_move")) > 0 })
	if first := everyResult(moved, "location_move")[0]; first.Value("isError") != false {
		t.Fatalf("a subagent with the tool could not move: %s", resultText(first))
	}
	untilCwd("placed", other)
	if locationOf("plain") != nil {
		t.Fatal("the other subagent stays put")
	}

	// The main agent moves its session once its turn has ended; the follower follows, the placed one stays.
	scheduled := callMain("location_move " + marshal(t, map[string]any{"location": next}))[0]
	if scheduled.Value("isError") != false {
		t.Fatalf("location_move: %s", resultText(scheduled))
	}
	matches(`moves to .*next when this turn ends`, resultText(scheduled))
	f.until(sessions.Member("sessions"), func(v delta.JsonValue) bool {
		for _, each := range list(v) {
			if object(each).Value("id") == f.Session {
				return object(each).Value("cwd") == next
			}
		}
		return false
	}, 30*time.Second)
	agentCwd := func(v delta.JsonValue) any {
		docs := object(object(v).Value("docs"))
		if docs == nil || object(docs.Value("pi.agent")) == nil {
			return nil
		}
		return object(docs.Value("pi.agent")).Value("cwd")
	}
	// The worker clears the move's record once its relocation returned, which is when the move's fence is lifted: the next
	// message is admitted from then on.
	f.until(f.Chat.Member("view"), func(v delta.JsonValue) bool {
		move := object(object(object(v).Value("docs")).Value("p2.location-move"))
		return agentCwd(v) == next && (move == nil || move.Value("pending") == nil)
	})
	f.until(plain.Member("view"), func(v delta.JsonValue) bool { return agentCwd(v) == next })
	if cwdOf("placed") != other {
		t.Fatalf("placed cwd = %v", cwdOf("placed"))
	}
	matches("You work at "+regexp.QuoteMeta(next), resultText(callMain("location_envs {}")[0]))

	// Guests can neither discover environments nor move agents.
	guest := guestOf(t, f.Fixture, f.Owner, f.Session, "full", "Guest")
	guestChat, err := p2test.HoldIn(guest, chatcontract.Chat, delta.JsonObjectOf("session", f.Session, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	denied := call("location_envs {}", guestChat, "")[0]
	if denied.Value("isError") != true {
		t.Fatal("a guest listed the environments")
	}
	matches(`needs a turn the owner started`, resultText(denied))
	if noMove := call("location_move "+marshal(t, map[string]any{"location": start}), guestChat, "")[0]; noMove.Value("isError") != true {
		t.Fatal("a guest moved the session")
	}
	// Nor through a subagent, nor by placing one on another environment.
	moves := len(everyResult(f.until(mover.Member("view"), func(v delta.JsonValue) bool { return v != nil }), "location_move"))
	call(subagent(map[string]any{"action": "send", "name": "placed", "message": "location_move " + marshal(t, map[string]any{"location": own})}), guestChat, "")
	guestMove := f.until(mover.Member("view"), func(v delta.JsonValue) bool { return len(everyResult(v, "location_move")) > moves })
	if results := everyResult(guestMove, "location_move"); results[len(results)-1].Value("isError") != true {
		t.Fatal("a guest moved a subagent")
	}
	if cwdOf("placed") != other {
		t.Fatalf("placed cwd = %v", cwdOf("placed"))
	}
	elsewhere := call(subagent(map[string]any{"action": "spawn", "name": "far", "message": "hi", "env": "box", "cwd": "/"}), guestChat, "")[0]
	matches(`Another environment needs a turn the owner started`, resultText(elsewhere))
	for _, as := range []host.Host{guest, f.Owner} {
		ref, err := as.Hold(p2test.Background, locationscontract.AgentLocations, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, ref), data.CodeForbidden)
	}
	for _, ref := range []*host.Reference{f.Chat, plain, mover, guestChat} {
		ref.Release()
	}
}
