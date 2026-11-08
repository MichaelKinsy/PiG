// Ports test/p2/smoke.test.ts and test/p2/m1.test.ts (Pi main c23b9ec1): the Chat, Models, Sessions and status contracts
// against the compiled-in plugins.
//
// Pi's "keeps streaming through a compatible reload of the chat plugin" reloads unchanged code: Pi's build digests are the
// same, so nothing reaches the server host or the workers (the case asserts `stopped` is unset, the stream goes on and
// calls after it work). The Go case asserts the same, and more: P2Server.Reload re-evaluates the plugin's facets on the
// server and the running worker (twice: the second ships too), see hosts/reload.go.

package e2e_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	statuscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/status/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"

	// The plugins M1 runs with: the server and each session worker (this test binary) load what registered.
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

func TestMain(m *testing.M) {
	detachedChild()
	p2test.Main(m)
}

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// assistants are the assistant messages of a conversation view's entries (`entry.model[0].role === "assistant"`).
func assistants(view delta.JsonValue) []*delta.JsonObject {
	var out []*delta.JsonObject
	entries, _ := object(view).Value("entries").([]any)
	for _, entry := range entries {
		model, _ := object(entry).Value("model").([]any)
		if len(model) > 0 && object(model[0]).Value("role") == "assistant" {
			out = append(out, object(model[0]))
		}
	}
	return out
}

func lastAssistant(view delta.JsonValue) *delta.JsonObject {
	all := assistants(view)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

func answerText(message *delta.JsonObject) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	content, _ := message.Value("content").([]any)
	for _, part := range content {
		if object(part).Value("type") == "text" {
			text.WriteString(object(part).Value("text").(string))
		}
	}
	return text.String()
}

// live is `view.docs["pi.live"]`.
func live(view delta.JsonValue) *delta.JsonObject {
	return object(object(object(view).Value("docs")).Value("pi.live"))
}

func running(view delta.JsonValue) bool { return live(view) != nil && live(view).Value("run") != nil }

func partial(view delta.JsonValue) string {
	generation := object(live(view).Value("generation"))
	if generation == nil {
		return ""
	}
	return answerText(object(generation.Value("message")))
}

func status(statuses delta.JsonValue, session string) *delta.JsonObject {
	return object(object(statuses).Value(session))
}

func busyHas(entry *delta.JsonObject, conversation float64) bool {
	busy, _ := entry.Value("busy").([]any)
	for _, id := range busy {
		if id == conversation {
			return true
		}
	}
	return false
}

func TestSmokeCreatesASessionAndGetsAnAnswer(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "hello"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", object(created).Value("id"), "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "r1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool { return view != nil && len(assistants(view)) > 0 }, 0); err != nil {
		t.Fatal(err)
	}
}

// TestM1WorkingChat is M1's acceptance with the fake provider: create a session, stream an answer, stop, restart-required
// plugin reload, reopen after the worker stopped idle, archive and restore. The cases run in order, as the TypeScript's.
func TestM1WorkingChat(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Idle: 1500 * time.Millisecond})
	browser := fixture.Browser()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	statuses, err := p2test.HoldIn(browser, statuscontract.SessionStatus, delta.NewJsonObject(0), "reader")
	if err != nil {
		t.Fatal(err)
	}
	var sessionID string
	var chat = func() *host.Reference { return nil }
	var current *host.Reference
	chat = func() *host.Reference { return current }
	holdChat := func(t *testing.T) {
		t.Helper()
		ref, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", sessionID, "conversation", float64(1)), "participant")
		if err != nil {
			t.Fatal(err)
		}
		current = ref
	}
	until := func(t *testing.T, source p2test.JSONSource, predicate func(delta.JsonValue) bool, timeout time.Duration) delta.JsonValue {
		t.Helper()
		value, err := p2test.Until(source, predicate, timeout)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	send := func(t *testing.T, text, requestID string, extra ...any) delta.JsonValue {
		t.Helper()
		args := delta.JsonObjectOf(append([]any{"text", text, "requestId", requestID}, extra...)...)
		result, err := chat().Call("send", args, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	t.Run("lists models and creates a session with the default model", func(t *testing.T) {
		models, err := browser.Hold(p2test.Background, modelscontract.Models, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defaultModel := until(t, models.Member("defaultModel"), func(value delta.JsonValue) bool { return value != nil }, 0)
		if object(defaultModel).Value("provider") != "faux" {
			t.Fatalf("default model: %v", defaultModel)
		}
		if variant, _, _ := sessions.Variant(); variant != "owner" {
			t.Fatalf("variant %q", variant)
		}
		created, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace, "title", "  hello\nworld "), p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		session := object(created)
		sessionID, _ = session.Value("id").(string)
		if session.Value("title") != "hello world" {
			t.Fatalf("title %v", session.Value("title"))
		}
		if model := object(session.Value("model")); model.Value("provider") != "faux" || model.Value("modelId") != "faux-1" {
			t.Fatalf("model %v", session.Value("model"))
		}
		listed := until(t, sessions.Member("sessions"), func(list delta.JsonValue) bool {
			entries, _ := list.([]any)
			for _, entry := range entries {
				if object(entry).Value("id") == sessionID {
					return true
				}
			}
			return false
		}, 0)
		if object(listed.([]any)[0]).Value("cwd") != fixture.Workspace {
			t.Fatalf("cwd %v", listed)
		}
		if _, err := sessions.Call("create", delta.JsonObjectOf("cwd", "relative/path"), p2test.Background); err == nil || !strings.Contains(err.Error(), "absolute path") {
			t.Fatalf("a relative cwd: %v", err)
		}
		models.Release()
	})

	t.Run("sends a prompt and streams the answer", func(t *testing.T) {
		holdChat(t)
		first := object(send(t, "hello", "r1")).Value("submissionId")
		// A retried request is admitted once.
		if again := object(send(t, "hello", "r1")).Value("submissionId"); again != first {
			t.Fatalf("a retried request was admitted twice: %v %v", first, again)
		}
		view := until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && answerText(lastAssistant(value)) == "Echo: hello"
		}, 0)
		if len(assistants(view)) != 1 {
			t.Fatalf("assistants: %d", len(assistants(view)))
		}
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool {
			entry := status(value, sessionID)
			return entry != nil && entry.Value("running") == true
		}, 0)
	})

	t.Run("lets the agent rename its session (a Durable tool calling the server)", func(t *testing.T) {
		send(t, "rename-session Greeting the agent", "rename")
		until(t, sessions.Member("sessions"), func(list delta.JsonValue) bool {
			entries, _ := list.([]any)
			for _, entry := range entries {
				if object(entry).Value("id") == sessionID && object(entry).Value("title") == "Greeting the agent" {
					return true
				}
			}
			return false
		}, 0)
		until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && !running(value) && strings.HasPrefix(answerText(lastAssistant(value)), "Echo")
		}, 0)
	})

	t.Run("configures the model of the session", func(t *testing.T) {
		if _, err := chat().Call("configure", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "faux-1"), "thinkingLevel", "off"), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			agent := object(object(object(value).Value("docs")).Value("pi.agent"))
			return agent != nil && agent.Value("thinkingLevel") == "off"
		}, 0)
		if _, err := chat().Call("configure", delta.JsonObjectOf("model", delta.JsonObjectOf("provider", "faux", "modelId", "missing")), p2test.Background); err == nil || !strings.Contains(err.Error(), "Unknown model") {
			t.Fatalf("an unknown model: %v", err)
		}
	})

	t.Run("streams partial text, shows the session busy, and stops", func(t *testing.T) {
		send(t, "slow one", "r2")
		// Send pushes the worker's status before it answers: the server's index shows the session busy already.
		store, err := fixture.Server.Host.Get(p2test.Background, statuscontract.StatusStore, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		value, _ := store.Member("statuses").Value()
		if !busyHas(status(value, sessionID), 1) {
			t.Fatalf("the index does not show the session busy: %v", value)
		}
		until(t, chat().Member("view"), func(value delta.JsonValue) bool { return value != nil && len(partial(value)) > 20 }, 0)
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool { return busyHas(status(value, sessionID), 1) }, 0)
		// A follow-up queued behind the run comes back to its author when stopping.
		send(t, "queued later", "r3", "whenBusy", "followUp")
		stopped, err := chat().Call("stop", nil, p2test.Background)
		if err != nil {
			t.Fatal(err)
		}
		restored, _ := object(stopped).Value("restored").([]any)
		if len(restored) != 1 || restored[0] != "queued later" {
			t.Fatalf("restored: %v", restored)
		}
		after, _ := store.Member("statuses").Value()
		if busy, _ := status(after, sessionID).Value("busy").([]any); len(busy) != 0 {
			t.Fatalf("stop did not push the idle status before it answered: %v", busy)
		}
		view := until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && !running(value) && lastAssistant(value) != nil && lastAssistant(value).Value("stopReason") == "aborted"
		}, 0)
		_ = view
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool {
			busy, _ := status(value, sessionID).Value("busy").([]any)
			return len(busy) == 0
		}, 0)
	})

	t.Run("keeps streaming through a compatible reload of the chat plugin", func(t *testing.T) {
		send(t, "slow two", "r4")
		until(t, chat().Member("view"), func(value delta.JsonValue) bool { return value != nil && len(partial(value)) > 20 }, 0)
		current, _ := chat().Member("view").Value()
		before := partial(current)
		// Pi's reload of unchanged code is a no-op (the build's digests are the same); here it re-evaluates the plugin on the
		// server and the running worker. Twice: a second reload of the same code ships as well (the counter is in the key).
		for round := 1; round <= 2; round++ {
			result, err := fixture.Server.Reload([]string{"chat"}, context.Background())
			if err != nil {
				t.Fatalf("reload %d: %v", round, err)
			}
			// chat is a worker-only plugin: the server host has nothing of it to renew.
			if len(result.Server.Plugins.Changed) != 0 || len(result.Server.Failed) != 0 {
				t.Fatalf("reload %d: the server's report = %+v", round, result.Server)
			}
			if len(result.Workers) != 1 || result.Workers[0].Kind != plugins.RolloutReported || !slices.Contains(result.Workers[0].Report.Plugins.Changed, "chat") || len(result.Workers[0].Report.Failed) != 0 {
				t.Fatalf("reload %d: the workers' reports = %+v\n%s", round, result.Workers, processLogText())
			}
		}
		// The stream continues after the reload: partials grow, then the full answer commits.
		until(t, chat().Member("view"), func(value delta.JsonValue) bool { return value != nil && len(partial(value)) > len(before)+40 }, 0)
		view := until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && !running(value) && strings.HasPrefix(answerText(lastAssistant(value)), "Echo: slow two")
		}, 30*time.Second)
		answer := lastAssistant(view)
		if answer.Value("stopReason") != "stop" || answerText(answer) != "Echo: slow two "+strings.Repeat("streaming ", 300) {
			t.Fatalf("the answer: %v", answerText(answer))
		}
		// Calls after the reload run on the new code.
		send(t, "after reload", "r5")
		until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && answerText(lastAssistant(value)) == "Echo: after reload"
		}, 0)
	})

	t.Run("stops the worker when idle and reopens the session from its storage", func(t *testing.T) {
		released := time.Now()
		chat().Release()
		// The released Chat lingers 30 s on the worker, which keeps it alive (Chord's activity counts lingering objects).
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool {
			entry := status(value, sessionID)
			return entry != nil && entry.Value("running") == false
		}, 60*time.Second)
		// The wait is the 30 s linger, then the 1.5 s idle time (found by a check each second), then the worker's exit; Pi
		// waits the same (m1.test.ts has the same 60 s budget).
		t.Logf("the worker stopped %s after the Chat was released", time.Since(released).Round(10*time.Millisecond))
		reopening := time.Now()
		holdChat(t)
		view := until(t, chat().Member("view"), func(value delta.JsonValue) bool { return value != nil && len(assistants(value)) >= 6 }, 0)
		t.Logf("the reopened session's view (a new worker process) took %s", time.Since(reopening).Round(10*time.Millisecond))
		if answerText(assistants(view)[0]) != "Echo: hello" {
			t.Fatalf("first answer %q", answerText(assistants(view)[0]))
		}
		// The new worker started on the epoch before the reload and was reconciled; its status reports still arrive.
		streaming := time.Now()
		send(t, "slow reopened", "r6")
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool { return busyHas(status(value, sessionID), 1) }, 0)
		until(t, chat().Member("view"), func(value delta.JsonValue) bool {
			return value != nil && strings.HasPrefix(answerText(lastAssistant(value)), "Echo: slow reopened")
		}, 30*time.Second)
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool {
			busy, _ := status(value, sessionID).Value("busy").([]any)
			return len(busy) == 0
		}, 0)
		t.Logf("the slow answer (3000 characters through the faux provider) took %s until the session was idle", time.Since(streaming).Round(10*time.Millisecond))
	})

	t.Run("archives (stopping the worker) and restores", func(t *testing.T) {
		if _, err := sessions.Call("archive", delta.JsonObjectOf("id", sessionID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		until(t, statuses.Member("statuses"), func(value delta.JsonValue) bool {
			entry := status(value, sessionID)
			return entry != nil && entry.Value("running") == false
		}, 30*time.Second)
		until(t, sessions.Member("sessions"), func(list delta.JsonValue) bool {
			entries, _ := list.([]any)
			for _, entry := range entries {
				if object(entry).Value("id") == sessionID && object(entry).Value("archivedAt") != nil {
					return true
				}
			}
			return false
		}, 0)
		chat().Release()
		// An archived session's worker does not start.
		refused, err := browser.Hold(p2test.Background, chatcontract.Chat, delta.JsonObjectOf("session", sessionID, "conversation", float64(1)), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if failure, err := p2test.FailureOf(refused); err != nil || failure == nil {
			t.Fatalf("an archived session's chat: %v %v", failure, err)
		}
		refused.Release()
		if _, err := sessions.Call("restore", delta.JsonObjectOf("id", sessionID), p2test.Background); err != nil {
			t.Fatal(err)
		}
		holdChat(t)
		until(t, chat().Member("view"), func(value delta.JsonValue) bool { return value != nil && len(assistants(value)) >= 7 }, 0)
		if err := p2test.Poll(func() (bool, error) { return p2runtime.StatusOf(chat()) == host.RefStateLive, nil }, 5*time.Second, "live chat"); err != nil {
			t.Fatal(err)
		}
	})
}

// processLogText is every log source's lines (what the server kept of its workers' output), for a failure's message.
func processLogText() string {
	var text strings.Builder
	for _, source := range p2runtime.ProcessLogs.Sources() {
		for _, line := range p2runtime.ProcessLogs.Lines(source.ID) {
			text.WriteString(source.ID + ": " + line.Text + "\n")
		}
	}
	return text.String()
}
