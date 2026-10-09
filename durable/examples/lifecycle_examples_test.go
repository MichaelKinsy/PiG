// Ports packages/durable/test/examples/21-late-join.ts, 22-subagent-foreground.ts, 29-sandbox-per-conversation.ts and
// 31-reload-and-restart.ts. Upstream's scripts pace themselves with timers and print; each Go example gates the work
// it observes and asserts what the script prints.

package examples_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

var emptyParameters = map[string]any{"type": "object", "properties": map[string]any{}}

// textResult is `{ content: [{ type: "text", text }] }`.
func textResult(text string) durable.ToolExecutionResult {
	return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
}

// assistantText is the text of an assistant message.
func assistantText(message ai.AssistantMessage) string {
	var text strings.Builder
	for _, block := range message.Content {
		if block, ok := block.(ai.TextContent); ok {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

// liveState is the view's pi.live document.
func liveState(t *testing.T, view harness.ConversationView) harness.LiveState {
	t.Helper()
	raw, ok := view.Docs.Get("pi.live")
	if !ok {
		return harness.LiveState{}
	}
	return must(durable.FromJsonValue[harness.LiveState](raw))
}

// 21-late-join.ts: a client attaches while a run is already underway. It gets the current state first, the
// conversation view or the snapshot event, and then only what changes after that.
// Pi source: packages/durable/src/harness/events.ts, packages/durable/src/harness/types.ts
// mutation-checked: zeroing the results of AgentEventStream.Start, Conversation.ViewState fails it
// mutation-checked: dropping the reads and writes of AgentEventStream.Snapshot fails it
func TestExample21LateJoin(t *testing.T) {
	// The tool prints "1\n" to "5\n", waits at the gate (upstream's 500 ms pause is the join), then prints "6\n"
	// to "10\n".
	joined := make(chan struct{})
	var emitted sync.Mutex
	registry := harness.CreateRegistry()
	installed(t, registry, new(durable.Extension{Name: "count", Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "count", Description: "Counts to ten", Parameters: emptyParameters},
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			for n := 1; n <= 10; n++ {
				if n == 6 {
					select {
					case <-joined:
					case <-ctx.Done():
						return durable.ToolExecutionResult{}, ctx.Err()
					}
				}
				emitted.Lock()
				api.Output(fmt.Sprintf("%d\n", n))
				emitted.Unlock()
			}
			return durable.ToolExecutionResult{}, nil
		},
	})}}))
	faux := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 40, TokenSize: &ai.FauxTokenSize{Min: new(1), Max: new(1)}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		fauxToolTurn("count", map[string]any{}, "call-1"),
		fauxAnswer("Counted to ten, and this answer streams slowly."),
	})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Count to ten, then tell me.")}))

	// Join while the tool is halfway through: the view holds the running tool's output.
	view := must(root.ViewState(background))
	var slot harness.ToolSlot
	eventually(t, func() bool {
		tools := liveState(t, view.Value()).Tools
		if len(tools) == 1 && tools[0].Status == harness.ToolSlotRunning && tools[0].Output != nil && strings.HasPrefix(*tools[0].Output, "1\n2\n3\n4\n5\n") {
			slot = tools[0]
			return true
		}
		return false
	})
	if slot.Name != "count" {
		t.Fatalf("view tool slot: %+v", slot)
	}
	var kinds []string
	for _, entry := range view.Value().Entries {
		kinds = append(kinds, entry.Kind)
	}
	expectEqual(t, "view entries", kinds, []string{"pi.user", "pi.system", "pi.assistant"})

	var viewOutputs []string
	var viewMu sync.Mutex
	unsubscribe, err := view.Subscribe(func(value harness.ConversationView, _ context.Context, _ chord.ReplicatedStateDelivery) {
		if tools := liveState(t, value).Tools; len(tools) == 1 && tools[0].Status == harness.ToolSlotRunning && tools[0].Output != nil {
			viewMu.Lock()
			viewOutputs = append(viewOutputs, *tools[0].Output)
			viewMu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Event client: the snapshot event carries the same state; later events apply on top of it.
	stream := must(harness.WatchEvents(background, opened, root.Id()))
	if len(stream.Snapshot.Tools) != 1 || stream.Snapshot.Tools[0].Name != "count" || stream.Snapshot.Tools[0].Status != harness.ToolSlotRunning {
		t.Fatalf("snapshot tools: %+v", stream.Snapshot.Tools)
	}
	output := ""
	if stream.Snapshot.Tools[0].Output != nil {
		output = *stream.Snapshot.Tools[0].Output
	}
	snapshotOutput := output
	var mu sync.Mutex
	var seen []string
	var textDeltas, answers []string
	stream.Start(func(_ context.Context, events []harness.AgentEvent) error {
		mu.Lock()
		defer mu.Unlock()
		for _, event := range events {
			seen = append(seen, event.EventType())
			switch event := event.(type) {
			case harness.ToolExecutionUpdateEvent:
				if event.Output == nil {
					continue
				}
				if event.Output.Set != nil {
					output = *event.Output.Set
					continue
				}
				if event.Output.TrimStart != nil {
					output = output[min(*event.Output.TrimStart, len(output)):]
				}
				if event.Output.Append != nil {
					output += *event.Output.Append
				}
			case harness.MessageUpdateEvent:
				for _, change := range event.Changes {
					if change.Type == "text_delta" {
						textDeltas = append(textDeltas, change.Delta)
					}
				}
			case harness.MessageEndEvent:
				if event.Entry.Kind == "pi.assistant" {
					if text := assistantText(event.Entry.Model[0].(ai.AssistantMessage)); text != "" {
						answers = append(answers, text)
					}
				}
			}
		}
		return nil
	})

	close(joined)
	if _, err := submission.Wait(background); err != nil {
		t.Fatal(err)
	}
	if err := opened.WaitForIdle(background); err != nil {
		t.Fatal(err)
	}
	// Event callbacks run after their commit: the answer's message end is awaited before the stream stops.
	eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(answers) == 1
	})
	if _, err := stream.Stop(); err != nil {
		t.Fatal(err)
	}
	unsubscribe()
	view.Dispose()

	mu.Lock()
	defer mu.Unlock()
	// The events after the snapshot carry only the rest: the output grew from the snapshot's, never restarted.
	const everything = "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	if !strings.HasPrefix(output, snapshotOutput) || !strings.HasPrefix(everything, output) {
		t.Fatalf("event output = %q after snapshot %q", output, snapshotOutput)
	}
	// The answer streams after the join: its deltas are a contiguous piece of the answer the message end commits.
	expectEqual(t, "committed answers", answers, []string{"Counted to ten, and this answer streams slowly."})
	if got := strings.Join(textDeltas, ""); got == "" || !strings.Contains(answers[0], got) {
		t.Fatalf("event text deltas = %q of %q", got, answers[0])
	}
	for _, kind := range seen {
		if kind == "snapshot" {
			t.Fatalf("a snapshot event after attachment: %v", seen)
		}
	}
	viewMu.Lock()
	defer viewMu.Unlock()
	for _, shown := range viewOutputs {
		if !strings.HasPrefix(shown, "1\n2\n3\n4\n5\n") {
			t.Fatalf("view output %q restarted", shown)
		}
	}
	closeSession(t, opened)
}

// subagentExtension is 22-subagent-foreground.ts's tool: the parent's tool call creates a child conversation it
// owns, runs one task there, and returns the child's answer.
func subagentExtension() *durable.Extension {
	var subagent *durable.Extension
	subagent = new(durable.Extension{Name: "subagent", Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{
			Name:        "subagent",
			Description: "Delegate a self-contained task to a subagent and get its answer back.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{"task": map[string]any{"type": "string", "description": "What the subagent should do"}},
				"required":   []any{"task"},
			},
		},
		// Safe to rerun after a crash: a rerun finds the child it already created and the submission it already made.
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			task, _ := args.(map[string]any)["task"].(string)
			// The child is owned by this tool call's task, so aborting the call aborts the child, and the call
			// finishes only once the child's work is done.
			created, err := api.Commit(ctx, func(tx durable.Tx) (any, error) {
				taskId := api.TaskId()
				existing, err := tx.ScanConversations(durable.ConversationQuery{OwnerTaskId: &taskId}, 1, nil)
				if err != nil {
					return nil, err
				}
				if len(existing.Items) > 0 {
					return existing.Items[0].Id, nil
				}
				child, err := tx.CreateConversation(durable.CreateConversationOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnedByTask, TaskId: taskId}})
				if err != nil {
					return nil, err
				}
				// Without this extension, the child is not offered this tool.
				return child.Id, harness.Configure(tx, child.Id, harness.AgentChange{Extensions: harness.SetTo(harness.ExtensionChange{Remove: []*durable.Extension{subagent}})})
			})
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			child := created.(durable.ConversationId)
			// A UI watching the parent sees this and can attach to the child.
			if err := api.Details(ctx, delta.JsonObjectOf("conversationId", float64(child))); err != nil {
				return durable.ToolExecutionResult{}, err
			}
			handle, err := api.Conversation(ctx, child)
			if err != nil || handle == nil {
				return durable.ToolExecutionResult{}, fmt.Errorf("child conversation: %w", err)
			}
			// The request ID makes a rerun get back the submission it made before the crash.
			request := durable.InputSubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(task), RequestId: new(fmt.Sprintf("subagent:%d", api.TaskId()))}
			submission, err := handle.Submit(ctx, request)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			settled, err := submission.Wait(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if settled.Status != durable.SubmissionDone || settled.Type != durable.SubmissionTypeInput {
				return durable.ToolExecutionResult{}, fmt.Errorf("Subagent failed: %s", settled.Status)
			}
			entry, err := api.Commit(ctx, func(tx durable.Tx) (any, error) { return tx.Entry(*settled.Answer) })
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			answer := entry.(*durable.EntryRecord)
			return durable.ToolExecutionResult{
				Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: assistantText(answer.Model[0].(ai.AssistantMessage))}},
				Details:    delta.JsonObjectOf("conversationId", float64(child)),
				HasDetails: true,
			}, nil
		},
	})}})
	return subagent
}

// 22-subagent-foreground.ts: the parent delegates, the child answers, and the parent reports. A UI finds the child
// through the tool's running details and shows its events under the call.
func TestExample22SubagentForeground(t *testing.T) {
	registry := harness.CreateRegistry()
	installed(t, registry, subagentExtension())
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: fauxModels(
		fauxToolTurn("subagent", map[string]any{"task": "Name three prime numbers."}, "call-1"),
		fauxAnswer("2, 3, and 5."),
		fauxAnswer("The subagent says: 2, 3, and 5."),
	), Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))

	// The UI: the parent's events, with each subagent's events under its call.
	var mu sync.Mutex
	lines := map[durable.ConversationId][]string{}
	attached := map[durable.ConversationId]bool{}
	var streams []*harness.AgentEventStream
	var attach func(id durable.ConversationId)
	attach = func(id durable.ConversationId) {
		mu.Lock()
		attached[id] = true
		mu.Unlock()
		stream := must(harness.WatchEvents(background, opened, id))
		mu.Lock()
		streams = append(streams, stream)
		mu.Unlock()
		stream.Start(func(_ context.Context, events []harness.AgentEvent) error {
			for _, event := range events {
				switch event := event.(type) {
				case harness.MessageEndEvent:
					if event.Entry.Kind == "pi.assistant" {
						if text := assistantText(event.Entry.Model[0].(ai.AssistantMessage)); text != "" {
							mu.Lock()
							lines[id] = append(lines[id], "assistant: "+text)
							mu.Unlock()
						}
					}
				case harness.ToolExecutionStartEvent:
					mu.Lock()
					lines[id] = append(lines[id], "tool "+event.ToolName)
					mu.Unlock()
				case harness.ToolExecutionUpdateEvent:
					if !event.HasDetails || event.Details == nil {
						continue
					}
					details, _ := event.Details.(*delta.JsonObject)
					child, ok := details.Value("conversationId").(float64)
					mu.Lock()
					known := attached[durable.ConversationId(child)]
					mu.Unlock()
					if ok && !known {
						attach(durable.ConversationId(child))
					}
				}
			}
			return nil
		})
	}
	attach(root.Id())

	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Use the subagent tool to find three prime numbers, then tell me what it said.")}))
	if _, err := submission.Wait(background); err != nil {
		t.Fatal(err)
	}
	if err := opened.WaitForIdle(background); err != nil {
		t.Fatal(err)
	}
	// Event callbacks run after their commit, so the last ones are awaited before the streams stop.
	eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(lines[root.Id()]) == 2 && len(attached) == 2 && func() bool {
			for id, got := range lines {
				if id != root.Id() && len(got) == 1 {
					return true
				}
			}
			return false
		}()
	})
	mu.Lock()
	all := append([]*harness.AgentEventStream(nil), streams...)
	mu.Unlock()
	for _, stream := range all {
		if _, err := stream.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attached) != 2 {
		t.Fatalf("attached conversations = %v, want the parent and one child", attached)
	}
	expectEqual(t, "parent events", lines[root.Id()], []string{"tool subagent", "assistant: The subagent says: 2, 3, and 5."})
	for id, got := range lines {
		if id != root.Id() {
			expectEqual(t, "child events", got, []string{"assistant: 2, 3, and 5."})
		}
	}
	// The child is owned by the tool call's task.
	for id := range attached {
		if id == root.Id() {
			continue
		}
		record := commit(t, root, func(tx durable.Tx) (*durable.ConversationRecord, error) { return tx.Conversation(id) })
		if record == nil || record.Owner == nil {
			t.Fatalf("child record = %+v, want owned by a task", record)
		}
	}
	closeSession(t, opened)
}

type sandbox struct {
	Path string `json:"path,omitempty"`
}

// 29-sandbox-per-conversation.ts: the app records each conversation's sandbox in its own document, and the Harness
// environment function looks it up for every tool call. A sandbox is a directory.
func TestExample29SandboxPerConversation(t *testing.T) {
	// `fork: "initial"`: a fork gets no sandbox until the app assigns one.
	sandboxDoc := durable.DefineDoc(durable.DocDefinition[sandbox]{
		CommonDocDefinition: durable.CommonDocDefinition[sandbox]{Kind: "app.sandbox", Version: 1, Initial: func() sandbox { return sandbox{} }},
		DocumentSemantics:   durable.DocumentSemantics{Scope: durable.ScopeConversation, History: durable.HistoryLatest, Fork: durable.ForkInitial},
	})
	note := func(text string) ai.FauxResponseStep {
		return fauxToolTurn("write", map[string]any{"path": "note.txt", "content": text}, "")
	}
	models := fauxModels(note("from alice"), fauxAnswer("Saved."), note("from bob"), fauxAnswer("Saved."))
	registry := harness.CreateRegistry()
	installed(t, registry, tools.CodingTools)
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models:   models,
		Registry: registry,
		// Committed reads only; a conversation without a sandbox gets no environment, so its tools fail cleanly.
		Env: func(ctx context.Context, target harness.EnvTarget) (env.ExecutionEnv, error) {
			current, err := durable.Snapshot[sandbox](ctx, target.Read, sandboxDoc, target.ConversationId)
			if err != nil || current == nil || current.Path == "" {
				return nil, err
			}
			return envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: current.Path}), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Each user's conversation gets a fresh sandbox in the creating commit.
	conversationFor := func(path string) harness.Conversation {
		return must(opened.CreateConversation(background, harness.ConversationCreateOptions{
			Ownership: ownerless.Ownership,
			Agent:     &harness.AgentChange{Model: harness.SetTo(fauxModel)},
			Init: func(tx durable.Tx, id durable.ConversationId) error {
				draft, err := durable.TxDoc[sandbox](tx, sandboxDoc, id)
				if err != nil {
					return err
				}
				return draft.Set("path", path)
			},
		}))
	}
	alicePath, bobPath := t.TempDir(), t.TempDir()
	alice, bob := conversationFor(alicePath), conversationFor(bobPath)
	say(t, alice, "Leave a note.")
	say(t, bob, "Leave a note.")
	for path, want := range map[string]string{alicePath: "from alice", bobPath: "from bob"} {
		got, err := os.ReadFile(filepath.Join(path, "note.txt"))
		if err != nil || string(got) != want {
			t.Fatalf("sandbox %s note = %q, %v; want %q", path, got, err, want)
		}
	}
	closeSession(t, opened)
}

// 31-reload-and-restart.ts: reload an extension while a call runs, then restart the process. Running work finishes
// on the code it started with; stored choices are names, so they survive a restart and bind to whatever code the new
// process installs.
func TestExample31ReloadAndRestart(t *testing.T) {
	// Stand-in for code loaded from disk: each call builds the extension as the file currently reads.
	started := make(chan struct{}, 4)
	var gateMu sync.Mutex
	gate := make(chan struct{})
	close(gate)
	currentGate := func() chan struct{} {
		gateMu.Lock()
		defer gateMu.Unlock()
		return gate
	}
	loadVersioned := func(version string) *durable.Extension {
		return new(durable.Extension{Name: "versioned", Tools: []*durable.ToolRegistration{new(durable.ToolRegistration{
			ToolSchema: ai.ToolSchema{Name: "version", Description: "Report the tool's code version", Parameters: emptyParameters},
			Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				held := currentGate()
				started <- struct{}{}
				select {
				case <-held:
				case <-ctx.Done():
					return durable.ToolExecutionResult{}, ctx.Err()
				}
				return textResult(version), nil
			},
		})}})
	}
	callVersion := func() ai.FauxResponseStep { return fauxToolTurn("version", map[string]any{}, "") }
	models := fauxModels(callVersion(), fauxAnswer("Done."), callVersion(), fauxAnswer("Done."))
	databasePath := filepath.Join(t.TempDir(), "session.sqlite")
	open := func(registry harness.Registry) harness.Harness {
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{Models: models, Registry: registry})
		if err != nil {
			t.Fatal(err)
		}
		return opened
	}

	// First process.
	registry := harness.CreateRegistry()
	installed(t, registry, loadVersioned("v1"))
	opened := open(registry)
	// Selected by name. The name is what is stored, never the code.
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{
		Model:      harness.SetTo(fauxModel),
		Extensions: harness.SetTo(harness.ExtensionChange{Exact: true, List: []*durable.Extension{loadVersioned("v1")}}),
	}}))
	lastResult := func(root harness.Conversation) string {
		t.Helper()
		page := must(root.Entries(background, durable.EntryQuery{}, 10, nil))
		for _, entry := range page.Items {
			if durable.ToolResultEntry.Is(&entry) {
				var text strings.Builder
				for _, part := range entry.Model[0].(ai.ToolResultMessage).Content {
					if part, ok := part.(ai.TextContent); ok {
						text.WriteString(part.Text)
					}
				}
				return text.String()
			}
		}
		t.Fatal("no tool result")
		return ""
	}

	// The file changes while a call runs: the running call finishes on v1, the next call uses v2.
	release := make(chan struct{})
	gateMu.Lock()
	gate = release
	gateMu.Unlock()
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Which version?")}))
	<-started
	installed(t, registry, loadVersioned("v2"))
	close(release)
	if _, err := submission.Wait(background); err != nil {
		t.Fatal(err)
	}
	if got := lastResult(root); got != "v1" {
		t.Fatalf("call running during the reload: %q", got)
	}
	say(t, root, "And now?")
	if got := lastResult(root); got != "v2" {
		t.Fatalf("next call: %q", got)
	}
	closeSession(t, opened)

	// Second process: the conversation still selects "versioned", but this process has not installed it yet.
	registry = harness.CreateRegistry()
	opened = open(registry)
	root = must(opened.Root(background, nil))
	expectEqual(t, "after restart, before install", toolNames(t, root), []string{})
	installed(t, registry, loadVersioned("v3"))
	expectEqual(t, "after install", toolNames(t, root), []string{"version"})
	closeSession(t, opened)
}

// 16-real-model.ts: stream an answer and watch it arrive through the conversation's pi.live document. While the
// answer streams, generation commits throttled partials to pi.live; the watch sees only committed values. Upstream
// streams from OpenAI when OPENAI_API_KEY is set and prints "skipped" otherwise; the OpenAI provider is outside
// durable, so this example drives the same Harness path with a slowly streaming faux model.
// Pi source: packages/durable/src/types.ts
// mutation-checked: zeroing the results of WatchHandle.Start fails it
func TestExample16RealModel(t *testing.T) {
	registry := harness.CreateRegistry()
	untagged := false
	installed(t, registry, new(durable.Extension{Name: "concise", Sections: []*durable.PromptSection{
		harness.Section("preamble", func(context.Context, durable.PromptInput) (*string, error) {
			return new("You are a concise assistant."), nil
		}, harness.SectionOptions{Tag: &untagged}),
	}}))
	poem := strings.Repeat("Roses are red, violets are blue. ", 12)
	faux := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 400, TokenSize: &ai.FauxTokenSize{Min: new(2), Max: new(6)}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{fauxAnswer(poem)})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel), ThinkingLevel: harness.SetTo(ai.ModelThinkingLevel("high"))}}))
	liveWatch := must(durable.WatchDoc[harness.LiveState](background, opened, harness.LiveDoc, root.Id()))
	if liveWatch == nil {
		t.Fatal("no pi.live document")
	}
	// Print only what each committed partial adds to the text printed so far.
	var mu sync.Mutex
	printed := ""
	var increments []string
	printText := func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if len(text) <= len(printed) || !strings.HasPrefix(text, printed) {
			return
		}
		increments = append(increments, text[len(printed):])
		printed = text
	}
	liveWatch.Start(func(_ context.Context, value *harness.LiveState, _ []durable.Op) error {
		if value == nil || value.Generation == nil {
			return nil
		}
		if value.Generation.Message == nil {
			return nil // no partial yet
		}
		if text := assistantText(*value.Generation.Message); text != "" {
			printText(text)
		}
		return nil
	})
	opened.Resume()
	settled := say(t, root, "Write a long poem")
	if _, err := liveWatch.Stop(); err != nil {
		t.Fatal(err)
	}
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("unanswered: %v %v", settled.Reason, settled.Detail)
	}
	// The last throttle window may not have been committed as a partial; the answer entry has the rest.
	printText(answerText(t, root, settled))
	mu.Lock()
	defer mu.Unlock()
	if printed != poem {
		t.Fatalf("printed %q, want the poem", printed)
	}
	// The answer arrived in more than one committed piece: the watch streamed it.
	if len(increments) < 2 {
		t.Fatalf("the watch saw the answer in %d piece(s): %q", len(increments), increments)
	}
	closeSession(t, opened)
}
