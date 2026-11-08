// Package chat is p2's chat plugin (apps/p2/plugins/chat/{worker,shell}.ts): conversations as principals see them, and
// the user shell commands (`!cmd`/`!!cmd`) of the worker's conversations.
package chat

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/schema"
)

// User shell commands (`!cmd`/`!!cmd`) of the worker's conversations: each command is a job whose state shows its output
// while it runs; the run places the command's `p2.shell` entry before it ends, so the live block gives way to the entry
// without a gap. One command at a time per conversation; the session neither moves nor clones while one runs.

var (
	str     = schema.Str
	boolean = schema.Bool
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop    = schema.Prop
	opt     = schema.Opt
)

// commandSchema is a command as the job takes it. The author is closed, unlike the stored entry's: job arguments are
// checked like method arguments. RequestID is the caller's ID for the command, its `shellId`: a client shows the command
// under it before the state arrives.
var commandSchema = obj(schema.Properties{
	prop("conversation", schema.Int(schema.Minimum(1))),
	prop("command", str(schema.MinLength(1), schema.MaxLength(100_000))),
	prop("includeInContext", boolean()),
	prop("author", obj(schema.Properties{
		prop("kind", schema.Union([]schema.Schema{p2runtime.Literal("owner"), p2runtime.Literal("guest")})),
		prop("id", str()), prop("name", str()),
	})),
	prop("requestId", str(schema.MinLength(1), schema.MaxLength(200))),
})

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// ShellCommands is the worker's user shell commands (`p2.shell-commands`; host-internal): each conversation's running
// command, and how to start and cancel one. Chat's views show their conversation's command and pass the caller as its
// author. `shells` holds the running (or finished, its entry not placed yet) command of each conversation, by
// conversation. `start` refuses while its conversation runs one (`ShellBusy`); its `shellId` is the caller's
// `requestId`, and a retried `requestId` of the same author starts nothing (among the last 100 starts). `abort` cancels
// the running command `shellId` and resolves once it shows as cancelled, false when it is not running.
var ShellCommands = must(contracts.Local("p2.shell-commands", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("shells", p2runtime.Contract(schema.Map(chatcontract.LiveShellSchema))),
		contracts.Mem("start", contracts.NewMethod(p2runtime.Contract(commandSchema), p2runtime.Contract(obj(schema.Properties{prop("shellId", str())})))),
		contracts.Mem("abort", contracts.NewMethod(
			p2runtime.Contract(obj(schema.Properties{prop("shellId", str())})),
			p2runtime.Contract(obj(schema.Properties{prop("aborted", boolean())})),
		)),
	},
}))

// sampleInterval is how often the live output is published at most (the bash tool's sampling, 100 KiB/s).
const sampleInterval = 200 * time.Millisecond

var outputSchema = obj(schema.Properties{
	prop("output", str()),
	prop("truncated", boolean()),
	prop("running", boolean()),
	prop("cancelled", boolean()),
	opt("exitCode", schema.Num()),
	// Where the environment kept the complete output, when it spilled.
	opt("fullOutputPath", str()),
	opt("error", str()),
})

// outputDoc is a command's live output: the run's state document.
var outputDoc = must(documents.DefineState("p2.shell-commands.output", documents.StateOptions{
	Schema:  p2runtime.Contract(outputSchema),
	Version: 1,
	Initial: func() *delta.JsonObject {
		return delta.JsonObjectOf("output", "", "truncated", false, "running", true, "cancelled", false)
	},
}))

// shellJob is a command's run. 2: the command's `requestId` (its `shellId`).
var shellJob = must(host.DefineJob("p2.shell-commands.run", host.JobOptions{
	Version: 2,
	Args:    p2runtime.Contract(commandSchema),
	Result:  contracts.Nul(),
	State:   outputDoc,
}))

// admittedDoc holds the request keys (`author:requestId`) of recent starts and the run each started, oldest first.
var admittedDoc = must(documents.DefineState("p2.shell-commands.admitted", documents.StateOptions{
	Schema:  p2runtime.Contract(obj(schema.Properties{prop("starts", schema.Map(str()))})),
	Version: 1,
	Initial: func() *delta.JsonObject { return delta.JsonObjectOf("starts", delta.NewJsonObject(0)) },
}))

const maxAdmitted = 100

// ServeShellCommands serves `ShellCommands` (the chat worker facet).
func ServeShellCommands(facet host.FacetHost) error {
	return facet.Serve(ShellCommands, host.LocalFactory(serveShells), host.ServeOptions{})
}

func serveShells(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	harness := cached(p2runtime.HarnessOf(self))
	worker := cached(p2runtime.WorkerOf(self))
	admitted := self.State(admittedDoc).(documents.SingletonHandle)
	job := self.Job(shellJob, host.JobImplementation{
		Run: func(args any, ctx host.JobCtx) (any, error) {
			parts, err := worker()
			if err == nil {
				defer parts.Liveness.AddActiveCalls(-1)
			}
			command, _ := args.(*delta.JsonObject)
			environments, err := harness()
			if err != nil {
				return nil, err
			}
			if err := runCommand(command, environments, ctx); err != nil {
				return nil, err
			}
			return nil, placeEntry(command, ctx, environments)
		},
	})
	shells := self.Derive([]any{job.Running()}, func(values []any) (delta.JsonValue, error) {
		byConversation := delta.NewJsonObject(0)
		runs, _ := values[0].([]any)
		for _, each := range runs {
			run, _ := each.(*delta.JsonObject)
			if run.Value("phase") == "blocked" {
				continue
			}
			command, _ := run.Value("args").(*delta.JsonObject)
			state, _ := run.Value("state").(*delta.JsonObject)
			conversation, _ := command.Value("conversation").(float64)
			byConversation.Set(strconv.FormatFloat(conversation, 'f', -1, 64), liveShell(command, state))
		}
		return byConversation, nil
	})
	running := func(shellId string) bool { return runningIn(shells.Value(), shellId) }
	return contracts.Implementation{Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{"shells": shells},
		Methods: map[string]contracts.MethodFunction{
			"start": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				// First use in this section: the run, off the loop, takes the kept natives.
				_, _ = harness()
				return start(self, job, shells, admitted, worker, args, ctx)
			},
			"abort": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				object, _ := args.(*delta.JsonObject)
				shellID, _ := object.Value("shellId").(string)
				// What lives in the host's sources is read here, inside the handler's section.
				if !running(shellID) {
					return delta.JsonObjectOf("aborted", false), nil
				}
				id, found := runOf(job, shellID)
				if !found {
					return delta.JsonObjectOf("aborted", true), nil
				}
				waitCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				cancelled, stop, err := watchCancelled(shells, shellID, waitCtx)
				if err != nil {
					return nil, err
				}
				defer stop()
				// The wait below needs the host's loop (the run's abort enters it): leave the handler's section.
				chordctx.Suspend(ctx)
				// `abort` resolves once the run ended, after placing its entry (at the next boundary while the agent works):
				// the caller waits only until the command shows as cancelled.
				if err := abortUntilCancelled(job, id, cancelled, waitCtx); err != nil {
					return nil, err
				}
				return delta.JsonObjectOf("aborted", true), nil
			},
		},
	}}, nil
}

// start starts a command in one transaction with its admission record, refusing a busy conversation. A running command is
// work the session's fence waits for (a move, a clone), as a call is: counted from before its start (the caller's own
// call still counts) until its run ends.
func start(self host.Self, job host.JobHandle, shells chord.Watchable[delta.JsonValue], admitted documents.SingletonHandle,
	worker func() (*p2runtime.WorkerParts, error), args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
	command, _ := args.(*delta.JsonObject)
	parts, err := worker()
	if err != nil {
		return nil, err
	}
	parts.Liveness.AddActiveCalls(1)
	started := false
	defer func() {
		if !started {
			parts.Liveness.AddActiveCalls(-1)
		}
	}()
	author, _ := command.Value("author").(*delta.JsonObject)
	authorID, _ := author.Value("id").(string)
	requestID, _ := command.Value("requestId").(string)
	conversation, _ := command.Value("conversation").(float64)
	key := authorID + ":" + requestID
	result, err := self.Tx(func(tx documents.Tx) (any, error) {
		draft, err := tx.Draft(admitted)
		if err != nil {
			return nil, err
		}
		starts := draft.Object("starts")
		if starts.Has(key) {
			return false, nil
		}
		if current, _ := shells.Value().(*delta.JsonObject); current != nil && current.Has(strconv.FormatFloat(conversation, 'f', -1, 64)) {
			return nil, errors.New(chatcontract.ShellBusy)
		}
		run, err := tx.Start(job.(documents.JobHandle), command)
		if err != nil {
			return nil, err
		}
		if err := starts.Set(key, run.ID()); err != nil {
			return nil, err
		}
		keys := starts.Keys()
		for _, old := range keys[:max(0, len(keys)-maxAdmitted)] {
			starts.Delete(old)
		}
		return true, nil
	}, ctx)
	if err != nil {
		return nil, err
	}
	started, _ = result.(bool)
	return delta.JsonObjectOf("shellId", requestID), nil
}

// runOf is the ID of the running run whose command has `shellID`.
func runOf(job host.JobHandle, shellID string) (string, bool) {
	value, err := job.Running().Value()
	if err != nil {
		return "", false
	}
	runs, _ := value.([]any)
	for _, each := range runs {
		run, _ := each.(*delta.JsonObject)
		if run.Value("phase") == "blocked" {
			continue
		}
		command, _ := run.Value("args").(*delta.JsonObject)
		if command.Value("requestId") == shellID {
			id, _ := run.Value("id").(string)
			return id, true
		}
	}
	return "", false
}

// runningIn reports whether the shells value holds a running command `shellID`.
func runningIn(value delta.JsonValue, shellID string) bool {
	current, _ := value.(*delta.JsonObject)
	for _, key := range current.Keys() {
		shell, _ := current.Value(key).(*delta.JsonObject)
		if shell.Value("id") == shellID && shell.Value("running") == true {
			return true
		}
	}
	return false
}

// watchCancelled follows the shells until the command `shellID` shows as no longer running: the channel gets a signal then.
// It opens the watch, so call it inside the handler's section.
func watchCancelled(shells chord.Watchable[delta.JsonValue], shellID string, ctx context.Context) (<-chan struct{}, func(), error) {
	watch, err := shells.Watch(ctx)
	if err != nil {
		return nil, nil, err
	}
	cancelled := make(chan struct{}, 1)
	signal := func(value delta.JsonValue) {
		if !runningIn(value, shellID) {
			select {
			case cancelled <- struct{}{}:
			default:
			}
		}
	}
	watch.Start(func(_ context.Context, call chord.WatchCall[delta.JsonValue]) error {
		if call.Kind == chord.WatchCallValue {
			signal(call.Value)
		}
		return nil
	})
	signal(watch.Value())
	return cancelled, func() { _, _ = watch.Stop() }, nil
}

// abortUntilCancelled aborts the run and returns when it settled or the command shows as cancelled, whichever comes first;
// ctx ends the wait.
func abortUntilCancelled(job host.JobHandle, id string, cancelled <-chan struct{}, ctx context.Context) error {
	aborted := make(chan error, 1)
	go func() {
		_, err := job.Abort(id, ctx)
		aborted <- err
	}()
	select {
	case err := <-aborted:
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		return nil
	case <-cancelled:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// liveShell is a run as Chat shows it.
func liveShell(command, state *delta.JsonObject) *delta.JsonObject {
	shell := delta.JsonObjectOf(
		"id", command.Value("requestId"),
		"command", command.Value("command"),
		"author", command.Value("author"),
		"includeInContext", command.Value("includeInContext"),
		"output", state.Value("output"),
		"truncated", state.Value("truncated"),
		"running", state.Value("running"),
		"cancelled", state.Value("cancelled"),
	)
	if exitCode := state.Value("exitCode"); exitCode != nil {
		shell.Set("exitCode", exitCode)
	}
	if message := state.Value("error"); message != nil {
		shell.Set("error", message)
	}
	return shell
}

// liveOutput is the tail a command kept so far, shared by the environment's output callback and the sampler.
type liveOutput struct {
	mu        sync.Mutex
	output    string
	truncated bool
}

func (l *liveOutput) add(text string, skipped bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if skipped {
		// The environment omitted output outside the kept tail: what follows replaces it.
		l.output = ""
		l.truncated = true
	}
	tail, cut := p2runtime.ShellTailDefault(l.output + text)
	l.output = tail
	l.truncated = l.truncated || cut
}

func (l *liveOutput) snapshot() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.output, l.truncated
}

// runCommand runs the command where its conversation works (a subagent's own location may be on another machine than the
// session's), sampling its output into the run's state, which ends with how it ended.
func runCommand(command *delta.JsonObject, parts *p2runtime.HarnessParts, ctx host.JobCtx) error {
	conversation, _ := command.Value("conversation").(float64)
	text, _ := command.Value("command").(string)
	live := &liveOutput{}
	// State changes are commits: written by one goroutine, so the last is the last written, and joined before the run
	// moves on.
	notify := make(chan struct{}, 1)
	stop := make(chan struct{})
	var writes sync.WaitGroup
	var writeErr error
	write := func(change func(draft *delta.Object) error) {
		if _, err := ctx.State().Change(ctx, func(draft *delta.Object) (any, error) { return nil, change(draft) }, host.ChangeOptions{}); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	writes.Add(1)
	go func() {
		defer writes.Done()
		pause := time.NewTimer(sampleInterval)
		pause.Stop()
		defer pause.Stop()
		for {
			select {
			case <-stop:
				return
			case <-notify:
			}
			pause.Reset(sampleInterval)
			select {
			case <-stop:
				return
			case <-pause.C:
			}
			output, truncated := live.snapshot()
			write(func(draft *delta.Object) error {
				if err := draft.Set("output", output); err != nil {
					return err
				}
				return draft.Set("truncated", truncated)
			})
		}
	}()
	ended := delta.NewJsonObject(2)
	func() {
		environments, err := parts.Environments()
		if err != nil {
			ended.Set("error", err.Error())
			return
		}
		durableHarness, err := parts.Durable()
		if err != nil {
			ended.Set("error", err.Error())
			return
		}
		location, env, err := p2runtime.ConversationEnvironment(ctx, environments, durable.ConversationId(conversation), durableHarness)
		if err != nil {
			ended.Set("error", err.Error())
			return
		}
		inherit := true
		result, err := env.Exec(ctx, text, &durableenv.ShellExecOptions{
			Cwd:        location.Cwd,
			InheritEnv: &inherit,
			Spill:      &durableenv.ShellSpillOptions{AfterBytes: p2runtime.ShellMaxBytes, AfterLines: p2runtime.ShellMaxLines},
			Window: &durableenv.ShellOutputWindow{
				MaxBytes: p2runtime.ShellMaxBytes, MaxLines: p2runtime.ShellMaxLines,
				MinIntervalMs: float64(sampleInterval / time.Millisecond), BytesPerSecond: 100 * 1024,
			},
			OnOutput: func(_ context.Context, text string, info durableenv.ShellOutputInfo) {
				live.add(text, info.Skipped != nil)
				select {
				case notify <- struct{}{}:
				default:
				}
			},
		})
		if err == nil {
			ended.Set("exitCode", float64(result.ExitCode))
			if result.SpillPath != "" {
				ended.Set("fullOutputPath", result.SpillPath)
			}
			return
		}
		var failure *durableenv.ExecutionError
		if errors.As(err, &failure) {
			if failure.Code == durableenv.ExecutionErrorAborted {
				return
			}
			if failure.Code == durableenv.ExecutionErrorTimeout {
				ended.Set("error", "Command timed out")
			} else {
				ended.Set("error", failure.Message)
			}
			if failure.SpillPath != "" {
				ended.Set("fullOutputPath", failure.SpillPath)
			}
			return
		}
		ended.Set("error", err.Error())
	}()
	close(stop)
	writes.Wait()
	output, truncated := live.snapshot()
	// A command's last newline ends its output; it is not an empty last line.
	output = strings.TrimSuffix(strings.TrimSuffix(output, "\n"), "\r")
	cancelled := ctx.Err() != nil
	write(func(draft *delta.Object) error {
		for key, value := range map[string]any{"output": output, "truncated": truncated, "running": false, "cancelled": cancelled} {
			if err := draft.Set(key, value); err != nil {
				return err
			}
		}
		for _, key := range ended.Keys() {
			if err := draft.Set(key, ended.Value(key)); err != nil {
				return err
			}
		}
		return nil
	})
	return writeErr
}

// placeEntry places the finished command as its `p2.shell` entry and waits until it is placed. A write never starts a
// run; while the agent works it is placed at the run's next boundary. A cancelled command is placed too: without the
// run's abort signal.
func placeEntry(command *delta.JsonObject, ctx host.JobCtx, parts *p2runtime.HarnessParts) error {
	state, err := ctx.State().Value()
	if err != nil {
		return err
	}
	authorObject, _ := command.Value("author").(*delta.JsonObject)
	author := p2runtime.Author{
		Kind: p2runtime.AuthorKind(stringOf(authorObject, "kind")), ID: stringOf(authorObject, "id"), Name: stringOf(authorObject, "name"),
	}
	includeInContext, _ := command.Value("includeInContext").(bool)
	shell := p2runtime.ShellData{
		Author: author, Command: stringOf(command, "command"), Output: stringOf(state, "output"),
		Cancelled: state.Value("cancelled") == true, Truncated: state.Value("truncated") == true, IncludeInContext: includeInContext,
	}
	data := delta.JsonObjectOf(
		"author", authorObject, "command", shell.Command, "output", shell.Output,
		"cancelled", shell.Cancelled, "truncated", shell.Truncated, "includeInContext", includeInContext,
	)
	if exitCode, ok := state.Value("exitCode").(float64); ok {
		shell.ExitCode = &exitCode
		data.Set("exitCode", exitCode)
	}
	if path, ok := state.Value("fullOutputPath").(string); ok {
		shell.FullOutputPath = &path
		data.Set("fullOutputPath", path)
	}
	if message, ok := state.Value("error").(string); ok {
		shell.Error = &message
		data.Set("error", message)
	}
	context := chordctx.WithoutAbortSignal(ctx)
	durableHarness, err := parts.Durable()
	if err != nil {
		return err
	}
	id := durable.ConversationId(command.Value("conversation").(float64))
	conversation, err := durableHarness.Conversation(context, id)
	if err != nil {
		return err
	}
	if conversation == nil {
		return errors.New("Unknown conversation " + strconv.Itoa(int(id)))
	}
	entry := &durable.EntryDraft{Kind: p2runtime.ShellEntry, Data: data}
	if includeInContext {
		entry.Model = []ai.Message{ai.UserMessage{
			Content: ai.UserContentBlocks{
				ai.TextContent{Text: p2runtime.SenderText(p2runtime.AuthorSender(author))},
				ai.TextContent{Text: p2runtime.ShellContextText(shell)},
			},
			Timestamp: time.Now().UnixMilli(),
		}}
	}
	requestID := author.ID + ":shell:" + ctx.JobID()
	submission, err := conversation.Submit(context, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, RequestId: &requestID, Entry: entry})
	if err != nil {
		return err
	}
	_, err = submission.Wait(context)
	return err
}

func stringOf(object *delta.JsonObject, key string) string {
	text, _ := object.Value(key).(string)
	return text
}
