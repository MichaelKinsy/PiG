// Ports packages/durable/test/examples/17-print.ts and 18-json.ts.

package examples_test

import (
	"context"
	"encoding/json"
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
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
	jsonlnode "github.com/MichaelKinsy/PiG/durable/storage/jsonl/node"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// codingAssistant opens a Harness whose model lists the directory with bash and then answers, as the examples'
// OPENAI_API_KEY-less path does. The live OpenAI path of the scripts is not ported.
func codingAssistant(t *testing.T, store durable.Storage, cwd string) (harness.Harness, harness.Conversation) {
	t.Helper()
	models := fauxModels(
		fauxToolTurn("bash", map[string]any{"command": "ls"}, "call-1"),
		fauxAnswer("This directory holds the durable package sources, tests, and docs."),
	)
	registry := harness.CreateRegistry()
	untagged := false
	installed(t, registry, new(durable.Extension{
		Name:  "coding",
		Tools: []*durable.ToolRegistration{tools.CreateReadTool(nil), tools.CreateBashTool(nil)},
		Sections: []*durable.PromptSection{{Key: "preamble", Tag: &untagged, Render: func(context.Context, durable.PromptInput) (*string, error) {
			text := "You are a concise coding assistant."
			return &text, nil
		}}},
	}))
	executionEnv := envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd})
	opened, err := harness.OpenHarness(background, store, harness.HarnessOptions{
		Models:   models,
		Registry: registry,
		Env:      func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) { return executionEnv, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	return opened, root
}

// 17-print.ts: ask one question and print the final answer.
func TestExample17Print(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("docs"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened, root := codingAssistant(t, storage.NewMemoryStorage(), directory)
	settled := say(t, root, "What is in this directory?")
	if got := answerText(t, root, settled); got != "This directory holds the durable package sources, tests, and docs." {
		t.Fatalf("answer: %q", got)
	}
	// The bash call listed the directory the environment points at.
	page := must(root.Entries(background, durable.EntryQuery{}, 20, nil))
	listed := false
	for _, entry := range page.Items {
		if durable.ToolResultEntry.Is(&entry) {
			result := entry.Model[0].(ai.ToolResultMessage)
			listed = strings.Contains(result.Content[0].(ai.TextContent).Text, "README.md")
		}
	}
	if !listed {
		t.Fatal("the bash tool result does not list the directory")
	}
	closeSession(t, opened)
}

// 18-json.ts: the same coding turn over each storage the script offers (sqlite, jsonl, memory). The script's two output modes are
// TestExample18JsonOps and TestExample18JsonState. The OPENAI_API_KEY path needs a live key.
func TestExample18JsonStorages(t *testing.T) {
	stores := map[string]func(t *testing.T) durable.Storage{
		"memory": func(*testing.T) durable.Storage { return storage.NewMemoryStorage() },
		"jsonl": func(t *testing.T) durable.Storage {
			store, err := jsonlnode.OpenNodeJsonlStorage(background, filepath.Join(t.TempDir(), "jsonl"), jsonl.JsonlStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return store
		},
		"sqlite": func(t *testing.T) durable.Storage {
			store, err := sqlitenode.OpenNodeSqliteStorage(filepath.Join(t.TempDir(), "session.sqlite"), sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			return store
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			opened, root := codingAssistant(t, open(t), t.TempDir())
			settled := say(t, root, "What is in this directory?")
			if got := answerText(t, root, settled); got != "This directory holds the durable package sources, tests, and docs." {
				t.Fatalf("answer: %q", got)
			}
			if err := opened.WaitForIdle(background); err != nil {
				t.Fatal(err)
			}
			expectEqual(t, "transcript", transcriptKinds(t, root), []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"})
			closeSession(t, opened)
		})
	}
}

// viewLines are the lines a JSON-mode run of 18-json.ts prints: the watch's callbacks, serialized, in delivery order.
type viewLines struct {
	mu     sync.Mutex
	values []harness.ConversationView
	ops    [][]durable.Op
}

func (lines *viewLines) add(change chord.WatchChange[harness.ConversationView]) {
	lines.mu.Lock()
	defer lines.mu.Unlock()
	lines.values = append(lines.values, change.Value)
	lines.ops = append(lines.ops, change.Ops)
}

// runJson attaches before submitting, so the watch covers the whole run, runs the prompt and waits for the last value of the run.
func runJson(t *testing.T) (initial harness.ConversationView, lines *viewLines) {
	t.Helper()
	opened, root := codingAssistant(t, storage.NewMemoryStorage(), t.TempDir())
	watch := must(root.Watch(background, nil))
	initial = watch.Value()
	lines = &viewLines{}
	watch.Start(func(_ context.Context, change chord.WatchChange[harness.ConversationView]) error {
		lines.add(change)
		return nil
	})
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("What is in this directory?")}))
	must(submission.Wait(background))
	if err := opened.WaitForIdle(background); err != nil {
		t.Fatal(err)
	}
	// Let the last value reach the listener before stopping.
	eventually(t, func() bool {
		lines.mu.Lock()
		defer lines.mu.Unlock()
		if len(lines.values) == 0 {
			return false
		}
		last := lines.values[len(lines.values)-1]
		return liveState(t, last).Run == nil && len(last.Entries) == 5
	})
	must(watch.Stop())
	closeSession(t, opened)
	return initial, lines
}

// 18-json.ts --ops (the default): the raw frames, starting with the view itself; each later line holds the ops of the commits since.
// Applying the lines' ops to the first line's view gives each value the watch delivered.
func TestExample18JsonOps(t *testing.T) {
	initial, lines := runJson(t)
	value := orderedViewJSON(t, initial)
	for index, ops := range lines.ops {
		converted := make([]durable.Op, len(ops))
		for position, op := range orderedViewJSON(t, ops).([]any) {
			converted[position] = op.([]any)
		}
		next, err := delta.ApplyImmutable(value, converted)
		if err != nil {
			t.Fatal(err)
		}
		value = next
		if got, want := viewJSON(t, value), viewJSON(t, lines.values[index]); got != want {
			t.Fatalf("line %d: the ops give %s, the watch delivered %s", index, got, want)
		}
	}
	if len(lines.ops) == 0 {
		t.Fatal("the run printed no ops line")
	}
}

// 18-json.ts --state: per call, the entries appended since the previous one and the current `pi.live`, state a renderer draws.
func TestExample18JsonState(t *testing.T) {
	initial, lines := runJson(t)
	newest := durable.EntryId(0)
	if len(initial.Entries) > 0 {
		newest = initial.Entries[len(initial.Entries)-1].Id
	}
	var appended []string
	for _, value := range lines.values {
		for _, entry := range value.Entries {
			if entry.Id > newest {
				appended = append(appended, entry.Kind)
			}
		}
		if len(value.Entries) > 0 {
			newest = value.Entries[len(value.Entries)-1].Id
		}
	}
	expectEqual(t, "entries printed", appended, []string{"pi.user", "pi.system", "pi.assistant", "pi.tool-result", "pi.assistant"})
	last := lines.values[len(lines.values)-1]
	if live := liveState(t, last); live.Run != nil || live.RunFailure != nil || live.Generation != nil {
		t.Fatalf("pi.live after the run = %+v, want nothing", live)
	}
}

func orderedViewJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := delta.DecodeJson(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func viewJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}
