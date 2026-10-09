package durableagent

// pi: packages/coding-agent/src/experimental/vacation/sessions.ts

// pi: packages/coding-agent/src/experimental/vacation/runtime.ts

// pi: packages/coding-agent/src/experimental/durable/sessions.ts

// pi: packages/coding-agent/src/experimental/durable/runtime.ts

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

func answering(text string) func(int, string, http.ResponseWriter, *http.Request) {
	return func(_ int, _ string, w http.ResponseWriter, _ *http.Request) { sseText(w, text) }
}

// runtime.ts:153-237: a new session starts on the saved default model in the project directory, lists its main conversation and the models the runtime has, and opens with the task panel shown.
func TestOpenStartsANewSession(t *testing.T) {
	p := newProvider(t, answering("hi"))
	agentDir := isolate(t, p, "model")
	project := t.TempDir()
	session := openSession(t, project, false)
	view := session.View.Current()
	resolved, _ := filepath.EvalSymlinks(project)
	if view.Session.CWD != resolved || !strings.HasPrefix(view.Session.Directory, filepath.Join(agentDir, "experimental", "durable-sessions")) || filepath.Base(view.Session.Directory) != view.Session.ID {
		t.Fatalf("session = %+v", view.Session)
	}
	if len(view.Conversations) != 1 || view.Conversations[0].ID != durable.ROOT_CONVERSATION_ID || view.Conversations[0].Label != "main" || view.Conversations[0].Title != nil {
		t.Fatalf("conversations = %+v, want the main conversation without a title", view.Conversations)
	}
	var models []string
	for _, model := range view.Models {
		models = append(models, model.Provider+"/"+model.ModelId+"/"+model.Name)
	}
	if !contains(models, "scripted/model/Plain model") || !contains(models, "scripted/reasoner/Reasoning model") {
		t.Fatalf("models = %v", models)
	}
	agent := AgentOf(view.Conversation)
	if agent.Model == nil || *agent.Model != (durable.ModelRef{Provider: "scripted", ModelId: "model"}) || agent.Cwd == nil || *agent.Cwd != resolved {
		t.Fatalf("agent = %+v, want the saved default model in the project directory", agent)
	}
	if view.Tasks == nil {
		t.Fatal("the task panel starts open")
	}
	if got := notices(session); len(got) != 0 {
		t.Fatalf("notices = %v", got)
	}
}

// runtime.ts:172-176 and 297-301: without a usable model the session opens with a notice to select one.
func TestOpenWarnsWhenTheSavedModelIsUnavailable(t *testing.T) {
	p := newProvider(t, answering("hi"))
	agentDir := isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	// The next start finds the stored model gone from the catalog.
	writeModels(t, agentDir, p)
	write(t, filepath.Join(agentDir, "models.json"), `{"providers":{"scripted":{"baseUrl":"`+p.server.URL+`/v1","api":"openai-completions","apiKey":"fixture-key","models":[{"id":"reasoner","name":"Reasoning model","reasoning":true,"input":["text"],"contextWindow":64000,"maxTokens":1234}]}}}`)
	again := openSession(t, projectOf(t, session), true)
	if got := notices(again); !contains(got, "warning: Saved model is unavailable: scripted/model") {
		t.Fatalf("notices = %v", got)
	}
}

func projectOf(t *testing.T, session *OpenDurableResult) string {
	t.Helper()
	return session.View.Current().Session.CWD
}

// runtime.ts:248-259: a prompt runs the turn, and the view follows it: the user entry, the answer, and a listener call per burst.
func TestSubmitRunsATurnAndPublishesTheView(t *testing.T) {
	p := newProvider(t, answering("the answer"))
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	var calls atomic.Int32
	unsubscribe := session.View.Subscribe(func() { calls.Add(1) })
	awaitCompletion(t, session.Controller.Submit("a question", durable.WhenBusySteer))
	waitFor(t, "the answer", func() bool { return slices.Contains(assistantAnswers(session.View.Current()), "the answer") })
	if calls.Load() == 0 {
		t.Fatal("no view update reached the listener")
	}
	unsubscribe()
	after := calls.Load()
	awaitCompletion(t, session.Controller.Submit("again", durable.WhenBusySteer))
	waitFor(t, "the second answer", func() bool { return len(assistantAnswers(session.View.Current())) == 2 })
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != after {
		t.Fatal("an unsubscribed listener was called")
	}
}

// runtime.ts:194-205: a turn that ends without an answer shows a notice, except an abort, which the user asked for.
func TestAPromptWithoutAnAnswerShowsANoticeUnlessAborted(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var cancelled atomic.Bool
	p := newProvider(t, func(n int, _ string, w http.ResponseWriter, r *http.Request) {
		switch {
		case failing.Load() && n == 1:
			http.Error(w, `{"error":{"message":"the provider is unwell"}}`, http.StatusBadRequest)
		case n == 2:
			<-r.Context().Done()
			cancelled.Store(true)
		default:
			sseText(w, "fine")
		}
	})
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	awaitCompletion(t, session.Controller.Submit("first", durable.WhenBusySteer))
	waitFor(t, "the no-answer notice", func() bool { return contains(notices(session), "error: No answer: ") })

	// The second turn hangs in the provider; Abort cancels its request and ends the turn without a notice.
	before := len(session.View.Current().Notices)
	awaitCompletion(t, session.Controller.Submit("second", durable.WhenBusySteer))
	waitFor(t, "the second request", func() bool { return p.requests() >= 2 })
	awaitCompletion(t, session.Controller.Abort())
	waitFor(t, "the provider request to be cancelled", cancelled.Load)
	time.Sleep(100 * time.Millisecond)
	if got := len(session.View.Current().Notices); got != before {
		t.Fatalf("an abort added notices: %v", notices(session))
	}
}

// runtime.ts:318-337 and the queue of 282-286: commands run one at a time in the order they were asked, each failure shown as a notice. Selecting a reasoning model and cycling its thinking, asked together, only succeeds in that order.
func TestCommandsRunInOrderAndFailuresBecomeNotices(t *testing.T) {
	p := newProvider(t, answering("unused"))
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)

	cycle := session.Controller.CycleThinking()
	awaitCompletion(t, cycle)
	if got := notices(session); !contains(got, "error: Current model does not support thinking") {
		t.Fatalf("notices = %v", got)
	}

	reasoner := durable.ModelRef{Provider: "scripted", ModelId: "reasoner"}
	selected := session.Controller.SetModel(reasoner)
	cycled := session.Controller.CycleThinking()
	if finished(cycled) && !finished(selected) {
		t.Fatal("a later command finished before an earlier one")
	}
	awaitCompletion(t, cycled)
	agent := agentOfView(session)
	waitFor(t, "the model and the thinking level", func() bool {
		agent = agentOfView(session)
		return agent.Model != nil && *agent.Model == reasoner && agent.ThinkingLevel != "" && agent.ThinkingLevel != "off"
	})
	if got := notices(session); len(got) != 1 {
		t.Fatalf("notices = %v, want only the first failure", got)
	}

	awaitCompletion(t, session.Controller.SetModel(durable.ModelRef{Provider: "scripted", ModelId: "missing"}))
	if got := notices(session); !contains(got, "error: Unknown model: scripted/missing") {
		t.Fatalf("notices = %v", got)
	}
}

// runtime.ts:292-301: selecting a model clamps the thinking level to what the model supports.
func TestSetModelClampsThinking(t *testing.T) {
	p := newProvider(t, answering("unused"))
	isolate(t, p, "reasoner")
	session := openSession(t, t.TempDir(), false)
	awaitCompletion(t, session.Controller.CycleThinking())
	waitFor(t, "a thinking level", func() bool {
		return agentOfView(session).ThinkingLevel != "" && agentOfView(session).ThinkingLevel != "off"
	})
	awaitCompletion(t, session.Controller.SetModel(durable.ModelRef{Provider: "scripted", ModelId: "model"}))
	waitFor(t, "the plain model with thinking off", func() bool {
		agent := agentOfView(session)
		return agent.Model != nil && agent.Model.ModelId == "model" && (agent.ThinkingLevel == "off" || agent.ThinkingLevel == "")
	})
}

// runtime.ts:262-279: a manual compaction of a context that fits reports that there is nothing to compact.
func TestCompactReportsWhenThereIsNothingToCompact(t *testing.T) {
	p := newProvider(t, answering("short answer"))
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	awaitCompletion(t, session.Controller.Submit("hello", durable.WhenBusySteer))
	waitFor(t, "the answer", func() bool { return len(assistantAnswers(session.View.Current())) == 1 })
	awaitCompletion(t, session.Controller.Compact(nil))
	waitFor(t, "the compaction notice", func() bool {
		return contains(notices(session), "info: Nothing to compact: the context fits in the recent window that is kept verbatim.")
	})
}

// runtime.ts:303-312: the task panel is toggled by a command; closing it clears the graph from the view.
func TestToggleTasksShowsAndHidesTheGraph(t *testing.T) {
	p := newProvider(t, answering("unused"))
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	awaitCompletion(t, session.Controller.ToggleTasks())
	if session.View.Current().Tasks != nil {
		t.Fatal("the panel did not close")
	}
	awaitCompletion(t, session.Controller.ToggleTasks())
	if session.View.Current().Tasks == nil {
		t.Fatal("the panel did not open")
	}
}

// runtime.ts:93-160 and 313-325: a subagent's conversation appears in the view when it is created, titled by its task on the first user message, and the user can switch to it and back; a conversation that does not exist is a notice.
func TestSubagentConversationsAppearAndCanBeSwitchedTo(t *testing.T) {
	p := newProvider(t, func(n int, _ string, w http.ResponseWriter, _ *http.Request) {
		switch n {
		case 1:
			sseToolCall(w, "subagent", "call-1", `{"task":"  look   into\nthe weather  "}`)
		case 2:
			sseText(w, "child answer")
		default:
			sseText(w, "parent done")
		}
	})
	isolate(t, p, "model")
	project := t.TempDir()
	session := openSession(t, project, false)
	awaitCompletion(t, session.Controller.Submit("delegate", durable.WhenBusySteer))
	waitFor(t, "the parent's final answer", func() bool { return slices.Contains(assistantAnswers(session.View.Current()), "parent done") })
	var child ConversationSummary
	waitFor(t, "the subagent's summary with its title", func() bool {
		for _, summary := range session.View.Current().Conversations {
			if summary.ID != durable.ROOT_CONVERSATION_ID && summary.Title != nil {
				child = summary
				return true
			}
		}
		return false
	})
	if want := "subagent " + itoa(int64(child.ID)); child.Label != want || *child.Title != "look into the weather" {
		t.Fatalf("subagent summary = %+v, want label %q and the task as a one-line title", child, want)
	}
	awaitCompletion(t, session.Controller.SwitchConversation(child.ID))
	waitFor(t, "the subagent's view", func() bool { return session.View.Current().Conversation.Conversation.Id == child.ID })
	if !slices.Contains(assistantAnswers(session.View.Current()), "child answer") {
		t.Fatalf("the subagent's view shows %v", assistantAnswers(session.View.Current()))
	}
	awaitCompletion(t, session.Controller.SwitchConversation(durable.ROOT_CONVERSATION_ID))
	waitFor(t, "the main view", func() bool {
		return session.View.Current().Conversation.Conversation.Id == durable.ROOT_CONVERSATION_ID
	})
	awaitCompletion(t, session.Controller.SwitchConversation(9999))
	if got := notices(session); !contains(got, "error: Conversation 9999 does not exist") {
		t.Fatalf("notices = %v", got)
	}
	// The editor now talks to the shown conversation.
	awaitCompletion(t, session.Controller.SwitchConversation(child.ID))
	waitFor(t, "the subagent's view", func() bool { return session.View.Current().Conversation.Conversation.Id == child.ID })
	before := len(assistantAnswers(session.View.Current()))
	awaitCompletion(t, session.Controller.Submit("and tomorrow?", durable.WhenBusySteer))
	waitFor(t, "the subagent's second answer", func() bool { return len(assistantAnswers(session.View.Current())) == before+1 })

	// runtime.ts:206-208: a later user entry leaves the title alone (summary.title === undefined).
	if title := conversationTitle(session.View.Current(), child.ID); title == nil || *title != "look into the weather" {
		t.Fatalf("title after a second prompt = %q, want the task", deref(title))
	}
	// runtime.ts:103-115: on reopen the title is the oldest user entry; entries() is newest first, so findLast of the last page holds it.
	directory := session.View.Current().Session.Directory
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	continued := openSession(t, project, true)
	if got := continued.View.Current().Session.Directory; got != directory {
		t.Fatalf("continued %s, want %s", got, directory)
	}
	if title := conversationTitle(continued.View.Current(), child.ID); title == nil || *title != "look into the weather" {
		t.Fatalf("title after reopen = %q, want the oldest user entry", deref(title))
	}
}

func deref(text *string) string {
	if text == nil {
		return "<nil>"
	}
	return *text
}

func conversationTitle(view DurableView, id durable.ConversationId) *string {
	for _, summary := range view.Conversations {
		if summary.ID == id {
			return summary.Title
		}
	}
	return nil
}

func itoa(value int64) string { return strconv.FormatInt(value, 10) }

// runtime.ts:340-366: Close releases the session lock and its storage; closing again returns the same result. --continue opens the newest session of the directory and keeps its model, whatever the saved default says now.
func TestCloseReleasesTheSessionAndContinueKeepsItsModel(t *testing.T) {
	p := newProvider(t, answering("remembered"))
	agentDir := isolate(t, p, "model")
	project := t.TempDir()
	session := openSession(t, project, false)
	directory := session.View.Current().Session.Directory
	awaitCompletion(t, session.Controller.Submit("remember this", durable.WhenBusySteer))
	waitFor(t, "the answer", func() bool { return len(assistantAnswers(session.View.Current())) == 1 })
	first := session.Close()
	if first != nil {
		t.Fatal(first)
	}
	if again := session.Close(); again != nil {
		t.Fatalf("second close = %v, want the first result, nil", again)
	}
	if _, err := os.Stat(directory + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the session lock outlives Close: %v", err)
	}

	writeDefault(t, agentDir, "reasoner")
	continued := openSession(t, project, true)
	view := continued.View.Current()
	if view.Session.Directory != directory {
		t.Fatalf("continued %s, want %s", view.Session.Directory, directory)
	}
	if agent := AgentOf(view.Conversation); agent.Model == nil || agent.Model.ModelId != "model" {
		t.Fatalf("continued model = %+v, want the session's own model", agent.Model)
	}
	if !slices.Contains(assistantAnswers(view), "remembered") {
		t.Fatalf("the continued session lost its transcript: %v", assistantAnswers(view))
	}
}

// runtime.ts:362-365 and the README: Close writes no outcome, so a turn cut off in the provider resumes after --continue and finishes without the TUI handling any recovery.
func TestAnInterruptedTurnResumesAfterContinue(t *testing.T) {
	var hang atomic.Bool
	hang.Store(true)
	reached := make(chan struct{}, 1)
	p := newProvider(t, func(_ int, _ string, w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			select {
			case reached <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		sseText(w, "recovered answer")
	})
	isolate(t, p, "model")
	project := t.TempDir()
	session := openSession(t, project, false)
	awaitCompletion(t, session.Controller.Submit("finish this", durable.WhenBusySteer))
	<-reached
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	hang.Store(false)
	continued := openSession(t, project, true)
	waitFor(t, "the recovered answer", func() bool { return slices.Contains(assistantAnswers(continued.View.Current()), "recovered answer") })
}

// sessions.ts: a second process cannot open a session that is open.
func TestOpenRefusesASessionOpenElsewhere(t *testing.T) {
	previous := sessionLockWait
	sessionLockWait = 50 * time.Millisecond
	t.Cleanup(func() { sessionLockWait = previous })
	p := newProvider(t, answering("unused"))
	isolate(t, p, "model")
	project := t.TempDir()
	openSession(t, project, false)
	_, err := Open(context.Background(), OpenDurableOptions{CWD: project, ContinueSession: true}, CodingProfile)
	if err == nil || !strings.HasPrefix(err.Error(), "Session is already open in another process: ") {
		t.Fatalf("error = %v", err)
	}
}

// runtime.ts:127-130: the view keeps the newest twenty notices.
func TestNoticesKeepTheNewestTwenty(t *testing.T) {
	p := newProvider(t, answering("unused"))
	isolate(t, p, "model")
	session := openSession(t, t.TempDir(), false)
	var last Completion
	for i := range 25 {
		last = session.Controller.SwitchConversation(durable.ConversationId(1000 + i))
	}
	awaitCompletion(t, last)
	kept := session.View.Current().Notices
	if len(kept) != 20 {
		t.Fatalf("%d notices, want 20", len(kept))
	}
	if kept[0].Message != "Conversation 1005 does not exist" || kept[19].Message != "Conversation 1024 does not exist" {
		t.Fatalf("notices run from %q to %q, want the newest twenty", kept[0].Message, kept[19].Message)
	}
	for i := 1; i < len(kept); i++ {
		if kept[i].ID <= kept[i-1].ID {
			t.Fatalf("notice ids %d then %d are not increasing", kept[i-1].ID, kept[i].ID)
		}
	}
}
