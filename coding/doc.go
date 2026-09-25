// Package coding is the public SDK for embedding the pig coding
// agent as a Go library.
//
// # Quick start
//
//	import (
//	    "context"
//	    "github.com/MichaelKinsy/PiG/coding"
//	)
//
//	svcs, err := coding.NewServices(coding.ServicesOptions{
//	    CWD:      "/my/project",
//	    AgentDir: coding.DefaultAgentDir(),
//	})
//	if err != nil { return err }
//
//	rt, err := coding.NewRuntime(coding.RuntimeOptions{Services: svcs})
//	if err != nil { return err }
//	defer rt.Close()
//
//	model, err := coding.BuildModel("github-copilot/gpt-4o-mini", svcs)
//	if err != nil { return err }
//
//	sess, err := rt.New(coding.SessionStartOptions{
//	    Model:        model,
//	    SystemPrompt: "You are a helpful coding assistant.",
//	})
//	if err != nil { return err }
//	defer sess.Close()
//
//	msgs, err := sess.Send(context.Background(), "What is 2+2?")
//
// # Architecture
//
// The package exposes three public types that compose into a working
// agent:
//
//   - [Services]: dependency container. Owns the auth storage,
//     model registry, settings manager. Construct once per process
//     (or per CWD).
//
//   - [Runtime]: session factory. Holds a Services plus an
//     optional pre-loaded extension runner. Produces *Session
//     values via New / Resume / Open / Continue.
//
//   - [Session]: a single live conversation. Wraps the headless
//     agent loop, the on-disk JSONL transcript, and the slash
//     command registry. Exposes Send for prompts, Events for
//     streaming, plus session-management methods (Fork, Clone,
//     Tree, SetName).
//
// Sessions persist as JSONL files at <CWD>/.pig/sessions/<id>.jsonl.
// Resuming a session via Runtime.Resume / Runtime.Continue rebuilds
// the agent's in-memory message history from the on-disk transcript.
//
// # Tools
//
// By default a fresh Session has the built-in coding-agent tools
// (read, write, bash, edit, grep, find, ls). Add your own tools
// via SessionStartOptions.ExtraTools: any type implementing
// [agent.AgentTool] works:
//
//	rt.New(coding.SessionStartOptions{
//	    Model: model,
//	    ExtraTools: []agent.AgentTool{myTool, otherTool},
//	})
//
// To suppress all extension-supplied tools (e.g., for a sandboxed
// session), set SessionStartOptions.SkipExtensionTools = true.
//
// # Slash commands
//
// Sessions support the same slash commands as the interactive TUI
// via Session.DispatchSlash. In headless mode (no picker callbacks
// wired), commands like /resume / /fork / /tree fall back to text
// listings instead of overlay UIs:
//
//	out, err := sess.DispatchSlash("/tree")
//	for _, line := range out { fmt.Println(line) }
//
// # Forking and cloning
//
// Session.Fork moves the conversation leaf to an earlier entry; the
// next Send creates a new branch off that point. The agent's
// in-memory history is rebuilt to match.
//
// Session.Clone snapshots the current path-to-leaf as a new JSONL
// file and returns a new Session that's independent of the source.
//
// # Streaming events
//
// Session.Events returns a buffered channel of [agent.AgentEvent]
// values. Subscribe BEFORE calling Send; events fire during the
// agent's tool-loop and include text deltas, tool calls, tool
// results, and end-of-turn markers:
//
//	go func() {
//	    for ev := range sess.Events() {
//	        switch ev.Type {
//	        case agent.EventTextDelta:
//	            fmt.Print(ev.TextDelta)
//	        }
//	    }
//	}()
//
// # See also
//
//   - examples/sdk/main.go: minimal embedding example
//   - [agent]: agent loop + tool primitives
//   - [ai]: provider-agnostic LLM types and OAuth flows
package coding
