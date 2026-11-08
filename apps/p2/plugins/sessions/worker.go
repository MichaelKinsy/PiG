package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/sessions/worker.ts: what a session's worker contributes: the lock mirror, `SessionTransfer` (the
// server reads a conversation's active context here and seeds the new session's worker) and the `rename_session` tool.

// sessionToolsName is the worker object that contributes the tool (`p2.session-tools`).
const sessionToolsName = "p2.session-tools"

// renameDescription is the tool's description: the agent keeps the session's title naming its overall task.
var renameDescription = strings.Join([]string{
	"Rename the current session: its title in the session list, which is how the user finds it again.",
	"Keep the title naming the overall task of the session. Call this whenever that overall task changes: the user moves on to a different piece of work, or the work turns out to be something other than what the title says. Also call it when the current title does not describe the task, for example when it is just the start of the first message.",
	`Do not rename for sub-steps, follow-up questions, fixes or progress within the same task. Example: "Migrate pi-mobile to the monorepo" stays while you fix its build and push it; it becomes "Fix the terminal on Android" when the user turns to the terminal.`,
	fmt.Sprintf("The title: a short noun or verb phrase of 3 to 8 words, at most %d characters, in the user's language, no trailing period, no quotes.", contract.MaxTitle),
}, "\n")

// isJSSpace is what JavaScript's `\s` and `trim` take as white space.
func isJSSpace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// cleanTitle is title with each run of white space one space, trimmed.
func cleanTitle(title string) string { return strings.Join(strings.FieldsFunc(title, isJSSpace), " ") }

// jsonQuote is `JSON.stringify(text)`.
func jsonQuote(text string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(out.String(), "\n")
}

func toolReply(text string, isError bool) durable.ToolExecutionResult {
	result := durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}
	if isError {
		result.IsError = &isError
	}
	return result
}

// renameTool is the `rename_session` tool. It runs outside Chord calls (a Durable tool), so each call opens a short
// root of the worker's. Only top-level conversations (the main conversation and its `/tree` branches) may rename the
// session.
func renameTool(worker func() (*p2runtime.WorkerParts, error)) (*durable.ToolRegistration, error) {
	parameters := schema.Obj(schema.Properties{
		schema.Prop("title", schema.Str(schema.MinLength(1), schema.MaxLength(contract.MaxTitle))),
	}, schema.Open())
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{"rename_session", renameDescription, mustMarshal(parameters)})
	if err != nil {
		return nil, err
	}
	var tool ai.ToolSchema
	if err := json.Unmarshal(encoded, &tool); err != nil {
		return nil, err
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: tool,
		// Renaming to the same title again is harmless.
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			fields, _ := args.(map[string]any)
			title, _ := fields["title"].(string)
			title = cleanTitle(title)
			if title == "" {
				return toolReply("The title is empty.", true), nil
			}
			parts, err := worker()
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			// A Durable tool is not a Chord call: it pushes the title in a short root of the worker's, under its own context.
			answered, err := parts.Root(func(root host.Ctx) (any, error) {
				sessions, err := root.Get(contract.Sessions, delta.NewJsonObject(0), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				return sessions.Call("renameSelf", delta.JsonObjectOf("title", title), root)
			}, ctx)
			if err != nil {
				if ctx.Err() != nil {
					return durable.ToolExecutionResult{}, err
				}
				return toolReply(err.Error(), true), nil
			}
			previous, _ := answered.(*delta.JsonObject).Value("previous").(string)
			if previous == title {
				return toolReply("Session title: "+jsonQuote(title)+".", false), nil
			}
			return toolReply("Renamed the session from "+jsonQuote(previous)+" to "+jsonQuote(title)+".", false), nil
		},
	}), nil
}

func mustMarshal(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}

// serveSessionTools contributes the `rename_session` tool.
func serveSessionTools(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, sessionToolsName, func(self host.StoreSelf) error {
		worker := p2runtime.WorkerOf(self)
		tool, err := renameTool(worker)
		if err != nil {
			return err
		}
		const name = "p2-session-title"
		extension := &p2runtime.P2Extension{
			Extension: &durable.Extension{Name: name, Tools: []*durable.ToolRegistration{tool}},
			Access:    map[string]p2runtime.ToolAccess{"rename_session": {Scope: "main"}},
		}
		p2runtime.ContributeKeyed(self, p2runtime.Extensions, p2runtime.Entry(name, extension))
		return nil
	})
}

// history is the server's read of a conversation's active context before an entry, as entry drafts: the fork holds
// what came before the chosen message, as the context stood at the entry before it.
func history(ctx context.Context, session harness.Harness, conversation durable.ConversationId, before durable.EntryId) (any, error) {
	top, err := p2runtime.IsTopLevel(ctx, session, conversation)
	if err != nil {
		return nil, err
	}
	// A subagent's history as a session of its own needs its cwd and tools: not yet.
	if !top {
		return nil, errors.New("Forking a subagent conversation is not supported yet")
	}
	found, err := session.Conversation(ctx, conversation)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, fmt.Errorf("Unknown conversation %d", conversation)
	}
	chosen, err := found.Entries(ctx, durable.EntryQuery{ConversationId: found.Id(), MaxEntryId: &before}, 1, nil)
	if err != nil {
		return nil, err
	}
	if len(chosen.Items) == 0 || chosen.Items[0].Id != before {
		return nil, errors.New("Unknown message")
	}
	previousID := before - 1
	previous, err := found.Entries(ctx, durable.EntryQuery{ConversationId: found.Id(), MaxEntryId: &previousID}, 1, nil)
	if err != nil {
		return nil, err
	}
	if len(previous.Items) == 0 {
		return delta.JsonObjectOf("entries", []any{}), nil
	}
	view, err := found.Context(ctx, &durable.ContextOptions{At: &previous.Items[0].Id})
	if err != nil {
		return nil, err
	}
	// Edits are applied in `contributions` (their targets' IDs change); a head marker starts the copy's context.
	entries := make([]any, len(view.Entries))
	for i, entry := range view.Entries {
		draft := durable.EntryDraft{Kind: entry.Kind, Data: entry.Data}
		if i < len(view.Contributions) && len(view.Contributions[i]) > 0 {
			draft.Model = append([]ai.Message(nil), view.Contributions[i]...)
		}
		draft.HeadSelf = i == 0 && view.Head != nil
		encoded, err := durable.ToJsonValue(draft)
		if err != nil {
			return nil, err
		}
		entries[i] = encoded
	}
	return delta.JsonObjectOf("entries", entries), nil
}

// seed appends entries to the main conversation in one commit; refused when it has history already.
func seed(ctx context.Context, session harness.Harness, entries []any) error {
	root, err := session.Conversation(ctx, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return err
	}
	if root == nil {
		return errors.New("No main conversation")
	}
	existing, err := root.Entries(ctx, durable.EntryQuery{ConversationId: durable.ROOT_CONVERSATION_ID}, 1, nil)
	if err != nil {
		return err
	}
	if len(existing.Items) > 0 {
		return errors.New("The session has history already")
	}
	drafts := make([]durable.EntryDraft, len(entries))
	for i, entry := range entries {
		drafts[i], err = durable.FromJsonValue[durable.EntryDraft](entry)
		if err != nil {
			return fmt.Errorf("seed entry %d: %w", i, err)
		}
	}
	_, err = session.Commit(ctx, func(tx durable.Tx) (any, error) {
		for _, draft := range drafts {
			if _, err := tx.AppendEntry(durable.ROOT_CONVERSATION_ID, draft); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// serveTransfer serves SessionTransfer to the server.
func serveTransfer(facet host.FacetHost) error {
	return facet.Serve(contract.SessionTransfer, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		if who, err := p2runtime.FromChord(principal); err != nil || who != p2runtime.Server {
			return contracts.Implementation{}, p2runtime.Forbidden("server only")
		}
		parts := p2runtime.HarnessOf(self)
		worker := p2runtime.WorkerOf(self)
		opened := func() (harness.Harness, error) {
			held, err := parts()
			if err != nil {
				return nil, err
			}
			return held.Durable()
		}
		return contracts.Implementation{Variant: "server", Members: contracts.VariantImplementation{
			Methods: map[string]contracts.MethodFunction{
				"history": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					in := objectArgs(args)
					conversation, _ := in.Value("conversation").(float64)
					before, _ := in.Value("before").(float64)
					session, err := opened()
					if err != nil {
						return nil, err
					}
					out, err := history(ctx, session, durable.ConversationId(conversation), durable.EntryId(before))
					if err != nil {
						return nil, err
					}
					return out, nil
				},
				"copyFiles": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					to, _ := objectArgs(args).Value("to").(string)
					process, err := worker()
					if err != nil {
						return nil, err
					}
					return nil, process.CopyFilesTo(ctx, to)
				},
				"seed": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					chordctx.Suspend(ctx)
					entries, _ := objectArgs(args).Value("entries").([]any)
					session, err := opened()
					if err != nil {
						return nil, err
					}
					return nil, seed(ctx, session, entries)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// Worker is the sessions plugin's worker facet.
var Worker = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveLockMirror, serveTransfer, serveSessionTools} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
