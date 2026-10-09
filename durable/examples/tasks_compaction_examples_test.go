// Ports packages/durable/test/examples/24-child-tasks.ts and 25-compaction.ts. Upstream's scripts print and pace
// themselves with timers; each Go example asserts what the script prints.

package examples_test

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

type cardInput struct {
	Card string `json:"card"`
}

type chargeState struct {
	Phase string  `json:"phase"`
	At    float64 `json:"at"`
}

type checkoutInput struct {
	Cards []string `json:"cards"`
}

type checkoutState struct {
	Phase    string           `json:"phase"`
	Payments []durable.TaskId `json:"payments,omitempty"`
}

// 24-child-tasks.ts: a task that owns child tasks. A checkout charges four payments at once and waits for them; the
// task graph shows its tree while they run; a declined card, a cancellation and a restart each end it.
func TestExample24ChildTasks(t *testing.T) {
	// A fake bank: it declines an expired card at once, and confirms other charges after a moment.
	var bankMu sync.Mutex
	charged := map[string]bool{}
	var aborted []string
	var decided [][]durable.TaskOutcomeStatus

	type paymentRuntime = durable.TaskRuntime[cardInput, chargeState, cardInput, any]
	type paymentRunning = durable.RunningTask[cardInput, chargeState, cardInput]
	finish := func(ctx context.Context, runtime paymentRuntime, outcome durable.TaskOutcome[cardInput]) error {
		return runtime.Commit(ctx, func(durable.Tx, paymentRunning) (*durable.NextTaskState[chargeState, cardInput], error) {
			return &durable.NextTaskState[chargeState, cardInput]{Status: durable.TaskTerminal, Outcome: &outcome}, nil
		})
	}
	payment := durable.DefineTask(durable.TaskDefinition[cardInput, chargeState, cardInput, any]{
		Name:    "example.payment",
		Version: 1,
		Initial: func(cardInput) chargeState {
			return chargeState{Phase: "charge", At: float64(time.Now().UnixMilli()) + 100}
		},
		Phases: map[string]durable.PhaseHandler[cardInput, chargeState, cardInput, any]{
			"charge": func(ctx context.Context, task paymentRunning, runtime paymentRuntime) error {
				card := task.Input.Card
				if strings.HasPrefix(card, "expired") {
					return finish(ctx, runtime, durable.TaskOutcome[cardInput]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: card + " declined"}})
				}
				bankMu.Lock()
				charged[card] = true
				bankMu.Unlock()
				if err := runtime.Sleep(ctx, task.State.Checkpoint.At); err != nil {
					return err
				}
				return finish(ctx, runtime, durable.TaskOutcome[cardInput]{Status: durable.OutcomeCompleted, Result: &cardInput{Card: card}})
			},
		},
		// Each payment undoes its own effect when aborted.
		Abort: func(ctx context.Context, task paymentRunning, runtime paymentRuntime) error {
			bankMu.Lock()
			delete(charged, task.Input.Card)
			aborted = append(aborted, task.Input.Card)
			bankMu.Unlock()
			return finish(ctx, runtime, durable.TaskOutcome[cardInput]{Status: durable.OutcomeAborted})
		},
	})

	type checkoutRuntime = durable.TaskRuntime[checkoutInput, checkoutState, string, any]
	type checkoutRunning = durable.RunningTask[checkoutInput, checkoutState, string]
	// With failFast, the first payment that fails aborts the others; the checkout itself is not aborted and decides
	// its outcome once every payment is done.
	checkoutTask := durable.DefineTask(durable.TaskDefinition[checkoutInput, checkoutState, string, any]{
		Name:    "example.checkout",
		Version: 1,
		Initial: func(checkoutInput) checkoutState { return checkoutState{Phase: "pay"} },
		Phases: map[string]durable.PhaseHandler[checkoutInput, checkoutState, string, any]{
			"pay": func(ctx context.Context, task checkoutRunning, runtime checkoutRuntime) error {
				return runtime.Commit(ctx, func(tx durable.Tx, _ checkoutRunning) (*durable.NextTaskState[checkoutState, string], error) {
					var payments []durable.TaskId
					for _, card := range task.Input.Cards {
						id, err := durable.CreateTask(tx, payment, cardInput{Card: card}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: task.Id}})
						if err != nil {
							return nil, err
						}
						payments = append(payments, id)
					}
					return &durable.NextTaskState[checkoutState, string]{Status: durable.TaskWaiting, Checkpoint: &checkoutState{Phase: "decide", Payments: payments}, On: payments, Policy: durable.JoinFailFast}, nil
				})
			},
			"decide": func(ctx context.Context, task checkoutRunning, runtime checkoutRuntime) error {
				outcomes, err := runtime.Outcomes(ctx, task.State.Checkpoint.Payments)
				if err != nil {
					return err
				}
				paid := true
				var statuses []durable.TaskOutcomeStatus
				for _, outcome := range outcomes {
					statuses = append(statuses, outcome.Status)
					paid = paid && outcome.Status == durable.OutcomeCompleted
				}
				bankMu.Lock()
				decided = append(decided, statuses)
				bankMu.Unlock()
				return runtime.Commit(ctx, func(durable.Tx, checkoutRunning) (*durable.NextTaskState[checkoutState, string], error) {
					if paid {
						return &durable.NextTaskState[checkoutState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeCompleted, Result: new("order placed")}}, nil
					}
					return &durable.NextTaskState[checkoutState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeFailed, Error: &durable.TaskOutcomeError{Message: "payment failed"}}}, nil
				})
			},
		},
		// Runs only after every payment is done, so the refunds have already happened.
		Abort: func(ctx context.Context, _ checkoutRunning, runtime checkoutRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, checkoutRunning) (*durable.NextTaskState[checkoutState, string], error) {
				return &durable.NextTaskState[checkoutState, string]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[string]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})

	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "checkout", Tasks: []durable.AnyTask{payment, checkoutTask}}))
	databasePath := filepath.Join(t.TempDir(), "session.sqlite")
	open := func() harness.Harness {
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: ai.CreateModels(), Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}
	checkout := func(root harness.Conversation, cards ...string) durable.TaskId {
		return commit(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, checkoutTask, checkoutInput{Cards: cards}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		})
	}
	outcomeOf := func(opened harness.Harness, id durable.TaskId) durable.TaskOutcomeStatus {
		t.Helper()
		settled, err := opened.WaitForTask(background, id)
		if err != nil || settled.State.Outcome == nil {
			t.Fatalf("task %d: %+v %v", id, settled.State, err)
		}
		return settled.State.Outcome.Status
	}
	decisions := func() [][]durable.TaskOutcomeStatus {
		bankMu.Lock()
		defer bankMu.Unlock()
		return slices.Clone(decided)
	}

	opened := open()
	root := must(opened.Root(background, nil))

	// One card is declined: the declined payment fails, failFast aborts the other three, and the checkout, itself
	// not aborted, decides "failed" once every payment is done.
	id := checkout(root, "visa-1", "expired-2", "visa-3", "visa-4")
	if got := outcomeOf(opened, id); got != durable.OutcomeFailed {
		t.Fatalf("declined checkout: %s", got)
	}
	expectEqual(t, "payment outcomes", decisions(), [][]durable.TaskOutcomeStatus{{durable.OutcomeAborted, durable.OutcomeFailed, durable.OutcomeAborted, durable.OutcomeAborted}})
	bankMu.Lock()
	expectEqual(t, "refunded cards", len(charged), 0)
	bankMu.Unlock()

	// The customer cancels: the graph shows the checkout waiting on its four payments, and aborting the checkout
	// aborts every payment (each refunds itself) before the checkout ends aborted.
	id = checkout(root, "visa-5", "visa-6", "visa-7", "visa-8")
	opened.Resume()
	graph := must(opened.TaskGraph(background))
	eventually(t, func() bool {
		nodes := graph.Value().Tasks
		node, ok := nodes[idKey(id)]
		if !ok || node.State.Status != durable.TaskWaiting || len(node.State.On) != 4 {
			return false
		}
		children := 0
		for _, candidate := range nodes {
			if candidate.Owner != nil && *candidate.Owner == id && candidate.Kind == "example.payment" {
				children++
			}
		}
		return children == 4
	})
	tree := graph.Value().Tasks
	if tree[idKey(id)].Owner != nil || tree[idKey(id)].State.Policy != durable.JoinFailFast {
		t.Fatalf("checkout node: %+v", tree[idKey(id)])
	}
	graph.Dispose()
	if _, err := opened.AbortTask(background, id); err != nil {
		t.Fatal(err)
	}
	if got := outcomeOf(opened, id); got != durable.OutcomeAborted {
		t.Fatalf("cancelled checkout: %s", got)
	}
	bankMu.Lock()
	expectEqual(t, "refunds after cancel", len(charged), 0)
	for _, card := range []string{"visa-5", "visa-6", "visa-7", "visa-8"} {
		if !slices.Contains(aborted, card) {
			t.Fatalf("payment %s was not aborted: %v", card, aborted)
		}
	}
	bankMu.Unlock()

	// The process stops while the payments run, and a new one continues.
	id = checkout(root, "visa-9", "visa-10", "visa-11", "visa-12")
	eventually(t, func() bool {
		bankMu.Lock()
		defer bankMu.Unlock()
		return charged["visa-9"] && charged["visa-10"] && charged["visa-11"] && charged["visa-12"]
	})
	closeSession(t, opened)
	opened = open()
	if got := outcomeOf(opened, id); got != durable.OutcomeCompleted {
		t.Fatalf("restarted checkout: %s", got)
	}
	last := decisions()[len(decisions())-1]
	expectEqual(t, "restarted payments", last, []durable.TaskOutcomeStatus{durable.OutcomeCompleted, durable.OutcomeCompleted, durable.OutcomeCompleted, durable.OutcomeCompleted})
	closeSession(t, opened)
}

func idKey(id durable.TaskId) string { return strconv.FormatInt(int64(id), 10) }

// 25-compaction.ts: a long trip-planning chat whose older messages are summarized so the model context stays small.
// Pi source: packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of Conversation.Compact fails it
// mutation-checked: dropping the reads and writes of FauxModelDefinition.ContextWindow fails it
func TestExample25Compaction(t *testing.T) {
	// A fake model with a tiny 3000-token window. It answers chat messages, writes summaries when asked to
	// summarize, and once rejects a request as too long, the way real providers report a context overflow.
	var mu sync.Mutex
	overflowOnce := false
	summaries := 0
	var hold chan struct{}    // while set, the next chat answer waits for it, which keeps the conversation busy
	var holding chan struct{} // closed when the held answer is requested: its turn has prepared
	// awaitSelected returns once every listed compaction has chosen its range. In Pi the faux answer arrives through a
	// chain of microtasks (faux.ts queueMicrotask), and a compaction started by the turn runs its select phase (reads and
	// one commit, also microtasks) before that chain ends, so select sees the context without the answer. Goroutines
	// give no such order: under load the answer could commit first and move the cut. The model states Pi's order.
	var awaitSelected func()
	userText := func(messages []ai.Message) string {
		for _, message := range slices.Backward(messages) {
			if user, ok := message.(ai.UserMessage); ok {
				if blocks, ok := user.Content.(ai.UserContentBlocks); ok && len(blocks) > 0 {
					if block, ok := blocks[0].(ai.TextContent); ok {
						return strings.ReplaceAll(block.Text, "\n", " ")
					}
				}
			}
		}
		return ""
	}
	respond := func(transcript ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		messages := transcript.Messages()
		if system, ok := messages[0].(ai.SystemMessage); ok {
			if content, ok := system.Content.(ai.SystemText); ok && strings.Contains(string(content), "summarization") {
				mu.Lock()
				summaries++
				n := summaries
				mu.Unlock()
				return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("## Goal\nPlan a week in Lisbon (summary #" + strconv.Itoa(n) + ").")}}.AssistantMessage(), nil
			}
		}
		awaitSelected()
		mu.Lock()
		if overflowOnce {
			overflowOnce = false
			mu.Unlock()
			return ai.FauxResponse{StopReason: "error", ErrorMessage: "prompt is too long"}.AssistantMessage(), nil
		}
		held, prepared := hold, holding
		hold = nil
		mu.Unlock()
		if held != nil {
			close(prepared)
			<-held
		}
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("A detailed answer to \"" + userText(messages) + "\": " + strings.Repeat("details ", 200))}}.AssistantMessage(), nil
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "tiny", ContextWindow: 3000, MaxTokens: 1000}}})
	steps := make([]ai.FauxResponseStep, 100)
	for i := range steps {
		steps[i] = ai.FauxFactoryStep(respond)
	}
	faux.SetResponses(steps)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())

	// Generation blocks to compact above 3000 - 1000 = 2000 tokens and starts a background compaction above
	// 2000 - 800. Settings are read at every use, so the getter makes backgroundTokens live.
	var backgroundTokens atomic.Int64
	backgroundTokens.Store(800)
	settings := func() *harness.HarnessSettings {
		return &harness.HarnessSettings{Compaction: &harness.CompactionPolicyPatch{ReserveTokens: new(1000), KeepRecentTokens: new(400), BackgroundTokens: new(int(backgroundTokens.Load()))}}
	}
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: harness.CreateRegistry(), Settings: settings})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "tiny"})}}))
	awaitSelected = func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			live, err := durable.Snapshot[harness.LiveState](background, opened, harness.LiveDoc, root.Id())
			if err != nil {
				t.Errorf("live snapshot: %v", err)
				return
			}
			selecting := false
			for _, running := range liveCompactions(live) {
				task, err := opened.GetTask(background, running.TaskId)
				if err != nil {
					t.Errorf("compaction task %d: %v", running.TaskId, err)
					return
				}
				if task.State.Outcome == nil && task.State.Checkpoint != nil {
					checkpoint := must(durable.FromJsonValue[struct{ Phase string }](*task.State.Checkpoint))
					selecting = selecting || checkpoint.Phase == "select"
				}
			}
			if !selecting {
				return
			}
			if time.Now().After(deadline) {
				t.Error("a compaction did not choose its range")
				return
			}
			time.Sleep(time.Millisecond)
		}
	}

	// head is the compaction reason at the head of the model context, "" when the context has no summary.
	head := func() (reason string, messages int, stored int) {
		view := must(root.Context(background, nil))
		page := must(root.Entries(background, durable.EntryQuery{}, 1000, nil))
		if view.Head != nil && durable.CompactionEntry.Is(view.Head) {
			data := must(durable.FromJsonValue[durable.CompactionEntryData](view.Head.Data))
			reason = string(data.Reason)
		}
		return reason, len(view.Messages), len(page.Items)
	}
	ask := func(question string) durable.SettledSubmissionRecord {
		t.Helper()
		submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(question)}))
		record, err := submission.Wait(background)
		if err != nil {
			t.Fatal(err)
		}
		// Let a background compaction started by this turn finish, so its summary shows below.
		live := must(durable.Snapshot[harness.LiveState](background, opened, harness.LiveDoc, root.Id()))
		if live != nil {
			for _, running := range live.Compactions {
				if _, err := opened.WaitForTask(background, running.TaskId); err != nil {
					t.Fatal(err)
				}
			}
		}
		return record
	}

	// 1. A long chat: once the context crosses the background threshold, a compaction runs while the chat goes on,
	// and its summary lands at once when the conversation is idle, otherwise at the next turn boundary.
	for _, question := range []string{"Where should we stay?", "What should we eat?", "Which day trips?", "Any museums?", "Nightlife?"} {
		if record := ask(question); record.Status != durable.SubmissionDone {
			t.Fatalf("%q: %+v", question, record)
		}
	}
	reason, messages, stored := head()
	mu.Lock()
	n := summaries
	mu.Unlock()
	// Pi Durable 1.0.4 running the script prints: threshold summary first, 5 messages in context, 14 entries stored.
	if n != 2 || reason != "threshold" || messages != 5 || stored != 14 {
		t.Fatalf("after the long chat: %d summaries, head %q, %d messages in context, %d entries stored", n, reason, messages, stored)
	}

	// 2. A manual compaction while an answer is still being written. The summary is ready first, waits in the
	// inbox, and is placed right after the answer.
	// In Pi the turn prepares while the manual compaction is still summarizing, so the turn finds it listed and starts
	// no background compaction. Here the manual one may finish before a descheduled turn prepares; with the background
	// threshold off until the turn has prepared, the turn starts none in either order, as in Pi.
	release := make(chan struct{})
	mu.Lock()
	hold, holding = release, make(chan struct{})
	prepared := holding
	mu.Unlock()
	backgroundTokens.Store(0)
	busy := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("How do we get around?")}))
	instructions := "Keep the hotel shortlist"
	manual := must(root.Compact(background, &instructions))
	<-prepared
	backgroundTokens.Store(800)
	settled, err := opened.WaitForTask(background, manual)
	if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("manual compaction: %+v %v", settled.State, err)
	}
	result := must(durable.FromJsonValue[harness.CompactionResult](*settled.State.Outcome.Result))
	if result.SubmissionId == nil {
		t.Fatalf("manual compaction result: %+v", result)
	}
	summary := must(opened.Submission(background, *result.SubmissionId))
	if status := must(summary.Status(background)).Status; status != durable.SubmissionQueued {
		t.Fatalf("the manual summary while the answer is held: %s", status)
	}
	close(release)
	if _, err := busy.Wait(background); err != nil {
		t.Fatal(err)
	}
	if final := must(summary.Wait(background)); final.Status != durable.SubmissionDone {
		t.Fatalf("the manual summary after the answer: %+v", final)
	}
	// Pi prints: manual summary first, 4 messages in context, 17 entries stored.
	if reason, messages, stored := head(); reason != "manual" || messages != 4 || stored != 17 {
		t.Fatalf("after compact(): head %q, %d messages in context, %d entries stored", reason, messages, stored)
	}

	// 3. The provider rejects a request as too long. Background compaction is turned off so the summary below is
	// the one the request itself forces. The context already estimates over the blocking threshold, so generation
	// compacts for the threshold first, and an overflow after that has no second compaction to make (a run compacts
	// at most once): the request ends model_error, as Pi's own run of this script prints.
	backgroundTokens.Store(0)
	ask("What should we pack?")
	if reason, messages, stored := head(); reason != "manual" || messages != 7 || stored != 20 {
		t.Fatalf("after the pack question: head %q, %d messages in context, %d entries stored", reason, messages, stored)
	}
	mu.Lock()
	overflowOnce = true
	mu.Unlock()
	record := ask("Summarize the plan for my partner")
	if record.Status != durable.SubmissionUnanswered || record.Reason == nil || *record.Reason != "model_error" {
		t.Fatalf("the overflowing request: %+v", record)
	}
	if reason, messages, stored := head(); reason != "threshold" || messages != 4 || stored != 24 {
		t.Fatalf("after the overflow: head %q, %d messages in context, %d entries stored", reason, messages, stored)
	}
	closeSession(t, opened)
}

// liveCompactions lists the compactions in a pi.live snapshot, none when the snapshot is absent.
func liveCompactions(live *harness.LiveState) []harness.CompactionStatus {
	if live == nil {
		return nil
	}
	return live.Compactions
}
