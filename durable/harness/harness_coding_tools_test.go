// Ports the cases of packages/durable/test/harness-tools.test.ts ("coding tools") and
// packages/durable/test/harness-tools-recovery.test.ts ("answers a real bash command interrupted by close and reopen")
// that run the real tools. durable/tools imports durable/harness, so these cases live in the external test package and
// build their own small chat setup from the exported API (test/chat-support.ts).

package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

var background = context.Background()

type codingSetup struct {
	faux     interface{ SetResponses([]ai.FauxResponseStep) }
	models   *ai.Models
	registry harness.Registry
}

func newCodingSetup() *codingSetup {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &codingSetup{faux: faux, models: models, registry: harness.CreateRegistry()}
}

func (setup *codingSetup) open(t *testing.T, store durable.Storage, environment env.ExecutionEnv) (harness.Harness, harness.Conversation) {
	t.Helper()
	options := harness.HarnessOptions{Models: setup.models, Registry: setup.registry}
	if environment != nil {
		options.Env = func(context.Context, harness.EnvTarget) (env.ExecutionEnv, error) { return environment, nil }
	}
	opened, err := harness.OpenHarness(background, store, options)
	if err != nil {
		t.Fatal(err)
	}
	root, err := opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	return opened, root
}

func toolCalls(name string, args map[string]any, id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, args, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
}

func answer(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}})
}

func submit(t *testing.T, conversation harness.Conversation, text string) durable.Submission {
	t.Helper()
	submission, err := conversation.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(text)})
	if err != nil {
		t.Fatal(err)
	}
	return submission
}

func entriesOf(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(background, durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]durable.EntryRecord, 0, len(page.Items))
	for _, v := range slices.Backward(page.Items) {
		entries = append(entries, v)
	}
	return entries
}

func resultText(message ai.ToolResultMessage) string {
	parts := make([]string, 0, len(message.Content))
	for _, item := range message.Content {
		switch typed := item.(type) {
		case ai.TextContent:
			parts = append(parts, typed.Text)
		case ai.ImageContent:
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "|")
}

func resultsOf(entries []durable.EntryRecord) []ai.ToolResultMessage {
	var results []ai.ToolResultMessage
	for index := range entries {
		if durable.ToolResultEntry.Is(&entries[index]) {
			results = append(results, entries[index].Model[0].(ai.ToolResultMessage))
		}
	}
	return results
}

func closeHarness(t *testing.T, opened harness.Harness) {
	t.Helper()
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestCodingTools(t *testing.T) {
	t.Run("answers a failing command with its retained tail and diagnostics in order", func(t *testing.T) {
		directory := t.TempDir()
		setup := newCodingSetup()
		if err := setup.registry.Install(new(durable.Extension{Name: "bash", Tools: []*durable.ToolRegistration{tools.CreateBashTool(nil)}})); err != nil {
			t.Fatal(err)
		}
		command := "i=1; while [ $i -le 3000 ]; do echo line-$i; i=$((i + 1)); done; exit 7"
		setup.faux.SetResponses([]ai.FauxResponseStep{toolCalls("bash", map[string]any{"command": command}, "b"), answer("done")})
		opened, root := setup.open(t, storage.NewMemoryStorage(), envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: directory}))
		if _, err := submit(t, root, "go").Wait(background); err != nil {
			t.Fatal(err)
		}
		var entry durable.EntryRecord
		for _, candidate := range entriesOf(t, root) {
			if candidate.Kind == "pi.tool-result" {
				entry = candidate
				break
			}
		}
		result := entry.Model[0].(ai.ToolResultMessage)
		if !result.IsError {
			t.Fatal("the result is not an error")
		}
		text := resultText(result)
		// Pi keeps the spilled full output for the user; the test removes its temporary directory.
		if _, rest, ok := strings.Cut(text, "[info] Full output: "); ok {
			spill, _, _ := strings.Cut(rest, "\n")
			t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(spill)) })
		}
		if !strings.HasPrefix(text, "line-1001\n") {
			t.Fatalf("text starts %q, want the retained tail from line-1001", text[:min(len(text), 20)])
		}
		codes := []string{}
		for _, diagnostic := range plainEntryObject(entry.Data)["diagnostics"].([]any) {
			code, _ := diagnostic.(map[string]any)["code"].(string)
			codes = append(codes, code)
		}
		if !reflect.DeepEqual(codes, []string{"full_output", "tool_error", "truncated"}) {
			t.Fatalf("diagnostic codes %v, want [full_output tool_error truncated]", codes)
		}
		if !strings.Contains(text, "line-3000\n|<harness>\n[info] Full output: ") {
			t.Fatalf("text lacks the full-output diagnostic: %q", text[max(0, len(text)-200):])
		}
		if !strings.Contains(text, "\n[error] Command exited with code 7\n[warn] Output truncated to its end: 1000 lines, ") {
			t.Fatalf("text lacks the exit and truncation diagnostics: %q", text[max(0, len(text)-200):])
		}
		closeHarness(t, opened)
	})

	t.Run("reads, edits, and runs a command in one run, then answers", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "notes.txt"), []byte("hello world\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		setup := newCodingSetup()
		if err := setup.registry.Install(new(durable.Extension{Name: "coding", Tools: []*durable.ToolRegistration{tools.CreateReadTool(), tools.CreateEditTool(), tools.CreateBashTool(nil)}})); err != nil {
			t.Fatal(err)
		}
		setup.faux.SetResponses([]ai.FauxResponseStep{
			toolCalls("read", map[string]any{"path": "notes.txt"}, "r"),
			toolCalls("edit", map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "world", "newText": "durable"}}}, "e"),
			toolCalls("bash", map[string]any{"command": "cat notes.txt"}, "b"),
			answer("done"),
		})
		opened, root := setup.open(t, storage.NewMemoryStorage(), envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: directory}))
		settled, err := submit(t, root, "go").Wait(background)
		if err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		entries := entriesOf(t, root)
		type summary struct {
			tool    string
			isError bool
			text    string
		}
		got := []summary{}
		for _, result := range resultsOf(entries) {
			got = append(got, summary{result.ToolName, result.IsError, resultText(result)})
		}
		want := []summary{{"read", false, "hello world\n"}, {"edit", false, "Successfully replaced 1 block(s) in notes.txt."}, {"bash", false, "hello durable\n"}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("results %+v, want %+v", got, want)
		}
		if last := entries[len(entries)-1].Kind; last != "pi.assistant" {
			t.Fatalf("last entry %s, want pi.assistant", last)
		}
		if content, err := os.ReadFile(filepath.Join(directory, "notes.txt")); err != nil || string(content) != "hello durable\n" {
			t.Fatalf("file %q %v, want hello durable", content, err)
		}
		closeHarness(t, opened)
	})
}

func TestToolRecoveryWithBash(t *testing.T) {
	t.Run("answers a real bash command interrupted by close and reopen, then finishes the run", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		environment := envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: filepath.Dir(path)})
		setup := newCodingSetup()
		if err := setup.registry.Install(new(durable.Extension{Name: "bash", Tools: []*durable.ToolRegistration{tools.CreateBashTool(nil)}})); err != nil {
			t.Fatal(err)
		}
		setup.faux.SetResponses([]ai.FauxResponseStep{toolCalls("bash", map[string]any{"command": "echo started; sleep 30"}, "b"), answer("done")})
		open := func() (harness.Harness, harness.Conversation) {
			store, err := sqlitenode.OpenNodeSqliteStorage(path, sqlitenode.NodeSqliteStorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			opened, root := setup.open(t, store, environment)
			opened.Resume()
			return opened, root
		}
		opened, root := open()
		id := submit(t, root, "go").Id()
		deadline := time.Now().Add(10 * time.Second)
		for {
			live, err := opened.SnapshotErased(background, harness.LiveDoc, root.Id())
			if err != nil {
				t.Fatal(err)
			}
			if slots, _ := live.Value("tools").([]any); len(slots) > 0 && slots[0].(*delta.JsonObject).Value("output") == "started\n" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the bash output never became durable")
			}
			time.Sleep(5 * time.Millisecond)
		}
		closeHarness(t, opened)

		opened, root = open()
		submission, err := opened.Submission(background, id)
		if err != nil || submission == nil {
			t.Fatalf("Submission %v %v", submission, err)
		}
		if settled, err := submission.Wait(background); err != nil || settled.Status != durable.SubmissionDone {
			t.Fatalf("input %+v %v, want done", settled, err)
		}
		result := resultsOf(entriesOf(t, root))[0]
		if want := "started\n|<harness>\n[error] Tool bash was interrupted and may have partially run\n</harness>"; resultText(result) != want {
			t.Fatalf("text %q, want %q", resultText(result), want)
		}
		closeHarness(t, opened)
	})
}

// plainEntryObject is value's JSON object as a map, for reads that ignore key order, as toEqual does.
func plainEntryObject(value any) map[string]any {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		panic(err)
	}
	return object
}
