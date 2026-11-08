// Ports test/p2/relocate.test.ts (Pi main c23b9ec1) under the fake provider: a session's relocation fences admission at
// an actual idle boundary (the real worker, the real fence, catalog update and in-place move); the failing steps are
// simulated as Pi's `relocateWith` does, the fence and the restart are real.

package e2e_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	promptcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	subagentscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents/contract"
	workspacecontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/workspace"
)

// relocation is a server under the fake provider with the means the relocation cases share.
type relocation struct {
	t       *testing.T
	fixture *p2test.Fixture
	workers *host.Reference
	target  string
}

func newRelocation(t *testing.T, options hosts.ServerOptions) *relocation {
	t.Helper()
	fixture := p2test.StartFixture(t, options)
	workers, err := fixture.Server.Host.Get(p2test.Background, p2runtime.Hosts, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workers.Release)
	return &relocation{t: t, fixture: fixture, workers: workers, target: directory(t, fixture.Workspace, "destination")}
}

func directory(t *testing.T, parent, name string) string {
	t.Helper()
	path := filepath.Join(parent, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFileAt(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *relocation) until(source p2test.JSONSource, predicate func(delta.JsonValue) bool, timeout ...time.Duration) delta.JsonValue {
	r.t.Helper()
	limit := time.Duration(0)
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	value, err := p2test.Until(source, predicate, limit)
	if err != nil {
		r.t.Fatal(err)
	}
	return value
}

func (r *relocation) hold(owner host.Host, contract *contracts.Contract, params *delta.JsonObject, variant string) *host.Reference {
	r.t.Helper()
	ref, err := p2test.HoldIn(owner, contract, params, variant)
	if err != nil {
		r.t.Fatal(err)
	}
	return ref
}

// worker is the state of the session's worker as the server lists it (`Hosts.workers`).
type workerState struct {
	listed     bool
	running    bool
	generation float64
	pid        float64
	err        string
}

func (r *relocation) worker(session string) workerState {
	r.t.Helper()
	value, err := r.workers.Member("workers").Value()
	if err != nil {
		r.t.Fatal(err)
	}
	state := object(object(value).Value(session))
	if state == nil {
		return workerState{}
	}
	out := workerState{listed: true, running: state.Value("running") == true}
	out.generation, _ = state.Value("generation").(float64)
	out.pid, _ = state.Value("pid").(float64)
	out.err, _ = state.Value("error").(string)
	return out
}

func (r *relocation) catalogCwd(session string) string {
	r.t.Helper()
	answer, err := r.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		catalog, err := ctx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		return catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
	}, p2test.Background, host.RootOptions{})
	if err != nil {
		r.t.Fatal(err)
	}
	cwd, _ := object(answer).Value("cwd").(string)
	return cwd
}

// relocateWith is `SessionWorkers.relocate` to cwd with the real steps (fence, catalog update, move, cancel) as the Sessions
// plugin runs them, except those `change` replaces (it gets the real ones to wrap).
func (r *relocation) relocateWith(session, cwd string, env any, change func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps) error {
	r.t.Helper()
	_, err := r.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
		to := delta.JsonObjectOf("cwd", cwd, "env", env)
		catalog, err := ctx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		boundary := func() (*host.Reference, error) {
			return ctx.Get(p2runtime.WorkerBoundary, delta.JsonObjectOf("session", session), host.GetOptions{})
		}
		real := p2runtime.RelocationSteps{
			Prepare: func(context.Context) (string, error) {
				held, err := boundary()
				if err != nil {
					return "", err
				}
				answer, err := held.Call("prepare", delta.JsonObjectOf("to", to), ctx)
				if err != nil {
					return "", err
				}
				if object(answer).Value("accepted") == true {
					return "", nil
				}
				if refusal, _ := object(answer).Value("refusal").(string); refusal != "" {
					return refusal, nil
				}
				return "refused", nil
			},
			Update: func(context.Context) error {
				_, err := catalog.Call("update", delta.JsonObjectOf("id", session, "cwd", cwd, "env", env), ctx)
				return err
			},
			Move: func(context.Context) error {
				held, err := boundary()
				if err != nil {
					return err
				}
				_, err = held.Call("relocate", to, ctx)
				return err
			},
			Cancel: func(context.Context) error {
				held, err := boundary()
				if err != nil {
					return err
				}
				_, err = held.Call("cancel", nil, ctx)
				return err
			},
		}
		return nil, r.fixture.Server.Workers().Relocate(ctx, session, change(real))
	}, p2test.Background, host.RootOptions{})
	return err
}

func viewText(view delta.JsonValue) string { return strings.Join(textsOf(view), "\n") }

// bashResults are the output of every bash tool result, in order.
func bashResults(view delta.JsonValue) []string {
	var out []string
	for _, message := range everyResult(view, "bash") {
		var text []string
		for _, part := range list(message.Value("content")) {
			if object(part).Value("type") == "text" {
				value, _ := object(part).Value("text").(string)
				text = append(text, strings.TrimSpace(value))
			} else {
				text = append(text, "")
			}
		}
		out = append(out, strings.Join(text, ","))
	}
	return out
}

func agentCwdOf(view delta.JsonValue) any {
	agent := object(object(object(view).Value("docs")).Value("pi.agent"))
	if agent == nil {
		return nil
	}
	return agent.Value("cwd")
}

func tasksAre(session string, count float64) func(delta.JsonValue) bool {
	return func(value delta.JsonValue) bool {
		entry := object(object(value).Value(session))
		return entry != nil && entry.Value("tasks") == count
	}
}

// idleRelocation is a session in a directory of its own whose worker runs and is idle, with an open Chat.
type idleRelocation struct {
	r        *relocation
	owner    host.Host
	sessions *host.Reference
	status   *host.Reference
	session  string
	chat     *host.Reference
	start    string
	next     string
	before   workerState
	name     string
	sent     int
}

func (r *relocation) idleSession(name string) *idleRelocation {
	r.t.Helper()
	s := &idleRelocation{r: r, name: name, owner: r.fixture.Browser()}
	s.sessions = r.hold(s.owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	s.status = r.hold(s.owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	s.start = directory(r.t, r.fixture.Workspace, name+"-start")
	s.next = directory(r.t, r.fixture.Workspace, name+"-next")
	created, err := s.sessions.Call("create", delta.JsonObjectOf("cwd", s.start), p2test.Background)
	if err != nil {
		r.t.Fatal(err)
	}
	s.session, _ = object(created).Value("id").(string)
	s.chat = s.chatOf(s.owner)
	s.answers(s.chat)
	s.before = r.worker(s.session)
	if !s.before.running {
		r.t.Fatalf("the worker is not running: %+v", s.before)
	}
	return s
}

func (s *idleRelocation) chatOf(owner host.Host) *host.Reference {
	s.r.t.Helper()
	return s.r.hold(owner, chatcontract.Chat, delta.JsonObjectOf("session", s.session, "conversation", float64(1)), "participant")
}

// send sends a message to ref: the answer's error is the admission's.
func (s *idleRelocation) send(ref *host.Reference) (string, error) {
	s.sent++
	message := "message " + itoa(s.sent)
	_, err := ref.Call("send", delta.JsonObjectOf("text", message, "requestId", s.name+"-"+itoa(s.sent)), p2test.Background)
	return message, err
}

// answers sends a message to ref and waits for its answer and an idle worker.
func (s *idleRelocation) answers(ref *host.Reference) {
	s.r.t.Helper()
	message, err := s.send(ref)
	if err != nil {
		s.r.t.Fatal(err)
	}
	s.awaitAnswer(ref, message)
}

func (s *idleRelocation) awaitAnswer(ref *host.Reference, message string) {
	s.r.t.Helper()
	s.r.until(ref.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(viewText(v), "Echo: "+message) })
	s.r.until(s.status.Member("statuses"), tasksAre(s.session, 0))
}

// gone is a wait for ref to leave `live` (its worker ended: the reference loses its object, loading again or failed while
// the worker restarts); start it before the action.
func (s *idleRelocation) gone(ref *host.Reference) func() {
	done := make(chan struct{})
	var once sync.Once
	stop := p2runtime.Follow[*host.RefState](ref.State(), func(state *host.RefState) {
		if state.Kind != host.RefStateLive {
			once.Do(func() { close(done) })
		}
	})
	return func() {
		s.r.t.Helper()
		defer stop()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			s.r.t.Fatal("the reference never left live")
		}
	}
}

// kill kills the worker process (a crash) and waits until the server saw it exit.
func (s *idleRelocation) kill() {
	s.r.t.Helper()
	pid := int(s.r.worker(s.session).pid)
	if pid == 0 {
		s.r.t.Fatal("no worker process")
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		s.r.t.Fatal(err)
	}
	s.r.until(s.r.workers.Member("workers"), func(v delta.JsonValue) bool {
		state := object(object(v).Value(s.session))
		return state != nil && state.Value("running") == false
	})
}

func (s *idleRelocation) cwdIs(want string) func(delta.JsonValue) bool {
	return func(view delta.JsonValue) bool { return agentCwdOf(view) == want }
}

func mustContain(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), pattern) {
		t.Fatalf("error = %v, want one containing %q", err, pattern)
	}
}

func TestSessionRelocationFakeProvider(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	r := newRelocation(t, hosts.ServerOptions{})

	t.Run("relocates a cold session without starting a worker and validates directories", func(t *testing.T) {
		owner := r.fixture.Browser()
		sessions := r.hold(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", r.fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := object(created).Value("id").(string)
		if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", r.target, "env", nil), p2test.Background); err != nil {
			t.Fatal(err)
		}
		rowOf := func(v delta.JsonValue) *delta.JsonObject {
			for _, each := range list(v) {
				if object(each).Value("id") == id {
					return object(each)
				}
			}
			return nil
		}
		rows := r.until(sessions.Member("sessions"), func(v delta.JsonValue) bool {
			row := rowOf(v)
			return row != nil && row.Value("cwd") == r.target
		})
		if env := rowOf(rows).Value("env"); env != nil {
			t.Fatalf("env = %v, want null", env)
		}
		if state := r.worker(id); state.listed {
			t.Fatalf("a worker was started: %+v", state)
		}
		file := filepath.Join(r.target, "file.txt")
		writeFileAt(t, file, "not a directory")
		_, err = sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", file, "env", nil), p2test.Background)
		mustContain(t, err, "Working directory is not a directory")
		_, err = sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", r.target, "env", "missing"), p2test.Background)
		mustContain(t, err, "not configured")
	})

	t.Run("moves a viewed idle worker in place: references stay live, tools, prompt and workspace follow", func(t *testing.T) {
		owner := r.fixture.Browser()
		sessions := r.hold(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		status := r.hold(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", r.fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := object(created).Value("id").(string)
		// A project template that exists only at the destination: the prompt resources load from the new cwd.
		writeFileAt(t, filepath.Join(r.target, ".pi", "prompts", "moved.md"), "Moved here: $@")
		writeFileAt(t, filepath.Join(r.target, "only-at-destination.txt"), "x")
		params := delta.JsonObjectOf("session", id, "conversation", float64(1))
		chat := r.hold(owner, chatcontract.Chat, params, "participant")
		prompts, err := owner.Hold(p2test.Background, promptcontract.PromptResources, params, host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		workspace, err := owner.Hold(p2test.Background, workspacecontract.Workspace, params, host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "before move", "requestId", "before"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		r.until(chat.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(viewText(v), "Echo: before move") })
		r.until(status.Member("statuses"), tasksAre(id, 0))
		names := func() []string {
			t.Helper()
			answer, err := workspace.Call("query", delta.JsonObjectOf("kind", "list", "path", ""), p2test.Background)
			if err != nil {
				t.Fatal(err)
			}
			if object(answer).Value("kind") != "list" {
				t.Fatalf("answer = %v", answer)
			}
			var out []string
			for _, entry := range list(object(answer).Value("entries")) {
				name, _ := object(entry).Value("name").(string)
				out = append(out, name)
			}
			return out
		}
		contains := func(names []string, name string) bool {
			for _, each := range names {
				if each == name {
					return true
				}
			}
			return false
		}
		if contains(names(), "only-at-destination.txt") {
			t.Fatal("the destination's file is listed before the move")
		}
		hasTemplate := func(v delta.JsonValue) bool {
			for _, each := range list(v) {
				if object(each).Value("name") == "moved" {
					return true
				}
			}
			return false
		}
		templates := r.until(prompts.Member("templates"), func(v delta.JsonValue) bool { return v != nil })
		if hasTemplate(templates) {
			t.Fatal("the destination's template is listed before the move")
		}
		generation := r.worker(id)
		changes, _ := workspace.Member("changes").Value()
		changesBefore, _ := changes.(float64)

		if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", r.target, "env", nil), p2test.Background); err != nil {
			t.Fatal(err)
		}
		// The same handles: nothing went gone, and the worker was not restarted.
		view := r.until(chat.Member("view"), func(v delta.JsonValue) bool { return agentCwdOf(v) == r.target })
		if !strings.Contains(viewText(view), "Echo: before move") {
			t.Fatal("the conversation lost its history")
		}
		if p2runtime.StatusOf(chat) != host.RefStateLive {
			t.Fatalf("chat is %v", p2runtime.StatusOf(chat))
		}
		if after := r.worker(id); after != generation {
			t.Fatalf("worker = %+v, want %+v (not restarted)", after, generation)
		}
		r.until(prompts.Member("templates"), hasTemplate)
		r.until(workspace.Member("changes"), func(v delta.JsonValue) bool { number, _ := v.(float64); return number > changesBefore })
		if !contains(names(), "only-at-destination.txt") {
			t.Fatal("the destination's file is not listed after the move")
		}
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "bash", "requestId", "after"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		settled := r.until(chat.Member("view"), func(v delta.JsonValue) bool { return len(bashResults(v)) == 1 })
		if got := bashResults(settled)[0]; got != r.target {
			t.Fatalf("bash printed %q, want %q", got, r.target)
		}
		for _, ref := range []*host.Reference{chat, prompts, workspace} {
			ref.Release()
		}
	})

	// Pi Mobile 24e3fccdd: subagents that followed the session move with it; one spawned with its own location stays,
	// with its prompt resources.
	t.Run("moves following subagents with the session, not one with its own location", func(t *testing.T) {
		owner := r.fixture.Browser()
		sessions := r.hold(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		status := r.hold(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		agents := r.hold(owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		start := directory(t, r.fixture.Workspace, "start")
		own := directory(t, r.fixture.Workspace, "own")
		next := directory(t, r.fixture.Workspace, "next")
		// A template only in the subagent's own directory, one only at the session's destination.
		for directory, name := range map[string]string{own: "mine", next: "theirs"} {
			writeFileAt(t, filepath.Join(directory, ".pi", "prompts", name+".md"), name+": $@")
		}
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", start), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := object(created).Value("id").(string)
		chatParams := func(conversation float64) *delta.JsonObject {
			return delta.JsonObjectOf("session", id, "conversation", conversation)
		}
		root := r.hold(owner, chatcontract.Chat, chatParams(1), "participant")
		subagent := func(args map[string]any, requestId string) {
			t.Helper()
			message := "subagent " + marshal(t, args)
			if _, err := root.Call("send", delta.JsonObjectOf("text", message, "requestId", requestId), p2test.Background); err != nil {
				t.Fatal(err)
			}
			r.until(root.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(viewText(v), "Echo: "+message) })
			r.until(status.Member("statuses"), tasksAre(id, 0))
		}
		subagent(map[string]any{"action": "spawn", "name": "follow", "message": "hi", "tools": []string{"bash"}}, "follow")
		subagent(map[string]any{"action": "spawn", "name": "own", "message": "hi", "cwd": own, "tools": []string{"bash"}}, "own")
		if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", next, "env", nil), p2test.Background); err != nil {
			t.Fatal(err)
		}
		root.Release()
		current := r.hold(owner, chatcontract.Chat, chatParams(1), "participant")
		r.until(current.Member("view"), func(v delta.JsonValue) bool { return agentCwdOf(v) == next })
		children := r.until(agents.Member("children"), func(v delta.JsonValue) bool { return len(list(object(v).Value(id))) == 2 })
		type answer struct {
			child   *delta.JsonObject
			cwd     any
			settled delta.JsonValue
		}
		pwd := func(name, requestId string) answer {
			t.Helper()
			var child *delta.JsonObject
			for _, each := range list(object(children).Value(id)) {
				if object(each).Value("name") == name {
					child = object(each)
				}
			}
			if child == nil {
				t.Fatalf("no child %s", name)
			}
			text := "subagent " + marshal(t, map[string]any{"action": "send", "name": name, "message": "bash"})
			if _, err := current.Call("send", delta.JsonObjectOf("text", text, "requestId", requestId), p2test.Background); err != nil {
				t.Fatal(err)
			}
			view := r.hold(owner, chatcontract.Chat, chatParams(child.Value("conversationId").(float64)), "participant")
			defer view.Release()
			settled := r.until(view.Member("view"), func(v delta.JsonValue) bool { return len(everyResult(v, "bash")) > 0 }, 20*time.Second)
			// The answer's report runs the root once more: idle before the next message, which would steer it.
			r.until(current.Member("view"), func(v delta.JsonValue) bool {
				return strings.Contains(viewText(v), `<sender kind="subagent" name="`+name+`"/>`) && !running(v)
			})
			r.until(status.Member("statuses"), tasksAre(id, 0))
			return answer{child: child, cwd: agentCwdOf(settled), settled: settled}
		}
		result := func(view delta.JsonValue) string { return bashResults(view)[0] }
		follow := pwd("follow", "follow-bash")
		if follow.cwd != next || follow.child.Value("location") != nil {
			t.Fatalf("follow: cwd %v, location %v", follow.cwd, follow.child.Value("location"))
		}
		if got := result(follow.settled); got != next {
			t.Fatalf("follow printed %q, want %q", got, next)
		}
		owned := pwd("own", "own-bash")
		location := object(owned.child.Value("location"))
		if owned.cwd != own || location == nil || location.Value("cwd") != own || location.Value("env") != nil {
			t.Fatalf("own: cwd %v, location %v", owned.cwd, owned.child.Value("location"))
		}
		if got := result(owned.settled); got != own {
			t.Fatalf("own printed %q, want %q", got, own)
		}
		// Prompt resources load where each conversation works: the subagent's own directory, the session's new one.
		templates := func(conversation float64) string {
			t.Helper()
			resources, err := owner.Hold(p2test.Background, promptcontract.PromptResources, chatParams(conversation), host.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer resources.Release()
			listed := r.until(resources.Member("templates"), func(v delta.JsonValue) bool { return len(list(v)) > 0 })
			var names []string
			for _, each := range list(listed) {
				names = append(names, object(each).Value("name").(string))
			}
			return strings.Join(names, ",")
		}
		if got := templates(owned.child.Value("conversationId").(float64)); got != "mine" {
			t.Fatalf("own templates = %q", got)
		}
		if got := templates(follow.child.Value("conversationId").(float64)); got != "theirs" {
			t.Fatalf("follow templates = %q", got)
		}
		if got := templates(1); got != "theirs" {
			t.Fatalf("session templates = %q", got)
		}
		// A subagent's own location never refuses a move; a destination that is not configured does.
		_, err = sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", "/", "env", "box"), p2test.Background)
		mustContain(t, err, "not configured")
		current.Release()
	})

	// The catalog is already moved when the worker fails to follow: the server restarts it, and the new worker starts at
	// the new location (its start reconciles the agent cwds). The failing step is simulated; the fence and restart are real.
	t.Run("restarts a worker that cannot follow the move in place", func(t *testing.T) {
		s := r.idleSession("fallback")
		gone := s.gone(s.chat)
		cancelled := false
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Move = func(context.Context) error { return errors.New("cannot follow") }
			real.Cancel = func(context.Context) error { cancelled = true; return nil }
			return real
		})
		if err != nil {
			t.Fatal(err)
		}
		if cancelled {
			t.Fatal("the update succeeded: nothing to cancel")
		}
		after := r.worker(s.session)
		if !after.running || after.generation <= s.before.generation {
			t.Fatalf("worker = %+v, want a new running one (was %+v)", after, s.before)
		}
		gone() // a restart: the old worker's objects end
		s.chat.Release()
		current := s.chatOf(s.owner)
		r.until(current.Member("view"), s.cwdIs(s.next))
		if _, err := current.Call("send", delta.JsonObjectOf("text", "bash", "requestId", "fallback-bash"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		settled := r.until(current.Member("view"), func(v delta.JsonValue) bool { return len(bashResults(v)) == 1 })
		if got := bashResults(settled)[0]; got != s.next {
			t.Fatalf("bash printed %q, want %q", got, s.next)
		}
		current.Release()
	})

	t.Run("keeps the move when the restart after a failed follow fails too: the start failure is the worker's status", func(t *testing.T) {
		s := r.idleSession("fallback-fails")
		// The new location's environment is not configured: the restart is refused (as any start would be).
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			update := real.Update
			real.Update = func(ctx context.Context) error {
				if err := update(ctx); err != nil {
					return err
				}
				_, err := r.fixture.Server.Host.Root(func(rootCtx host.Ctx) (any, error) {
					catalog, err := rootCtx.Get(sessionscontract.Catalog, delta.NewJsonObject(0), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					return catalog.Call("update", delta.JsonObjectOf("id", s.session, "env", "missing"), rootCtx)
				}, p2test.Background, host.RootOptions{})
				return err
			}
			real.Move = func(context.Context) error { return errors.New("cannot follow") }
			return real
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := r.catalogCwd(s.session); got != s.next {
			t.Fatalf("catalog cwd = %q, want %q", got, s.next)
		}
		state := r.worker(s.session)
		if state.running || !strings.Contains(state.err, "not configured") {
			t.Fatalf("worker = %+v, want stopped with a not-configured error", state)
		}
		s.chat.Release()
		if _, err := s.sessions.Call("archive", delta.JsonObjectOf("id", s.session), p2test.Background); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("lifts the fence when the catalog update fails", func(t *testing.T) {
		s := r.idleSession("update-fails")
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Update = func(context.Context) error { return errors.New("catalog down") }
			return real
		})
		mustContain(t, err, "catalog down")
		if got := r.catalogCwd(s.session); got != s.start {
			t.Fatalf("catalog cwd = %q, want %q", got, s.start)
		}
		if after := r.worker(s.session); after != s.before {
			t.Fatalf("worker = %+v, want the same %+v", after, s.before)
		}
		s.answers(s.chat) // admitted: not fenced
		s.chat.Release()
	})

	t.Run("stops a worker whose fence cannot be lifted, or whose prepare failed after the fence", func(t *testing.T) {
		s := r.idleSession("cancel-fails")
		gone := s.gone(s.chat)
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Update = func(context.Context) error { return errors.New("catalog down") }
			real.Cancel = func(context.Context) error { return errors.New("cancel lost") }
			return real
		})
		mustContain(t, err, "catalog down")
		if r.worker(s.session).running {
			t.Fatal("the worker is left running, fenced")
		}
		gone()
		s.chat.Release()
		// The next use starts a worker that admits again (at the old location: the catalog did not move).
		chat := s.chatOf(s.owner)
		r.until(chat.Member("view"), s.cwdIs(s.start))
		s.answers(chat)
		gone = s.gone(chat)
		err = r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			prepare := real.Prepare
			real.Prepare = func(ctx context.Context) (string, error) {
				refusal, err := prepare(ctx)
				if err != nil || refusal != "" {
					t.Errorf("the fence: %q, %v", refusal, err)
				}
				return "", errors.New("answer lost")
			}
			return real
		})
		mustContain(t, err, "answer lost")
		if r.worker(s.session).running {
			t.Fatal("the worker is left running after a failed prepare")
		}
		gone()
		if got := r.catalogCwd(s.session); got != s.start {
			t.Fatalf("catalog cwd = %q, want %q", got, s.start)
		}
		chat.Release()
	})

	t.Run("moves only the catalog when the worker exits while being fenced", func(t *testing.T) {
		s := r.idleSession("prepare-races-exit")
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Prepare = func(context.Context) (string, error) {
				s.kill()
				return "", errors.New("link lost")
			}
			return real
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := r.catalogCwd(s.session); got != s.next {
			t.Fatalf("catalog cwd = %q, want %q", got, s.next)
		}
		s.chat.Release()
		chat := s.chatOf(s.owner)
		r.until(chat.Member("view"), s.cwdIs(s.next))
		chat.Release()
	})

	// A subagent with its own location stays where it is, on another machine too: the worker's check under the fence
	// accepts such a move (it fails later only because no environment "box" exists here; envs.test.ts moves to one).
	t.Run("accepts a move to another machine under the fence while a subagent has its own location", func(t *testing.T) {
		s := r.idleSession("own-location")
		own := directory(t, r.fixture.Workspace, "own-location-own")
		spawn := map[string]any{"action": "spawn", "name": "mine", "message": "hi", "cwd": own, "tools": []string{"bash"}}
		if _, err := s.chat.Call("send", delta.JsonObjectOf("text", "subagent "+marshal(t, spawn), "requestId", "own-location-spawn"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		r.until(s.chat.Member("view"), func(v delta.JsonValue) bool {
			return strings.Contains(viewText(v), `<sender kind="subagent" name="mine"/>`) && !running(v)
		})
		s.answers(s.chat)
		_, err := r.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			boundary, err := ctx.Get(p2runtime.WorkerBoundary, delta.JsonObjectOf("session", s.session), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			answer, err := boundary.Call("prepare", delta.JsonObjectOf("to", delta.JsonObjectOf("cwd", "/", "env", "box")), ctx)
			if err != nil {
				return nil, err
			}
			if object(answer).Value("accepted") != true || object(answer).Value("refusal") != nil {
				t.Errorf("prepare = %v, want accepted without a refusal", answer)
			}
			return boundary.Call("cancel", nil, ctx)
		}, p2test.Background, host.RootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s.answers(s.chat) // the fence is lifted
		if after := r.worker(s.session); after != s.before {
			t.Fatalf("worker = %+v, want the same %+v", after, s.before)
		}
		s.chat.Release()
	})

	// Stops and catalog updates without the worker hold route resolution: a client arriving meanwhile waits and then
	// gets a worker, instead of failing as if it had called the dying one.
	t.Run("lets a client arriving while the catalog moves for an exited worker wait for it", func(t *testing.T) {
		s := r.idleSession("arrival")
		var arriving *host.Reference
		err := r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Prepare = func(context.Context) (string, error) {
				s.kill()
				return "", errors.New("link lost")
			}
			update := real.Update
			real.Update = func(ctx context.Context) error {
				// Held, not awaited: it waits for the relocation this step is part of. Pi lets it reach the server first
				// (its input: a 300 ms delay inside the step).
				var err error
				arriving, err = s.owner.Hold(p2test.Background, chatcontract.Chat, delta.JsonObjectOf("session", s.session, "conversation", float64(1)), host.GetOptions{})
				if err != nil {
					return err
				}
				time.Sleep(300 * time.Millisecond)
				return update(ctx)
			}
			return real
		})
		if err != nil {
			t.Fatal(err)
		}
		if arriving == nil {
			t.Fatal("no client arrived")
		}
		if err := p2runtime.WhenLive(p2test.Background, arriving); err != nil {
			t.Fatal(err)
		}
		r.until(arriving.Member("view"), s.cwdIs(s.next))
		s.answers(arriving)
		arriving.Release()
		s.chat.Release()
	})

	// A worker dying right before the move is asked: resolving its route must fail, not queue a start behind this very
	// relocation (which would wait for itself forever).
	t.Run("does not hang when the worker dies between the fence and the move", func(t *testing.T) {
		s := r.idleSession("crash-before-move")
		done := make(chan error, 1)
		go func() {
			done <- r.relocateWith(s.session, s.next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
				move := real.Move
				real.Move = func(ctx context.Context) error {
					s.kill()
					return move(ctx)
				}
				return real
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the relocation hung")
		}
		if got := r.catalogCwd(s.session); got != s.next {
			t.Fatalf("catalog cwd = %q, want %q", got, s.next)
		}
		s.chat.Release()
		chat := s.chatOf(s.owner)
		r.until(chat.Member("view"), s.cwdIs(s.next))
		s.answers(chat)
		chat.Release()
	})

	// The steps run in a root of the server's own: whenever the caller gives up, the worker is never left fenced and
	// ends up where the catalog says.
	t.Run("never leaves the worker fenced when the caller aborts", func(t *testing.T) {
		s := r.idleSession("abort")
		for index, delay := range []time.Duration{0, time.Millisecond, 3 * time.Millisecond, 10 * time.Millisecond, 30 * time.Millisecond, 80 * time.Millisecond} {
			cwd := s.next
			if index%2 != 0 {
				cwd = s.start
			}
			abort, cancel := context.WithCancel(p2test.Background)
			timer := time.AfterFunc(delay, cancel)
			_, _ = r.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
				sessions, err := ctx.Get(sessionscontract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				return sessions.Call("relocate", delta.JsonObjectOf("id", s.session, "cwd", cwd, "env", nil), ctx)
			}, abort, host.RootOptions{Principal: p2runtime.ToChord(p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal})})
			timer.Stop()
			cancel()
			// Admitted again within a short time, whatever happened to the move.
			if err := p2test.Poll(func() (bool, error) {
				message, err := s.send(s.chat)
				if err != nil {
					if strings.Contains(err.Error(), "Working directory is changing") {
						return false, nil
					}
					return false, err
				}
				s.awaitAnswer(s.chat, message)
				return true, nil
			}, 10*time.Second, "an admitted send"); err != nil {
				t.Fatal(err)
			}
			expected := r.catalogCwd(s.session)
			r.until(s.chat.Member("view"), s.cwdIs(expected))
		}
		if after := r.worker(s.session); after != s.before {
			t.Fatalf("worker = %+v, want %+v: moved in place throughout", after, s.before)
		}
		s.chat.Release()
	})

	t.Run("rejects foreground and named child work, and prevents new admission after an idle fence", func(t *testing.T) {
		owner := r.fixture.Browser()
		sessions := r.hold(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		status := r.hold(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", r.fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := object(created).Value("id").(string)
		chatParams := func(conversation float64) *delta.JsonObject {
			return delta.JsonObjectOf("session", id, "conversation", conversation)
		}
		root := r.hold(owner, chatcontract.Chat, chatParams(1), "participant")
		send := func(ref *host.Reference, text, requestId string, extra ...any) error {
			_, err := ref.Call("send", delta.JsonObjectOf(append([]any{"text", text, "requestId", requestId}, extra...)...), p2test.Background)
			return err
		}
		if err := send(root, "slow run", "slow"); err != nil {
			t.Fatal(err)
		}
		// A refused move is a failed operation the owner sees (the call answered when the move started).
		refused := func(pattern string) {
			t.Helper()
			if _, err := sessions.Call("relocate", delta.JsonObjectOf("id", id, "cwd", r.target, "env", nil), p2test.Background); err != nil {
				t.Fatal(err)
			}
			failed := func(v delta.JsonValue) *delta.JsonObject {
				for _, each := range list(v) {
					if object(each).Value("session") == id && object(each).Value("phase") == "failed" {
						return object(each)
					}
				}
				return nil
			}
			failure := failed(r.until(sessions.Member("operations"), func(v delta.JsonValue) bool { return failed(v) != nil }))
			if text, _ := failure.Value("error").(string); !strings.Contains(text, pattern) {
				t.Fatalf("operation error = %q, want %q", text, pattern)
			}
			if _, err := sessions.Call("dismissOperation", delta.JsonObjectOf("operation", failure.Value("id")), p2test.Background); err != nil {
				t.Fatal(err)
			}
			// The dismissal is on the watched list before the next refusal is read from it.
			if _, err := p2test.Until(sessions.Member("operations"), func(v delta.JsonValue) bool { return failed(v) == nil }, 5*time.Second); err != nil {
				value, _ := sessions.Member("operations").Value()
				t.Fatalf("after dismissing %v: %s", failure.Value("id"), jsonText(t, value))
			}
		}
		refused("Stop the current run")
		// The refusal left no fence: input is admitted, and the session stayed where it was.
		if err := send(root, "queued meanwhile", "queued", "whenBusy", "followUp"); err != nil {
			t.Fatal(err)
		}
		if got := r.catalogCwd(id); got != r.fixture.Workspace {
			t.Fatalf("catalog cwd = %q, want %q", got, r.fixture.Workspace)
		}
		if _, err := root.Call("stop", nil, p2test.Background); err != nil {
			t.Fatal(err)
		}
		r.until(status.Member("statuses"), tasksAre(id, 0))
		if err := send(root, "subagent", "helper"); err != nil {
			t.Fatal(err)
		}
		agents := r.hold(owner, subagentscontract.Subagents, delta.NewJsonObject(0), "reader")
		children := r.until(agents.Member("children"), func(v delta.JsonValue) bool { return len(list(object(v).Value(id))) > 0 })
		child := object(list(object(children).Value(id))[0])
		r.until(status.Member("statuses"), tasksAre(id, 0))
		helper := r.hold(owner, chatcontract.Chat, chatParams(child.Value("conversationId").(float64)), "participant")
		if err := send(helper, "slow helper", "slow-child"); err != nil {
			t.Fatal(err)
		}
		refused("Subagent helper is still running")
		if _, err := helper.Call("stop", nil, p2test.Background); err != nil {
			t.Fatal(err)
		}
		r.until(status.Member("statuses"), tasksAre(id, 0))
		_, err = r.fixture.Server.Host.Root(func(ctx host.Ctx) (any, error) {
			boundary, err := ctx.Get(p2runtime.WorkerBoundary, delta.JsonObjectOf("session", id), host.GetOptions{})
			if err != nil {
				return nil, err
			}
			answer, err := boundary.Call("prepare", nil, ctx)
			if err != nil {
				return nil, err
			}
			if object(answer).Value("accepted") != true {
				t.Errorf("prepare = %v, want accepted", answer)
			}
			defer func() { _, _ = boundary.Call("cancel", nil, ctx) }()
			mustContain(t, send(root, "fenced", "fenced"), "Working directory is changing")
			return nil, nil
		}, p2test.Background, host.RootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := send(root, "unfenced", "unfenced"); err != nil {
			t.Fatal(err)
		}
		r.until(root.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(viewText(v), "Echo: unfenced") })
		boundary, err := owner.Hold(p2test.Background, p2runtime.WorkerBoundary, delta.JsonObjectOf("session", id), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		err = p2runtime.WhenLive(p2test.Background, boundary)
		if err == nil {
			_, err = boundary.Call("prepare", nil, p2test.Background)
		}
		mustContain(t, err, "server lifecycle only")
	})
}

// A worker that does not follow in time is restarted at the new location, like one whose move failed.
func TestSessionRelocationAMoveThatHangs(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	r := newRelocation(t, hosts.ServerOptions{MoveTimeout: 500 * time.Millisecond})

	t.Run("restarts the worker once the move takes longer than the bound", func(t *testing.T) {
		owner := r.fixture.Browser()
		sessions := r.hold(owner, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
		status := r.hold(owner, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
		next := directory(t, r.fixture.Workspace, "next")
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", r.fixture.Workspace), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := object(created).Value("id").(string)
		chat := r.hold(owner, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
		if _, err := chat.Call("send", delta.JsonObjectOf("text", "before", "requestId", "before"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		r.until(chat.Member("view"), func(v delta.JsonValue) bool { return strings.Contains(viewText(v), "Echo: before") })
		r.until(status.Member("statuses"), tasksAre(id, 0))
		before := r.worker(id).generation
		started := time.Now()
		err = r.relocateWith(id, next, nil, func(real p2runtime.RelocationSteps) p2runtime.RelocationSteps {
			real.Move = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			return real
		})
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(started) < 500*time.Millisecond {
			t.Fatal("did not wait for the bound")
		}
		if r.worker(id).generation <= before {
			t.Fatal("no new worker")
		}
		chat.Release()
		current := r.hold(owner, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
		r.until(current.Member("view"), func(v delta.JsonValue) bool { return agentCwdOf(v) == next })
		current.Release()
	})
}
