// Ports packages/durable/test/examples/18-print.ts and 19-json.ts.

package examples_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
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
		Tools: []*durable.ToolRegistration{tools.CreateReadTool(), tools.CreateBashTool(nil)},
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

// 18-print.ts: ask one question and print the final answer.
func TestExample18Print(t *testing.T) {
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

// 19-json.ts: the same coding turn over each storage the script offers (sqlite, jsonl, memory). Not ported: the
// script's two output modes. `--ops` prints the conversation watch (Conversation.Watch, which durable/harness still
// stubs: view.go waits on durable/session observation exports) and the default mode prints watchEvents
// (harness/events.ts, not in durable/harness yet). The OPENAI_API_KEY path needs a live key.
func TestExample19JsonStorages(t *testing.T) {
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
