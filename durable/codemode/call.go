// Ports packages/durable/src/codemode/codemode.ts (CodemodeCall).

package codemode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/internal/search"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	argsPreviewChars  = 200
	errorPreviewChars = 500
)

// cut is text cut to max UTF-16 units, ending in "..." when it was longer.
func cut(text string, max int) string {
	if jsstring.Length(text) <= max {
		return text
	}
	units := jsstring.ToUTF16(text)
	return jsstring.FromUTF16(units[:max-3]) + "..."
}

// events is the call's single-threaded event loop. The sandbox's callbacks, commits and nested calls run on other
// goroutines and post what they found here, so the call's state is touched by one goroutine, in the order the events
// arrive, as upstream's single JavaScript thread does.
type events struct {
	mu     sync.Mutex
	items  []func()
	wake   chan struct{}
	closed bool
}

func newEvents() *events { return &events{wake: make(chan struct{}, 1)} }

// post queues f; it never blocks, and drops f once the loop has ended.
func (e *events) post(f func()) {
	e.mu.Lock()
	if !e.closed {
		e.items = append(e.items, f)
	}
	e.mu.Unlock()
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// next waits for the next event, in posting order.
func (e *events) next() func() {
	for {
		e.mu.Lock()
		if len(e.items) > 0 {
			f := e.items[0]
			e.items[0] = nil
			e.items = e.items[1:]
			e.mu.Unlock()
			return f
		}
		e.mu.Unlock()
		<-e.wake
	}
}

func (e *events) close() {
	e.mu.Lock()
	e.closed, e.items = true, nil
	e.mu.Unlock()
}

type queued struct {
	id      int
	at      float64
	outcome cm.Outcome
	global  *journalGlobal
}

type ack struct{ seq, issued int }

// call is one execution of the code mode tool: a first run, or a rerun after a restart that replays the journal.
type call struct {
	options ToolOptions
	api     durable.ToolExecutionApi
	ctx     context.Context
	// work is the context of what the call waits for (nested calls, reads); it ends when the call does.
	work context.Context
	// controller aborts the sandbox run; cancelling ctx cancels it too.
	controller context.Context
	abort      context.CancelCauseFunc
	events     *events
	spawned    sync.WaitGroup

	// callable are the tools of this attempt's agent, by name: their schemas report arguments that are not an object.
	callable map[string]*durable.ToolRegistration
	run      *cm.Run
	start    start
	// replayed is the journal as this attempt found it; replay delivers exactly these.
	replayed []journalEntry
	// journalLength is the entries in the journal, replayed and live; the next delivery is journalLength + 1.
	journalLength int
	// nextReplayed is the replayed entries delivered so far.
	nextReplayed int
	replaying    bool
	// rerun is whether an earlier attempt ran this call: its nested call keys may already exist.
	rerun bool
	// released is whether live calls may launch; before, they are held (see release).
	released bool
	// firstIssued is the count an earlier attempt recorded after the script's first run.
	firstIssued *int
	// durableIssued is the highest issued count known to be committed: calls up to it may launch.
	durableIssued int
	// waiting are live calls waiting for their issued count to be committed.
	waiting []cm.HostCall
	// replayIndex is the journal index of each replayed call ID.
	replayIndex map[int]int
	// issued is the calls the VM issued so far; its call IDs are 1, 2, ...
	issued int
	// ready are the results of journaled calls, ready to deliver in journal order.
	ready map[int]cm.Outcome
	// held are calls the journal does not have, admitted once replay is done.
	held []cm.HostCall
	// queue are finished calls waiting for their journal commit.
	queue []queued
	// acks are the acknowledged counts to record, by result number (0: the script's first run), in the order first set.
	acks       []ack
	committing bool
	// commitPosted is whether pump posted a commit that has not started yet.
	commitPosted bool
	// journalCommit is closed once the last started journal commit returned; the outcome commit waits for it.
	journalCommit chan struct{}
	// ended is set once the call decided an outcome other than the script's own (divergence, mismatch).
	ended   *storedResult
	failure error
	// rows are the script's tool calls for details, by VM call ID.
	rows map[int]*NestedCall
	// modelMu guards the slots of models.classify()/generateImages() calls: modelCalls in flight, and waiters in order.
	modelMu      sync.Mutex
	modelCalls   int
	modelWaiters []chan struct{}
	// generatedImages are the images models.generateImages() returned, to notice a script that never shows them.
	generatedImages atomic.Int64

	// stoppedFlag mirrors ended or failure for the goroutines that check stopped.
	stoppedFlag sync.Mutex
	stoppedSet  bool

	// gate opens once the VM's fingerprint is checked or recorded; nothing is delivered or admitted before.
	gateOpen    bool
	gateWaiters []func()

	finished bool
	out      durable.ToolExecutionResult
	outErr   error
}

func newCall(options ToolOptions, api durable.ToolExecutionApi, ctx context.Context) *call {
	return &call{options: options, api: api, ctx: ctx, events: newEvents(), replayIndex: map[int]int{}, ready: map[int]cm.Outcome{}, rows: map[int]*NestedCall{}}
}

// spawn runs f on its own goroutine, which the call waits for before it returns.
func (c *call) spawn(f func()) {
	//portlint:allow asyncorder spawned joins every spawn before the call returns, and each spawn's effects reach the call through c.post, the call's ordered event queue
	c.spawned.Go(func() {
		f()
	})
}

// spawnEntered is spawn, and returns once f has entered: it returned, or its goroutine is parked in a wait. A call
// Pi's host starts (`this.callTool(call)`, `this.callGlobal(call)`) runs synchronously up to its first await before the
// next event the script's VM posted, so a tool call a script made is running (its nested call is being admitted)
// before the call that follows it is started, and before the script can end and abort it.
func (c *call) spawnEntered(f func()) {
	c.spawned.Add(1)
	entered.Go(func() {
		defer c.spawned.Done()
		f()
	})()
}

func (c *call) post(f func()) { c.events.post(f) }

// afterGate runs f once the fingerprint gate is open (`this.gate.then(f)`).
func (c *call) afterGate(f func()) {
	if c.gateOpen {
		c.post(f)
		return
	}
	c.gateWaiters = append(c.gateWaiters, f)
}

func (c *call) openGate() {
	c.gateOpen = true
	for _, f := range c.gateWaiters {
		c.post(f)
	}
	c.gateWaiters = nil
}

// finish ends the call with its result or error; only the first counts.
func (c *call) finish(result durable.ToolExecutionResult, err error) {
	if c.finished {
		return
	}
	c.finished, c.out, c.outErr = true, result, err
}

func (c *call) execute(source string) (durable.ToolExecutionResult, error) {
	parsed, err := cm.ParseCodemodeSource(source)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	agent, err := c.api.Agent(c.ctx)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	c.callable = map[string]*durable.ToolRegistration{}
	tools := make([]declaredTool, len(agent.Callable))
	var listed []*durable.ToolRegistration
	for i, tool := range agent.Callable {
		c.callable[tool.Name] = tool
		sample, err := sampleOf(tool)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		tools[i] = declaredTool{Name: tool.Name, JSName: cm.ToCodemodeIdentifier(tool.Name), Description: sample, Structured: tool.StructuredOutputSchema != nil, Hidden: strings.HasPrefix(tool.Name, InternalPrefix)}
		if !tools[i].Hidden {
			listed = append(listed, tool)
		}
	}
	var globals []declaredGlobal
	for _, name := range discoveryGlobals {
		globals = append(globals, declaredGlobal{Name: name, Spread: true})
	}
	if c.options.models() {
		for _, name := range catalogGlobals {
			globals = append(globals, declaredGlobal{Name: name, Spread: true})
		}
		for _, model := range modelCalls {
			globals = append(globals, declaredGlobal{Name: model.global, Spread: true})
		}
	}
	for _, global := range c.options.Globals {
		globals = append(globals, declaredGlobal{Name: global.Name, Spread: global.Spread})
	}
	namespaces := []startNamespace{}
	for _, tool := range listed {
		namespace := namespaceOf(tool.Name, c.options.Namespaces)
		if namespace == nil {
			continue
		}
		index := slices.IndexFunc(namespaces, func(candidate startNamespace) bool { return candidate.Name == namespace.Name })
		if index < 0 {
			namespaces = append(namespaces, startNamespace{Name: namespace.Name, Description: namespace.Description, Instructions: namespace.Instructions, Tools: []string{}})
			index = len(namespaces) - 1
		}
		namespaces[index].Tools = append(namespaces[index].Tools, tool.Name)
	}
	maxOutputTokens := DefaultMaxOutputTokens
	if parsed.Options.MaxOutputTokens != nil {
		maxOutputTokens = int(*parsed.Options.MaxOutputTokens)
	}
	now := c.api.Now()
	var seedBytes [4]byte
	if _, err := rand.Read(seedBytes[:]); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	seed := binary.LittleEndian.Uint32(seedBytes[:])

	type found struct {
		done        *storedResult
		start       start
		journal     []journalEntry
		firstIssued *int
		rerun       bool
	}
	value, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
		run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
		if err != nil {
			return nil, err
		}
		state, err := durable.FromJsonValue[runState](run.Snapshot())
		if err != nil {
			return nil, err
		}
		if state.Result != nil {
			return found{done: state.Result}, nil
		}
		rerun := state.Start != nil
		if state.Start == nil {
			store, err := durable.TxDoc[StoreState](tx, StoreDoc, c.api.ConversationId())
			if err != nil {
				return nil, err
			}
			values, err := durable.FromJsonValue[StoreState](store.Snapshot())
			if err != nil {
				return nil, err
			}
			created := start{Version: ModuleVersion, StartedAt: now, Seed: seed, Tools: tools, Namespaces: namespaces, Globals: globals, MaxOutputTokens: maxOutputTokens, Store: values.Values}
			if created.Store == nil {
				created.Store = map[string]json.RawMessage{}
			}
			if parsed.Options.TimeoutMs != nil {
				deadline := now + float64(*parsed.Options.TimeoutMs)
				created.Deadline = &deadline
			}
			encoded, err := durable.ToJsonValue(created)
			if err != nil {
				return nil, err
			}
			if err := run.Set("start", encoded); err != nil {
				return nil, err
			}
			state.Start = &created
		}
		journal, err := durable.TxDoc[journalState](tx, JournalDoc, c.api.TaskId())
		if err != nil {
			return nil, err
		}
		entries, err := durable.FromJsonValue[journalState](journal.Snapshot())
		if err != nil {
			return nil, err
		}
		return found{start: *state.Start, journal: entries.Entries, firstIssued: entries.FirstIssued, rerun: rerun}, nil
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	f := value.(found)
	if f.done != nil {
		return f.done.result(), nil
	}
	c.start = f.start
	c.replayed = f.journal
	for index, entry := range f.journal {
		c.replayIndex[entry.ID] = index
	}
	c.journalLength = len(f.journal)
	c.replaying = len(f.journal) > 0
	c.rerun = f.rerun
	c.firstIssued = f.firstIssued
	if f.firstIssued != nil {
		c.durableIssued = *f.firstIssued
	}
	for _, entry := range f.journal {
		if entry.IssuedAfter != nil && *entry.IssuedAfter > c.durableIssued {
			c.durableIssued = *entry.IssuedAfter
		}
	}

	if c.start.Version != ModuleVersion {
		return c.decideNow(errorResult(fmt.Sprintf("The script cannot resume: it started under code mode version %d, this is %d.", c.start.Version, ModuleVersion)))
	}
	var remaining *float64
	if c.start.Deadline != nil {
		left := *c.start.Deadline - c.api.Now()
		remaining = &left
		if left <= 0 {
			return c.decideNow(errorResult("Script timed out: its deadline passed while it was interrupted."))
		}
	}

	c.controller, c.abort = context.WithCancelCause(c.ctx)
	defer c.abort(nil)
	var endWork context.CancelFunc
	c.work, endWork = context.WithCancel(c.ctx)
	defer endWork()
	var startTools []cm.DeclaredTool
	for _, tool := range c.start.Tools {
		if !tool.Hidden {
			startTools = append(startTools, cm.DeclaredTool{Name: tool.Name, JSName: tool.JSName, Description: tool.Description})
		}
	}
	startGlobals := make([]cm.DeclaredGlobal, len(c.start.Globals))
	for i, global := range c.start.Globals {
		startGlobals[i] = cm.DeclaredGlobal{Name: global.Name, Spread: global.Spread}
	}
	run, err := c.options.Sandbox.Start(c.controller, parsed.Code, cm.RunOptions{
		StartedAt: c.start.StartedAt,
		Seed:      c.start.Seed,
		Tools:     startTools,
		Globals:   startGlobals,
		Store:     c.start.Store,
		TimeoutMs: remaining,
		OnStarted: func(fingerprint string) { c.post(func() { c.checkFingerprint(fingerprint) }) },
		OnCall:    func(call cm.HostCall) { c.post(func() { c.onCall(call) }) },
		OnIdle:    func(seq, issued int) { c.post(func() { c.onIdle(seq, issued) }) },
	})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	c.run = run
	c.spawn(func() {
		result := run.Result()
		c.post(func() { c.onResult(result) })
	})
	for !c.finished {
		c.events.next()()
	}
	// A commit pump decided on starts, as it would have when pump ran; its result is dropped with the rest.
	c.flushPostedCommit()
	// What is still running is stopped: a nested call's wait ends with the context, a commit's result is dropped.
	c.events.close()
	c.abort(nil)
	endWork()
	c.spawned.Wait()
	return c.out, c.outErr
}

// onResult is what follows the script's end: the call's outcome, or an error that leaves the call to a later attempt.
func (c *call) onResult(result cm.Result) {
	if c.failure != nil {
		c.finish(durable.ToolExecutionResult{}, c.failure)
		return
	}
	if c.ended != nil {
		c.finish(c.ended.result(), nil)
		return
	}
	if c.ctx.Err() != nil {
		c.finish(durable.ToolExecutionResult{}, context.Cause(c.ctx))
		return
	}
	// Aborted, but not by this call: the host closed the sandbox, for example while shutting down before closing the
	// Harness. That decides nothing about the script: store nothing and end with this invocation, so a later attempt
	// replays it.
	if !result.OK && result.Error.Kind == "aborted" && c.controller.Err() == nil {
		if c.ctx.Done() == nil {
			c.finish(durable.ToolExecutionResult{}, errors.New("The code mode sandbox was closed while the script ran"))
			return
		}
		c.spawn(func() {
			select {
			case <-c.ctx.Done():
				c.post(func() { c.finish(durable.ToolExecutionResult{}, context.Cause(c.ctx)) })
			case <-c.work.Done():
			}
		})
		return
	}
	// A replay that ends where the live run went on is a divergence (design §4.3).
	during := result.During
	// Within the replayed part, the first run included: a rerun without journal entries replays that much too.
	if c.rerun && during != nil && during.Seq <= len(c.replayed) {
		// The live run went on: it acknowledged that result, or it made a call that later results answer, which the replay
		// never made.
		var continued bool
		if during.Seq == 0 {
			continued = len(c.replayed) > 0 || c.firstIssued != nil
		} else {
			continued = c.replayed[during.Seq-1].IssuedAfter != nil
		}
		continued = continued || slices.ContainsFunc(c.replayed[during.Seq:], func(entry journalEntry) bool { return entry.ID > during.Issued })
		if continued {
			c.decideAsync(errorResult(fmt.Sprintf("The script took a different path when resumed after a restart: it ended after result %d, where it went on before.", during.Seq)), nil)
			return
		}
	}
	c.complete(result)
}

// stopped is whether nothing more may happen: the run has its outcome, or the call decided or failed.
func (c *call) stopped() bool {
	return c.decided() || (c.run != nil && c.run.Finished())
}

// decided is whether the call decided or failed (ended or failure), whatever the run's own state.
func (c *call) decided() bool {
	c.stoppedFlag.Lock()
	defer c.stoppedFlag.Unlock()
	return c.stoppedSet
}

func (c *call) markStopped() {
	c.stoppedFlag.Lock()
	c.stoppedSet = true
	c.stoppedFlag.Unlock()
}

func (c *call) fail(err error) {
	if c.failure != nil {
		return
	}
	c.failure = err
	c.markStopped()
	c.abort(err)
}

// resultWithDetails is a decided result with the call's details.
func (c *call) resultWithDetails(decided storedResult) (storedResult, error) {
	details, err := json.Marshal(c.details())
	if err != nil {
		return decided, err
	}
	decided.Details = details
	return decided, nil
}

// storeDecided commits the outcome the call decided; the first outcome stays.
func (c *call) storeDecided(result storedResult) error {
	_, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
		run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
		if err != nil {
			return nil, err
		}
		if run.Has("result") {
			return nil, nil
		}
		encoded, err := durable.ToJsonValue(result)
		return nil, errors.Join(err, run.Set("result", encoded))
	})
	return err
}

// decideNow stores an outcome the call decided before the VM started; returns the outcome.
func (c *call) decideNow(decided storedResult) (durable.ToolExecutionResult, error) {
	result, err := c.resultWithDetails(decided)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if err := c.storeDecided(result); err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return result.result(), nil
}

// decideAsync stores an outcome the call decided while the VM ran, then finishes the call with it and runs then.
func (c *call) decideAsync(decided storedResult, then func()) {
	result, err := c.resultWithDetails(decided)
	if err != nil {
		c.finish(durable.ToolExecutionResult{}, err)
		return
	}
	c.spawn(func() {
		err := c.storeDecided(result)
		c.post(func() {
			if err != nil {
				c.finish(durable.ToolExecutionResult{}, err)
				return
			}
			if then != nil {
				then()
			}
			c.finish(result.result(), nil)
		})
	})
}

// diverge ends the call with message: stores it as the outcome, then stops the VM with an error the script cannot catch.
func (c *call) diverge(message string) {
	if c.ended != nil || c.failure != nil {
		return
	}
	result := errorResult("The script took a different path when resumed after a restart: " + message)
	c.ended = &result
	c.markStopped()
	stored, err := c.resultWithDetails(result)
	if err != nil {
		c.fail(err)
		return
	}
	c.spawn(func() {
		err := c.storeDecided(stored)
		c.post(func() {
			if err != nil {
				c.fail(err)
				return
			}
			c.abort(errors.New(message))
		})
	})
}

// complete stores the script's own outcome with its store writes in one transaction.
func (c *call) complete(result cm.Result) {
	details := c.details()
	options := formatOptions{
		generatedImages: int(c.generatedImages.Load()),
		wallMs:          c.api.Now() - c.start.StartedAt,
		maxOutputTokens: c.start.MaxOutputTokens,
		calls:           details.Calls,
		limits:          toolOutputLimits,
		write:           c.outputFiles(),
	}
	var writes *cm.StoreWrites
	if result.OK {
		writes = &result.StoreWrites
	}
	// The journal's last commit lands before the outcome's: a commit still posted starts now, and the outcome waits for it.
	c.flushPostedCommit()
	journalCommit := c.journalCommit
	c.spawn(func() {
		if journalCommit != nil {
			<-journalCommit
		}
		value, err := c.formatAndStore(result, details, options, writes)
		c.post(func() {
			if err != nil {
				c.finish(durable.ToolExecutionResult{}, err)
				return
			}
			c.finish(value.result(), nil)
		})
	})
}

// formatAndStore formats the script's result for the model, writing the files it names, and commits it.
func (c *call) formatAndStore(result cm.Result, details ToolDetails, options formatOptions, writes *cm.StoreWrites) (storedResult, error) {
	content, fullOutputPath, err := formatResult(result, options)
	if err != nil {
		return storedResult{}, err
	}
	details.FullOutputPath = fullOutputPath
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return storedResult{}, err
	}
	tool := storedResult{Output: contentOf(content), IsError: !result.OK, Details: encodedDetails}
	{
		value, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
			run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
			if err != nil {
				return nil, err
			}
			if run.Has("result") {
				existing, err := durable.FromJsonValue[storedResult](run.Get("result"))
				return existing, err
			}
			encoded, err := durable.ToJsonValue(tool)
			if err != nil {
				return nil, err
			}
			if err := run.Set("result", encoded); err != nil {
				return nil, err
			}
			if writes != nil && (len(writes.Set) > 0 || len(writes.Delete) > 0) {
				store, err := durable.TxDoc[StoreState](tx, StoreDoc, c.api.ConversationId())
				if err != nil {
					return nil, err
				}
				values := store.Object("values")
				for _, key := range writes.Delete {
					values.Delete(key)
				}
				for _, key := range sortedKeys(writes.Set) {
					var decoded any
					if err := json.Unmarshal(writes.Set[key], &decoded); err != nil {
						return nil, err
					}
					if err := values.Set(key, decoded); err != nil {
						return nil, err
					}
				}
			}
			return tool, nil
		})
		if err != nil {
			return storedResult{}, err
		}
		return value.(storedResult), nil
	}
}

// outputFiles writes the result's files under fixed names into the run's output directory, so a rerun overwrites them.
func (c *call) outputFiles() outputFile {
	environment := c.api.Env()
	var directory string
	var directoryErr error
	var made bool
	return func(name string, data any) (string, string) {
		if environment == nil {
			return "", "no execution environment"
		}
		if !made {
			directory, directoryErr = c.outputDir(environment)
			made = true
		}
		if directoryErr != nil {
			return "", directoryErr.Error()
		}
		path := directory + "/" + name
		if err := environment.WriteFile(c.ctx, path, data); err != nil {
			return "", err.Error()
		}
		return path, ""
	}
}

// outputDir is the run's output directory: the recorded one, else a new temp directory, recorded before any file is
// written.
func (c *call) outputDir(environment env.ExecutionEnv) (string, error) {
	recorded, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
		run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
		if err != nil {
			return nil, err
		}
		state, err := durable.FromJsonValue[runState](run.Snapshot())
		if err != nil {
			return nil, err
		}
		return state.OutputDir, nil
	})
	if err != nil {
		return "", err
	}
	if existing, _ := recorded.(*string); existing != nil {
		return *existing, nil
	}
	prefix := "pi-codemode-"
	created, err := environment.CreateTempDir(c.ctx, &prefix)
	if err != nil {
		return "", err
	}
	value, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
		run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
		if err != nil {
			return nil, err
		}
		state, err := durable.FromJsonValue[runState](run.Snapshot())
		if err != nil {
			return nil, err
		}
		if state.OutputDir != nil {
			return *state.OutputDir, nil
		}
		if err := run.Set("outputDir", created); err != nil {
			return nil, err
		}
		return created, nil
	})
	if err != nil {
		return "", err
	}
	return value.(string), nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (c *call) checkFingerprint(fingerprint string) {
	if c.start.Fingerprint != nil {
		recorded := *c.start.Fingerprint
		if recorded != fingerprint {
			ended := errorResult(fmt.Sprintf("The script cannot resume: the sandbox changed since it started (%s -> %s).", recorded, fingerprint))
			c.ended = &ended
			c.markStopped()
			stored, err := c.resultWithDetails(ended)
			if err != nil {
				c.fail(err)
				return
			}
			c.spawn(func() {
				err := c.storeDecided(stored)
				c.post(func() {
					if err != nil {
						c.fail(err)
					}
					c.abort(errors.New("Sandbox fingerprint changed"))
				})
			})
			return
		}
		c.fingerprintChecked()
		return
	}
	c.spawn(func() {
		_, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
			run, err := durable.TxDoc[runState](tx, RunDoc, c.api.TaskId())
			if err != nil {
				return nil, err
			}
			if started := run.Object("start"); started != nil {
				return nil, started.Set("fingerprint", fingerprint)
			}
			return nil, nil
		})
		c.post(func() {
			if err != nil {
				c.fail(err)
				return
			}
			c.fingerprintChecked()
		})
	})
}

func (c *call) fingerprintChecked() {
	c.openGate()
	// Nothing was delivered before the gate: start now. A rerun without a journal releases its calls at the script's
	// first acknowledgment instead.
	if c.replaying {
		c.deliverReplayed()
	} else if !c.rerun {
		c.release()
	}
}

// onCall handles a call the script made. Pi's onCall (packages/durable/src/codemode/codemode.ts:639-650) runs synchronously
// when the VM reports the call, before the VM's `done` can finish the run, so its `stopped` check never sees the run
// finished: a call the script made just before it ended, such as an unawaited `tools.hold({ id: 9 }); return 1;`, still
// gets its row and shows as cancelled in the details (test/codemode.test.ts:441-464). Here the call reaches the event
// loop after the run may have finished, so only the call's own decision or failure stops it.
func (c *call) onCall(made cm.HostCall) {
	if c.decided() {
		return
	}
	hostCall := asToolCall(made)
	if hostCall.Target == "tool" {
		c.started(hostCall)
	}
	c.issued = max(c.issued, hostCall.ID)
	index := -1
	if c.replaying {
		if i, ok := c.replayIndex[hostCall.ID]; ok {
			index = i
		}
	}
	if index == -1 {
		if c.released {
			c.waiting = append(c.waiting, hostCall)
			c.launchCovered()
		} else {
			c.held = append(c.held, hostCall)
		}
		return
	}
	entry := c.replayed[index]
	if entry.Global != nil {
		if hostCall.Target != "global" || hostCall.Name != entry.Global.Name || !sameJSON(hostCall.Args, entry.Global.Args) {
			c.diverge(fmt.Sprintf("call %d is %s, but was a call to %s", hostCall.ID, describe(hostCall), entry.Global.Name))
			return
		}
		c.ready[hostCall.ID] = outcomeFrom(entry.Global.Outcome)
		c.afterGate(c.deliverReplayed)
		return
	}
	if hostCall.Target != "tool" {
		c.diverge(fmt.Sprintf("call %d is %s, but was a tool call", hostCall.ID, describe(hostCall)))
		return
	}
	c.afterGate(func() {
		slot := c.reserveModelSlot(hostCall)
		// pig divergence (D184): the call's goroutine has entered before the next event, but a stall of it between two steps of its task can still let the script end and abort it before its tool started; Pi's one thread always starts it first.
		c.spawnEntered(func() {
			done, err := c.callTool(hostCall, slot)
			c.post(func() {
				if err != nil {
					if c.stopped() {
						return
					}
					if c.ctx.Err() != nil {
						c.fail(err)
					} else {
						// The key names another tool or other arguments.
						c.diverge(fmt.Sprintf("call %d is %s: %s", hostCall.ID, describe(hostCall), err.Error()))
					}
					return
				}
				c.finishRow(hostCall.ID, done)
				c.ready[hostCall.ID] = done.outcome
				c.deliverReplayed()
			})
		})
	})
}

// deliverReplayed delivers journaled results in journal order, each once the VM has issued its call.
func (c *call) deliverReplayed() {
	for c.nextReplayed < len(c.replayed) && !c.stopped() {
		entry := c.replayed[c.nextReplayed]
		outcome, ok := c.ready[entry.ID]
		if entry.ID > c.issued || !ok {
			return
		}
		delete(c.ready, entry.ID)
		c.nextReplayed++
		if err := c.run.Deliver(c.nextReplayed, entry.At, entry.ID, outcome); err != nil {
			c.fail(err)
			return
		}
	}
}

func (c *call) onIdle(seq, issued int) {
	if c.stopped() {
		return
	}
	// A count an earlier attempt recorded must match; a missing one is recorded now.
	if c.rerun && seq <= len(c.replayed) {
		var recorded *int
		if seq == 0 {
			recorded = c.firstIssued
		} else {
			recorded = c.replayed[seq-1].IssuedAfter
		}
		if recorded != nil && *recorded != issued {
			if seq == 0 {
				c.diverge(fmt.Sprintf("its first run made %d calls, where it had made %d", issued, *recorded))
			} else {
				c.diverge(fmt.Sprintf("after result %d it had made %d calls, where it had made %d", seq, issued, *recorded))
			}
			return
		}
	}
	c.setAck(seq, issued)
	if !c.replaying {
		// A rerun without a journal: every call an earlier attempt could have admitted is made by now.
		if seq == 0 && c.rerun {
			c.release()
		}
		// Committed at once when the script has processed everything delivered: calls it made wait for it, and a script
		// waiting on a long call is where a restart usually finds it. Otherwise with the next commit; the VM catches up
		// without waiting on anything.
		if seq == c.journalLength {
			c.pump()
		}
		return
	}
	// Caught up: the VM processed everything delivered and waits.
	if seq != c.nextReplayed {
		return
	}
	if seq < len(c.replayed) {
		if next := c.replayed[seq]; next.ID > issued {
			c.diverge(fmt.Sprintf("it waits without making call %d, which it made before", next.ID))
		}
		return
	}
	// The last replayed result is acknowledged: continue live, admitting the calls the journal did not have once their
	// count is committed.
	c.replaying = false
	c.pump()
	c.release()
}

func (c *call) setAck(seq, issued int) {
	for i := range c.acks {
		if c.acks[i].seq == seq {
			c.acks[i].issued = issued
			return
		}
	}
	c.acks = append(c.acks, ack{seq, issued})
}

// release lets the held calls launch, and later live calls, each once its issued count is committed. On a rerun, an earlier
// attempt may have admitted some of them under their keys, as other calls: when the replay releases (idle after the last
// journaled result, or after its first run without a journal) its counts matched every recorded one, so it has made every
// call an earlier attempt could have admitted. All held calls are checked first, whatever their target, and a divergence
// launches none. Calls made after this are new.
func (c *call) release() {
	c.afterGate(func() {
		if c.stopped() || c.released {
			return
		}
		calls := c.held
		c.held = nil
		if !c.rerun {
			c.releaseCalls(calls)
			return
		}
		c.spawn(func() {
			for _, hostCall := range calls {
				other, err := c.earlier(hostCall)
				if err != nil {
					c.post(func() { c.fail(err) })
					return
				}
				if c.stopped() {
					return
				}
				if other != "" {
					c.post(func() { c.diverge(fmt.Sprintf("call %d is %s, but was %s", hostCall.ID, describe(hostCall), other)) })
					return
				}
			}
			c.post(func() { c.releaseCalls(calls) })
		})
	})
}

func (c *call) releaseCalls(calls []cm.HostCall) {
	c.released = true
	c.waiting = append(c.waiting, calls...)
	c.waiting = append(c.waiting, c.held...)
	c.held = nil
	c.launchCovered()
}

// launchCovered launches the waiting calls whose issued count is committed.
func (c *call) launchCovered() {
	if !c.released || c.stopped() {
		return
	}
	var covered, rest []cm.HostCall
	for _, hostCall := range c.waiting {
		if hostCall.ID <= c.durableIssued {
			covered = append(covered, hostCall)
		} else {
			rest = append(rest, hostCall)
		}
	}
	if len(covered) == 0 {
		return
	}
	c.waiting = rest
	for _, hostCall := range covered {
		c.launch(hostCall)
	}
}

// earlier is the call an earlier attempt made under this call's key, when it is another one: another target, tool, or
// arguments. Empty when there is none or it is the same.
func (c *call) earlier(hostCall cm.HostCall) (string, error) {
	nested, err := durable.Snapshot[harness.NestedCallState](c.work, c.api, harness.NestedCallDoc, c.api.TaskId(), fmt.Sprintf("c%d", hostCall.ID))
	if err != nil || nested == nil || nested.TaskId == nil {
		return "", err
	}
	task, err := c.api.GetTask(c.work, *nested.TaskId)
	if err != nil || task == nil {
		return "", err
	}
	input, err := durable.FromJsonValue[harness.ToolTaskInput](task.Input)
	if err != nil || input.Kind != harness.ToolTaskNested || input.Call == nil {
		return "", nil
	}
	madeArguments, err := input.Call.ArgumentsJSON()
	if err != nil {
		return "", err
	}
	if hostCall.Target == "tool" && input.Call.Name == hostCall.Name && sameJSON(madeArguments, orEmptyObject(hostCall.Args)) {
		return "", nil
	}
	return "tools." + input.Call.Name + "(" + string(compact(madeArguments)) + ")", nil
}

func orEmptyObject(args json.RawMessage) json.RawMessage {
	if args == nil {
		return json.RawMessage("{}")
	}
	return args
}

// launch runs one live call and journals its result.
func (c *call) launch(hostCall cm.HostCall) {
	if c.stopped() {
		return
	}
	slot := c.reserveModelSlot(hostCall)
	// pig divergence (D184): the call's goroutine has entered before the next call is launched, but a stall of it between two steps of its task can still let the script end and abort it before its tool started; Pi's one thread always starts it first.
	c.spawnEntered(func() {
		var outcome cm.Outcome
		var recorded *journalGlobal
		var done nestedDone
		var err error
		if hostCall.Target == "tool" {
			done, err = c.callTool(hostCall, slot)
			outcome = done.outcome
		} else {
			outcome, err = c.callGlobal(hostCall)
			recorded = &journalGlobal{Name: hostCall.Name, Args: hostCall.Args, Outcome: outcomeToJSON(outcome)}
		}
		c.post(func() {
			if err != nil {
				// Stopped meanwhile: the outcome is decided, or a waiting model call gave up.
				if c.stopped() {
					return
				}
				if c.ctx.Err() != nil {
					c.fail(err)
				} else {
					// A key an earlier attempt admitted with another tool or other arguments: this attempt took another path.
					c.diverge(fmt.Sprintf("call %d is %s: %s", hostCall.ID, describe(hostCall), err.Error()))
				}
				return
			}
			// A result after the outcome is decided has nowhere to go, and the call's API has ended.
			if c.stopped() {
				return
			}
			if hostCall.Target == "tool" {
				c.finishRow(hostCall.ID, done)
			}
			c.queue = append(c.queue, queued{id: hostCall.ID, at: c.api.Now(), outcome: outcome, global: recorded})
			c.pump()
		})
	})
}

// nestedDone is what a script's tool call came to: its outcome for the script, and how the call's row shows it.
type nestedDone struct {
	outcome cm.Outcome
	// isError is whether the tool's result is an error result, also when the script got its value.
	isError     bool
	duration    *int64
	diagnostics string
}

// modelSlot is a model call's place in line for one of the maxConcurrentModelCalls slots.
type modelSlot struct {
	// granted is closed once the call holds a slot.
	granted <-chan struct{}
	// queued is whether every slot was taken when the call was launched, so it waits for one. Pi's `await this.modelSlot()`
	// (packages/durable/src/codemode/codemode.ts:820-826, 890-895) parks only then: a call that gets a slot at once goes on to
	// executeTool in the same turn as its launch, before the script can end, so only a call that waited can find the script
	// ended. Here too: launch returns once the call entered (spawnEntered), so the check is limited to queued calls.
	queued bool
}

// reserveModelSlot takes the call's place in line for one of the maxConcurrentModelCalls slots, on the event loop, so
// the calls get their slots in the order the script made them; nil for a call that needs none.
func (c *call) reserveModelSlot(hostCall cm.HostCall) *modelSlot {
	if hostCall.Target != "tool" || !strings.HasPrefix(hostCall.Name, InternalPrefix) {
		return nil
	}
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	granted := make(chan struct{})
	if c.modelCalls < maxConcurrentModelCalls {
		c.modelCalls++
		close(granted)
		return &modelSlot{granted: granted}
	}
	c.modelWaiters = append(c.modelWaiters, granted)
	return &modelSlot{granted: granted, queued: true}
}

// releaseModelSlot gives the slot to the first waiting call, if any.
func (c *call) releaseModelSlot() {
	c.modelMu.Lock()
	defer c.modelMu.Unlock()
	if len(c.modelWaiters) > 0 {
		close(c.modelWaiters[0])
		c.modelWaiters = c.modelWaiters[1:]
		return
	}
	c.modelCalls--
}

// callTool runs the nested call of a script's tool call under the key `c<id>`, so a rerun reattaches to it.
//
// Pi passes the arguments as the script gave them, and the nested call's own validation turns a value that is not an
// object into an error result. ExecuteTool takes a map (a Go map cannot hold another value), so such a call is answered
// here, with the tool schema's validation message, without a nested call: it has no nested slot, task, usage row or tool
// events.
func (c *call) callTool(hostCall cm.HostCall, slot *modelSlot) (nestedDone, error) {
	if slot != nil {
		if slot.queued {
			select {
			case <-slot.granted:
			case <-c.work.Done():
				return nestedDone{}, context.Cause(c.work)
			}
			// The call may have stopped while this one waited: a finished script admits no new paid call.
			if c.stopped() {
				c.releaseModelSlot()
				return nestedDone{}, errors.New("The script has ended")
			}
		}
		defer c.releaseModelSlot()
	}
	args := map[string]any{}
	if hostCall.Args != nil {
		if err := json.Unmarshal(hostCall.Args, &args); err != nil || args == nil {
			message := c.notAnObject(hostCall)
			return nestedDone{outcome: cm.Outcome{Message: message}, isError: true}, nil
		}
	}
	key := fmt.Sprintf("c%d", hostCall.ID)
	// The script's member order, which a Go map loses: the stored call, its slot and events, and models.* requests keep it.
	result, err := c.api.ExecuteTool(c.work, hostCall.Name, args, durable.NestedCallOptions{Key: &key, ArgumentsJSON: hostCall.Args})
	if err != nil {
		return nestedDone{}, err
	}
	if hostCall.Name == GenerateImagesTool {
		c.generatedImages.Add(int64(countImages(result)))
	}
	structured := false
	for _, tool := range c.start.Tools {
		if tool.Name == hostCall.Name {
			structured = tool.Structured
		}
	}
	var diagnostics []string
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity == "error" {
			diagnostics = append(diagnostics, diagnostic.Message)
		}
	}
	return nestedDone{outcome: outcomeOf(hostCall.Name, structured, result), isError: result.IsError, duration: result.DurationMs, diagnostics: strings.Join(diagnostics, "\n")}, nil
}

// countImages is the image blocks in a `models.generateImages()` result.
func countImages(result durable.NestedToolExecutionResult) int {
	raw, err := json.Marshal(result.StructuredOutput)
	if err != nil {
		return 0
	}
	var value struct {
		Output []struct {
			Type any `json:"type"`
		} `json:"output"`
	}
	//portlint:allow jsonclone once per models.generateImages() call, which costs a provider request; the stored result has no typed form here
	if json.Unmarshal(raw, &value) != nil { //perf:ok once per models.generateImages() call, which costs a provider request; the value is a stored result and has no typed form here
		return 0
	}
	count := 0
	for _, item := range value.Output {
		if item.Type == "image" {
			count++
		}
	}
	return count
}

// asToolCall is a script's call of `models.classify()` or `models.generateImages()` as the call of its internal tool.
func asToolCall(call cm.HostCall) cm.HostCall {
	if call.Target != "global" {
		return call
	}
	for _, model := range modelCalls {
		if model.global != call.Name {
			continue
		}
		list := jval(call.Args).items()
		argument := func(i int) string {
			if i < len(list) && list[i].kind() != "undefined" {
				return string(list[i])
			}
			return "null"
		}
		return cm.HostCall{ID: call.ID, Target: "tool", Name: model.tool, Args: json.RawMessage(`{"model":` + argument(0) + `,"context":` + argument(1) + `}`)}
	}
	return call
}

// notAnObject is why the arguments of a tool call are not valid: the message of the tool schema's validation.
func (c *call) notAnObject(hostCall cm.HostCall) string {
	if tool, ok := c.callable[hostCall.Name]; ok {
		if _, err := ai.ValidateToolArgumentsSchema(hostCall.Name, tool.Parameters, hostCall.Args); err != nil {
			return err.Error()
		}
	}
	return "Arguments of tool " + hostCall.Name + " must be an object"
}

func (c *call) callGlobal(hostCall cm.HostCall) (cm.Outcome, error) {
	discovery := slices.Contains(discoveryGlobals, hostCall.Name)
	catalog := slices.Contains(catalogGlobals, hostCall.Name)
	var global *Global
	if !discovery && !catalog {
		for i := range c.options.Globals {
			if c.options.Globals[i].Name == hostCall.Name {
				global = &c.options.Globals[i]
				break
			}
		}
		if global == nil {
			return cm.Outcome{Message: "Unknown global " + hostCall.Name}, nil
		}
	}
	// `undefined` stays undefined: the journal omits the value, and a replay resolves with undefined again.
	var value json.RawMessage
	var err error
	switch {
	case discovery:
		value, err = c.discover(hostCall)
	case catalog:
		value, err = catalogCall(c.work, hostCall.Name, hostCall.Args, c.api.Models())
	default:
		value, err = global.Execute(c.work, hostCall.Args, c.api)
	}
	if err != nil {
		if c.ctx.Err() != nil {
			return cm.Outcome{}, err
		}
		return cm.Outcome{Message: err.Error()}, nil
	}
	return cm.Outcome{OK: true, Value: value}, nil
}

// started records that the script made a tool call: its row appears, running, also while it waits to launch.
func (c *call) started(hostCall cm.HostCall) {
	if _, ok := c.rows[hostCall.ID]; ok {
		return
	}
	args := ""
	if hostCall.Args != nil {
		args = string(compact(hostCall.Args))
	}
	name := strings.TrimPrefix(hostCall.Name, InternalPrefix)
	c.rows[hostCall.ID] = &NestedCall{ID: hostCall.ID, Name: name, Args: cut(args, argsPreviewChars), Status: "running"}
}

// finishRow gives an ended tool call's row its status, duration, and error: an error result is an error row, also when the
// script got its value (bash's nonzero exit).
func (c *call) finishRow(id int, done nestedDone) {
	row, ok := c.rows[id]
	if !ok {
		return
	}
	row.Status = "ok"
	if done.isError {
		row.Status = "error"
	}
	if done.duration != nil {
		row.DurationMs = done.duration
	}
	message := done.diagnostics
	if !done.outcome.OK {
		message = done.outcome.Message
	}
	if done.isError && message != "" {
		row.Error = cut(message, errorPreviewChars)
	}
}

// details are the rows in call order; calls still running when the script ended were cancelled.
func (c *call) details() ToolDetails {
	calls := make([]NestedCall, 0, len(c.rows))
	for _, row := range c.rows {
		calls = append(calls, *row)
	}
	sort.Slice(calls, func(i, j int) bool { return calls[i].ID < calls[j].ID })
	for i := range calls {
		if calls[i].Status == "running" {
			calls[i].Status = "cancelled"
		}
	}
	return ToolDetails{Calls: calls}
}

// pump journals finished calls and acknowledged counts in one commit, then delivers the calls and launches the calls the
// counts cover; one commit in flight at a time, so what arrives meanwhile goes into the next.
//
// Pi's results that finish together join one commit because their callbacks all run in one microtask drain before the
// commit's awaits resume. Here each result is posted by its own goroutine, and the executor and commit goroutines hand the
// processor to each other ahead of goroutines already runnable, so with one processor each result would commit alone. The
// commit therefore starts from an event posted after a yield: results that finish together post first and join it. A commit
// pump decided on always starts: the call's end and its outcome start one still posted (flushPostedCommit).
func (c *call) pump() {
	if c.committing || c.stopped() {
		return
	}
	if len(c.queue) == 0 && len(c.acks) == 0 {
		return
	}
	c.committing = true
	c.commitPosted = true
	runtime.Gosched()
	c.post(c.commitQueued)
}

// flushPostedCommit starts the commit pump posted, when it has not started yet.
func (c *call) flushPostedCommit() {
	if c.commitPosted {
		c.commitQueued()
	}
}

// commitQueued is pump's commit of everything queued when it runs. pump checked that the call had not stopped, so it starts
// as it would have there.
func (c *call) commitQueued() {
	if !c.commitPosted {
		return
	}
	c.commitPosted = false
	done := make(chan struct{})
	c.journalCommit = done
	batch := c.queue
	c.queue = nil
	acks := c.acks
	c.acks = nil
	c.spawn(func() {
		_, err := c.api.Commit(c.ctx, func(tx durable.Tx) (any, error) {
			journal, err := durable.TxDoc[journalState](tx, JournalDoc, c.api.TaskId())
			if err != nil {
				return nil, err
			}
			entries := journal.Array("entries")
			for _, a := range acks {
				if a.seq == 0 {
					if !journal.Has("firstIssued") {
						if err := journal.Set("firstIssued", a.issued); err != nil {
							return nil, err
						}
					}
					continue
				}
				if entry := entries.Object(a.seq - 1); entry != nil && !entry.Has("issuedAfter") {
					if err := entry.Set("issuedAfter", a.issued); err != nil {
						return nil, err
					}
				}
			}
			for _, item := range batch {
				encoded, err := durable.ToJsonValue(journalEntry{ID: item.id, At: item.at, Global: item.global})
				if err != nil {
					return nil, err
				}
				if _, err := entries.Push(encoded); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		close(done)
		c.post(func() {
			if err != nil {
				c.fail(err)
				return
			}
			for _, a := range acks {
				c.durableIssued = max(c.durableIssued, a.issued)
			}
			for _, item := range batch {
				c.journalLength++
				if !c.run.Finished() {
					if err := c.run.Deliver(c.journalLength, item.at, item.id, item.outcome); err != nil {
						c.fail(err)
						return
					}
				}
			}
			c.launchCovered()
			c.committing = false
			c.pump()
		})
	})
}

func outcomeToJSON(outcome cm.Outcome) outcomeJSON {
	return outcomeJSON{Ok: outcome.OK, Value: outcome.Value, Message: outcome.Message, Properties: outcome.Properties}
}

func outcomeFrom(stored outcomeJSON) cm.Outcome {
	return cm.Outcome{OK: stored.Ok, Value: stored.Value, Message: stored.Message, Properties: stored.Properties}
}

func compact(raw json.RawMessage) []byte {
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}

func describe(hostCall cm.HostCall) string {
	prefix := ""
	if hostCall.Target == "tool" {
		prefix = "tools."
	}
	args := ""
	if hostCall.Args != nil {
		args = string(compact(hostCall.Args))
	}
	return prefix + hostCall.Name + "(" + args + ")"
}

// sameJSON is structural JSON equality; key order does not matter, and undefined (nil) equals only undefined.
func sameJSON(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == 0 && len(b) == 0
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// discover answers `searchTools()`, `describeTool()` and `describeNamespace()` from the start data.
func (c *call) discover(hostCall cm.HostCall) (json.RawMessage, error) {
	args := jval(hostCall.Args).items()
	argument := func(i int) jval {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	var tools []declaredTool
	for _, tool := range c.start.Tools {
		if !tool.Hidden {
			tools = append(tools, tool)
		}
	}
	namespaces := c.start.Namespaces
	namespaceOfTool := func(name string) *startNamespace {
		for i := range namespaces {
			if slices.Contains(namespaces[i].Tools, name) {
				return &namespaces[i]
			}
		}
		return nil
	}
	matchesNamespace := func(namespace *startNamespace, query string) bool {
		return namespace.Name == query || cm.ToCodemodeIdentifier(namespace.Name) == cm.ToCodemodeIdentifier(query)
	}
	switch hostCall.Name {
	case "describeTool":
		name, ok := argument(0).str()
		if !ok {
			return nil, errors.New("describeTool() expects a tool name")
		}
		for _, tool := range tools {
			if tool.Name == name || tool.JSName == name {
				return json.Marshal(tool.Description)
			}
		}
		return nil, nil
	case "describeNamespace":
		name, ok := argument(0).str()
		if !ok {
			return nil, errors.New("describeNamespace() expects a namespace name")
		}
		for i := range namespaces {
			if !matchesNamespace(&namespaces[i], name) {
				continue
			}
			described := struct {
				Name         string   `json:"name"`
				Description  string   `json:"description,omitempty"`
				Instructions string   `json:"instructions,omitempty"`
				Tools        []string `json:"tools"`
			}{namespaces[i].Name, namespaces[i].Description, namespaces[i].Instructions, make([]string, len(namespaces[i].Tools))}
			for j, tool := range namespaces[i].Tools {
				described.Tools[j] = cm.ToCodemodeIdentifier(tool)
			}
			return json.Marshal(described)
		}
		return nil, nil
	}
	query, ok := argument(0).str()
	if !ok {
		return nil, errors.New("searchTools() expects a query string")
	}
	options := argument(1)
	limit, namespace := searchOptions(options)
	if !limit.valid {
		return nil, errors.New("searchTools() limit must be a positive integer")
	}
	if namespace.kind() != "undefined" && namespace.kind() != "null" && namespace.kind() != "string" {
		return nil, errors.New("searchTools() namespace must be a string")
	}
	wanted, restricted := namespace.str()
	var documents []search.Document
	for _, tool := range tools {
		own := namespaceOfTool(tool.Name)
		if restricted && (own == nil || !matchesNamespace(own, wanted)) {
			continue
		}
		ownName, ownDescription := "", ""
		if own != nil {
			ownName, ownDescription = own.Name, own.Description
		}
		text := strings.Join([]string{tool.Name, strings.ReplaceAll(tool.Name, "_", " "), tool.Description, ownName, ownDescription}, " ")
		documents = append(documents, search.Document{Name: tool.Name, Text: text})
	}
	type found struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	matches := []found{}
	for _, name := range search.RankDocuments(query, documents, limit.n) {
		for _, tool := range tools {
			if tool.Name == name {
				matches = append(matches, found{tool.JSName, tool.Description})
				break
			}
		}
	}
	return json.Marshal(matches)
}

// searchLimit is the `limit` of searchTools(): a positive integer, the default when absent.
type searchLimit struct {
	n     int
	valid bool
}

// searchOptions reads `options?.limit ?? 8` and `options?.namespace` of searchTools()'s second argument.
func searchOptions(options jval) (searchLimit, jval) {
	limit := searchLimit{n: defaultSearchLimit, valid: true}
	var namespace jval
	if options.kind() == "undefined" || options.kind() == "null" {
		return limit, nil
	}
	namespace = options.field("namespace")
	value := options.field("limit")
	switch value.kind() {
	case "undefined", "null":
	case "number":
		var number float64
		if json.Unmarshal(value, &number) != nil || number != math.Trunc(number) || number <= 0 || math.IsInf(number, 0) {
			return searchLimit{}, namespace
		}
		//portlint:allow numbers number is a finite positive integer here (Number.isInteger, codemode.ts:930); rankDocuments' slice(0, limit) keeps every document at any limit past MaxInt32
		limit.n = int(min(number, math.MaxInt32))
	default:
		return searchLimit{}, namespace
	}
	return limit, namespace
}
