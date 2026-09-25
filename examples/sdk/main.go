// Command sdk-example is a minimal demonstration of embedding the
// pig coding agent as a Go library via the public `coding` package.
//
// What it does:
//
//  1. Constructs a coding.Services dependency container pointed at
//     a temporary working directory.
//  2. Builds a coding.Runtime over that container (no extensions, no
//     custom tools).
//  3. Creates a fresh coding.Session and sends a single user prompt.
//  4. Prints the assistant's reply.
//
// Prerequisites:
//
//   - You must have authed at least once with the standalone pig
//     binary so a valid auth.json exists at $PIG_HOME/agent/auth.json
//     (or ~/.pig/agent/auth.json on macOS/Linux).
//   - The model "github-copilot/gpt-4o-mini" must be available in
//     your environment. Adjust the modelID const below to use a
//     different provider/model combo.
//
// Run from the repo root:
//
//	go run ./examples/sdk
//
// Library consumers writing their own integrations can use this
// program as a starting template. See coding/doc.go for the full
// public API reference.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

const (
	// Adjust to taste; must be a model id known to the coding
	// agent's model registry.
	modelID = "github-copilot/gpt-4o-mini"
)

func main() {
	// 1. Working directory: where this session's JSONL transcript will
	//    live (./.pig/sessions/<id>.jsonl). Use a tempdir so the
	//    example doesn't litter your repo.
	cwd, err := os.MkdirTemp("", "pig-sdk-example-")
	if err != nil {
		log.Fatalf("mkdir cwd: %v", err)
	}
	defer func() { _ = os.RemoveAll(cwd) }()

	// 2. Auth directory: where the persistent auth.json lives. Default
	//    is ~/.pig/agent (or $PIG_HOME/agent).
	agentDir := coding.DefaultAgentDir()

	// 3. Build the dependency container.
	svcs, err := coding.NewServices(coding.ServicesOptions{
		CWD:      cwd,
		AgentDir: agentDir, // pick up legacy ~/.pi-coding-agent if present
	})
	if err != nil {
		log.Fatalf("NewServices: %v", err) //nolint:gocritic // intentional: defers are cleanup-only, already empty
	}

	// 4. Build the Runtime. No extensions, no shared tools: bring
	//    your own tools via SessionStartOptions.ExtraTools below if
	//    you want bash/read/etc.
	rt, err := coding.NewRuntime(coding.RuntimeOptions{
		Services: svcs,
	})
	if err != nil {
		log.Fatalf("NewRuntime: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// 5. Resolve the model. coding.BuildModel handles provider switching
	//    (github-copilot, openai, openrouter, groq, ollama, ...).
	model, err := coding.BuildModel(modelID, svcs)
	if err != nil {
		log.Fatalf("BuildModel(%q): %v\n\n"+
			"Hint: run `pig --print --model %s hi` once with the\n"+
			"standalone binary to verify auth + provider availability.",
			modelID, err, modelID)
	}

	// 6. Create a fresh session. SystemPrompt is intentionally minimal
	//    here; for real use, consider pig's prompts.BuildDefaultPrompt
	//    which assembles a far richer prompt (tool descriptions, agent
	//    persona, skill instructions, etc.).
	sess, err := rt.New(coding.SessionStartOptions{
		Model:        model,
		SystemPrompt: "You are a concise assistant. Reply in one short sentence.",
		// Add caller-supplied tools here:
		ExtraTools: []agent.AgentTool{},
	})
	if err != nil {
		log.Fatalf("rt.New: %v", err)
	}
	defer func() { _ = sess.Close() }()

	// 7. Send a single user prompt. Send is synchronous: it blocks
	//    until the agent's tool-loop terminates. For streaming, use
	//    sess.Events() in a separate goroutine BEFORE calling Send.
	ctx := context.Background()
	msgs, err := sess.Send(ctx, "What is 1 + 1? Reply in one short sentence.")
	if err != nil {
		log.Fatalf("sess.Send: %v", err)
	}

	// 8. Pull out the last assistant text and print it.
	reply := lastAssistantText(msgs)
	fmt.Println("ASSISTANT:", reply)
	fmt.Println()
	fmt.Printf("Session JSONL: %s\n", sess.Path())
	fmt.Printf("Session id:    %s\n", sess.ID())
	fmt.Printf("Working dir:   %s\n", filepath.Clean(cwd))
}

// lastAssistantText extracts the most recent assistant message's
// concatenated text content. For multi-block responses (e.g.,
// tool-calls + text), it returns just the text portion.
func lastAssistantText(msgs []agent.AgentMessage) string {
	for _, msg := range slices.Backward(msgs) {
		if msg.Assistant == nil {
			continue
		}
		var b strings.Builder
		for _, c := range msg.Assistant.Content {
			if t, ok := c.(ai.TextContent); ok {
				b.WriteString(t.Text)
			}
		}
		return b.String()
	}
	return "(no assistant reply)"
}
