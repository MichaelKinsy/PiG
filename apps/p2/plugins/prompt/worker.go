package prompt

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	durableharness "github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/prompt/worker.ts: prompt resources of a session: the app guide, AGENTS.md files (global and the
// project's) and skills, watched and reloaded without restarting the worker.
//
// Forced Go differences. A promise that is awaited is a channel closed when it settles. The natives (`WorkerOf`,
// `HarnessOf`) are read once, inside the files resource's open, and kept: the sections Durable renders run off the host's
// loop, where a registry value cannot be read.

func jsonQuote(text string) string {
	quoted, _ := chordjson.Marshal(text)
	return string(quoted)
}

// AppGuide is what the agent should know about the app it runs in, and where its private data lives (Pi Mobile's app
// guide, adapted). `app` is p2's own directory (`WorkerInit.App`): its documentation and source, for questions about p2
// itself. A remote session loads its skills from its environment's `~/.pi/p2/skills`.
func AppGuide(app, dataDir string, remote *string) string {
	skills := jsonQuote(nodepath.Join(dataDir, "skills"))
	if remote != nil && *remote != "" {
		skills = jsonQuote("~/.pi/p2/skills") + " on the remote environment \"" + *remote + "\""
	}
	return "You are running inside Pi (p2, the plugin-based Pi Mobile).\n" +
		"Application source and documentation: " + jsonQuote(app) + " (not the session cwd). For app changes, first read docs/handoff.md and docs/p2-notes.md there; plugins live in plugins/<name>/. Read them only when working on the app itself.\n" +
		"Private configuration/data: " + jsonQuote(dataDir) + ". Models: " + jsonQuote(nodepath.Join(dataDir, "models.json")) + "; settings: " + jsonQuote(nodepath.Join(dataDir, "settings.json")) + "; global instructions: " + jsonQuote(nodepath.Join(dataDir, "AGENTS.md")) + "; skills: " + skills + ". Before editing, read " + jsonQuote(nodepath.Join(app, "README.md")) + "'s Configuration section and plugins/models/model-config.ts or src/runtime/settings.ts under the app directory. Valid configuration changes hot-reload; in-flight requests keep their captured configuration.\n" +
		"Keep auth.json, owner keys/tokens and session data private. Never print, publish or commit credentials."
}

// loaded is one load of a location's prompt resources, or the error of a failed one (the last valid state stays).
type loaded struct {
	prompt *LoadedPrompt
	err    string
}

// loads are the prompt resources of one location: watched and loaded through its environment.
type loads interface {
	// current is the latest valid load (empty before the first).
	current() *LoadedPrompt
	// started waits for the first load (or its failure).
	started(ctx context.Context) error
	// subscribe calls listener with each load (or failure); a new subscriber gets the latest first.
	subscribe(listener func(loaded)) (stop func())
	// env is the environment its files are read through (skills, when invoked).
	env(ctx context.Context) (durableenv.ExecutionEnv, error)
}

var empty = &LoadedPrompt{Resources: Resources{SkillFiles: []SkillFile{}, Templates: []Template{}}, Project: &Resources{SkillFiles: []SkillFile{}, Templates: []Template{}}}

// broadcast is the listeners of a source of loads and its latest value.
type broadcast struct {
	lmu       sync.Mutex
	listeners map[int]func(loaded)
	next      int
	latest    *loaded
}

func (b *broadcast) publish(value loaded) {
	b.lmu.Lock()
	b.latest = &value
	listeners := make([]func(loaded), 0, len(b.listeners))
	for _, id := range slices.Sorted(maps.Keys(b.listeners)) {
		listeners = append(listeners, b.listeners[id])
	}
	b.lmu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
}

func (b *broadcast) subscribe(listener func(loaded)) func() {
	b.lmu.Lock()
	if b.listeners == nil {
		b.listeners = map[int]func(loaded){}
	}
	b.next++
	id := b.next
	b.listeners[id] = listener
	latest := b.latest
	b.lmu.Unlock()
	if latest != nil {
		listener(*latest)
	}
	return func() {
		b.lmu.Lock()
		defer b.lmu.Unlock()
		delete(b.listeners, id)
	}
}

// promptLoads watches the files at a location (through its environment) and loads them.
type promptLoads struct {
	broadcast
	dataDir string
	cancel  context.CancelFunc
	// startedDone is closed when the first load (or its failure) is done, ready when the environment is open (or failed),
	// finished when the watches stopped.
	startedDone chan struct{}
	ready       chan struct{}
	finished    chan struct{}

	mu      sync.Mutex
	prompt  *LoadedPrompt
	opened  durableenv.ExecutionEnv
	openErr error
}

func newPromptLoads(dataDir string, at p2runtime.Location, environments func() (p2runtime.ExecutionEnvironments, error)) *promptLoads {
	//portlint:allow ctxbackground the loads own this context for their life: close cancels it
	ctx, cancel := context.WithCancel(context.Background())
	l := &promptLoads{dataDir: dataDir, cancel: cancel, startedDone: make(chan struct{}), ready: make(chan struct{}), finished: make(chan struct{})}
	// Not awaited by the resource: the object (and its registrations) go live at once; input waits on the gate.
	//portlint:allow golifetime joined by close through finished
	go l.run(ctx, at, environments)
	return l
}

func (l *promptLoads) run(ctx context.Context, at p2runtime.Location, environments func() (p2runtime.ExecutionEnvironments, error)) {
	defer close(l.finished)
	var once sync.Once
	started := func() { once.Do(func() { close(l.startedDone) }) }
	defer started()
	opened, err := l.openEnvironment(ctx, at, environments)
	if err != nil {
		l.publish(loaded{err: "Could not watch prompt resources (" + err.Error() + ")"})
		return
	}
	remote := ""
	if at.Env != nil {
		remote = *at.Env
	}
	interval := 2 * time.Second
	if remote != "" {
		interval = 30 * time.Second
	}
	observer := NewObserver(Source{
		Env: opened, Directory: l.dataDir, Cwd: at.Cwd,
		Paths: func(ctx context.Context) (PathRules, error) { return pathsOf(ctx, opened) },
		SkillsRoot: func(ctx context.Context) (string, error) {
			if remote == "" {
				return nodepath.Join(l.dataDir, "skills"), nil
			}
			return opened.AbsolutePath(ctx, "~/.pi/p2/skills")
		},
		PromptsRoot: func(ctx context.Context) (string, error) {
			if remote == "" {
				return nodepath.Join(l.dataDir, "prompts"), nil
			}
			return opened.AbsolutePath(ctx, "~/.pi/p2/prompts")
		},
		Remote:   remote,
		Fallback: interval,
		Apply: func(prompt *LoadedPrompt, err string) {
			if prompt != nil {
				l.mu.Lock()
				l.prompt = prompt
				l.mu.Unlock()
			}
			l.publish(loaded{prompt: prompt, err: err})
		},
	})
	defer observer.Close()
	if err := observer.Start(ctx); err != nil && ctx.Err() == nil {
		l.publish(loaded{err: "Could not watch prompt resources (" + err.Error() + ")"})
	}
	started()
	<-ctx.Done()
}

// openEnvironment is the environment at `at`, kept for `env`.
func (l *promptLoads) openEnvironment(ctx context.Context, at p2runtime.Location, environments func() (p2runtime.ExecutionEnvironments, error)) (durableenv.ExecutionEnv, error) {
	var opened durableenv.ExecutionEnv
	current, err := environments()
	if err == nil {
		opened, err = current.At(ctx, at)
	}
	l.mu.Lock()
	l.opened, l.openErr = opened, err
	l.mu.Unlock()
	close(l.ready)
	return opened, err
}

func (l *promptLoads) current() *LoadedPrompt {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.prompt == nil {
		return empty
	}
	return l.prompt
}

func (l *promptLoads) started(ctx context.Context) error {
	select {
	case <-l.startedDone:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (l *promptLoads) env(ctx context.Context) (durableenv.ExecutionEnv, error) {
	select {
	case <-l.ready:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.opened, l.openErr
}

// close stops the watches and waits until they stopped.
func (l *promptLoads) close() {
	l.cancel()
	<-l.finished
}

// pathsOf are the path rules of the system env works on.
func pathsOf(ctx context.Context, executionEnv durableenv.ExecutionEnv) (PathRules, error) {
	remote, ok := executionEnv.(*env.RemoteExecutionEnv)
	if !ok {
		return LocalPaths, nil
	}
	info, err := remote.Connection.Info(ctx)
	if err != nil {
		return PathRules{}, err
	}
	return RemotePaths(info), nil
}

// movingLoads are the session's prompt resources, which move with the session (`move`, from the `Prompt` object's
// routine): loaded for the location it is at, and loaded anew after each move. Its loads follow the current location's.
type movingLoads struct {
	broadcast
	open func(p2runtime.Location) *promptLoads

	mu          sync.Mutex
	version     int
	inner       *promptLoads
	unsubscribe func()
}

func newMovingLoads(location *p2runtime.SessionLocation, open func(p2runtime.Location) *promptLoads) *movingLoads {
	m := &movingLoads{open: open, version: location.Version()}
	m.follow(open(location.Value()))
	return m
}

// follow makes inner the current loads: only the current loads publish (a superseded observer may still report, a reload
// on the old machine's connection which the move closes after this, while it closes).
func (m *movingLoads) follow(inner *promptLoads) {
	m.mu.Lock()
	m.inner = inner
	m.mu.Unlock()
	stop := inner.subscribe(func(value loaded) {
		m.mu.Lock()
		current := m.inner == inner
		m.mu.Unlock()
		if current {
			m.publish(value)
		}
	})
	m.mu.Lock()
	m.unsubscribe = stop
	m.mu.Unlock()
}

func (m *movingLoads) loads() *promptLoads {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner
}

func (m *movingLoads) current() *LoadedPrompt            { return m.loads().current() }
func (m *movingLoads) started(ctx context.Context) error { return m.loads().started(ctx) }
func (m *movingLoads) env(ctx context.Context) (durableenv.ExecutionEnv, error) {
	return m.loads().env(ctx)
}

// move loads them anew at `at` (from the new cwd, on the new machine) when `version` is a newer move; it returns once the
// old location's watches closed.
func (m *movingLoads) move(at p2runtime.Location, version int) {
	m.mu.Lock()
	if version <= m.version {
		m.mu.Unlock()
		return
	}
	m.version = version
	previous, stop := m.inner, m.unsubscribe
	m.mu.Unlock()
	stop()
	m.follow(m.open(at))
	previous.close()
}

func (m *movingLoads) close() {
	m.mu.Lock()
	inner, stop := m.inner, m.unsubscribe
	m.mu.Unlock()
	stop()
	inner.close()
}

// sessionFiles are where conversations find their prompt resources: the session's (following its moves) or their own location's.
type sessionFiles struct {
	session      *movingLoads
	environments func() (p2runtime.ExecutionEnvironments, error)
	open         func(p2runtime.Location) *promptLoads

	mu  sync.Mutex
	own map[string]*promptLoads
}

// of are the loads where conversation id works.
func (f *sessionFiles) of(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (loads, error) {
	environments, err := f.environments()
	if err != nil {
		return nil, err
	}
	at, err := environments.Locate(ctx, id, read)
	if err != nil {
		return nil, err
	}
	if p2runtime.SameLocation(at, environments.Session()) {
		return f.session, nil
	}
	key, _ := chordjson.Marshal([]any{at.Env, at.Cwd})
	f.mu.Lock()
	defer f.mu.Unlock()
	found, ok := f.own[string(key)]
	if !ok {
		found = f.open(at)
		f.own[string(key)] = found
	}
	return found, nil
}

func (f *sessionFiles) close() {
	f.session.close()
	f.mu.Lock()
	own := f.own
	f.own = nil
	f.mu.Unlock()
	for _, each := range own {
		each.close()
	}
}

// state is what the worker's `Prompt` object opens: the natives and the files.
type state struct {
	worker  *p2runtime.WorkerParts
	harness *p2runtime.HarnessParts
	files   *sessionFiles
}

// promptFilesParts is the open `Prompt`'s files (the native it contributes), for `PromptResources`.
type promptFilesParts struct {
	of func(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (loads, error)
}

// promptFilesNative is the registry of the open `Prompt`'s files (`p2.prompt-files`).
var promptFilesNative = p2runtime.NewNativeRegistry("p2.prompt-files")

// promptLocal is the session's prompt resources by location (worker, host-internal); its files are the promptFilesNative
// (`p2.prompt`; version 4: the files are a native).
var promptLocal = func() *contracts.Contract {
	contract, err := contracts.Local("p2.prompt", contracts.LocalDeclaration{
		Version: 4,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			// Expands `/template args` and `/skill:name args` where `conversation` works (see `PromptResources.expand`).
			contracts.Mem("expand", contracts.NewMethod(
				p2runtime.Contract(schema.Obj(schema.Properties{schema.Prop("text", schema.Str()), schema.Prop("conversation", schema.Num())})),
				p2runtime.Contract(schema.Obj(schema.Properties{
					schema.Prop("text", schema.Str()),
					schema.Opt("skill", schema.Obj(schema.Properties{schema.Prop("name", schema.Str())})),
				})),
			)),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// listedOf are the templates and skills of a load, as `PromptResources` shows them.
func listedOf(prompt *LoadedPrompt) *delta.JsonObject {
	templates := make([]any, len(prompt.Templates))
	for at, template := range prompt.Templates {
		info := delta.JsonObjectOf("name", template.Name, "description", template.Description)
		if template.ArgumentHint != "" {
			info.Set("argumentHint", template.ArgumentHint)
		}
		info.Set("source", template.Source)
		templates[at] = info
	}
	skills := make([]any, len(prompt.SkillFiles))
	for at, skill := range prompt.SkillFiles {
		skills[at] = delta.JsonObjectOf("name", skill.Name, "description", skill.Description)
	}
	return delta.JsonObjectOf("templates", templates, "skills", skills)
}

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}

// openState reads the natives and opens the session's files.
func openState(workerOf func() (*p2runtime.WorkerParts, error), harnessOf func() (*p2runtime.HarnessParts, error)) (*state, error) {
	worker, err := workerOf()
	if err != nil {
		return nil, err
	}
	harness, err := harnessOf()
	if err != nil {
		return nil, err
	}
	environments := func() (p2runtime.ExecutionEnvironments, error) { return harness.Environments() }
	open := func(at p2runtime.Location) *promptLoads { return newPromptLoads(worker.Init.DataDir, at, environments) }
	value := &state{worker: worker, harness: harness, files: &sessionFiles{environments: environments, open: open, own: map[string]*promptLoads{}}}
	value.files.session = newMovingLoads(worker.Location, open)
	return value, nil
}

func servePrompt(self host.Self) (contracts.Implementation, error) {
	store := self.(host.StoreSelf)
	// The natives are declared here and read when the files open.
	workerOf, harnessOf := p2runtime.WorkerOf(self), p2runtime.HarnessOf(self)
	// The state is kept when the files open; what needs it waits for `ready`.
	var (
		stateMu   sync.Mutex
		opened    *state
		openError error
	)
	ready := make(chan struct{})
	self.Open(
		func(context.Context) (any, error) {
			defer close(ready)
			value, err := openState(workerOf, harnessOf)
			stateMu.Lock()
			opened, openError = value, err
			stateMu.Unlock()
			if err != nil {
				return nil, err
			}
			return value, nil
		},
		func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*state).files.close()
			return nil
		},
	)
	get := func(ctx context.Context) (*state, error) {
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
		stateMu.Lock()
		defer stateMu.Unlock()
		if opened == nil {
			return nil, openError
		}
		return opened, nil
	}
	// The session's resources follow its moves (one committed while the files opened included).
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		current, err := get(ctx)
		if err != nil {
			return host.Threw(err)
		}
		location := current.worker.Location
		// Listening before moving: a move committed while the old watches close is not missed.
		moved := make(chan struct{}, 1)
		stop := location.Subscribe(func(p2runtime.Location) {
			select {
			case moved <- struct{}{}:
			default:
			}
		})
		return host.AwaitingCtx(ctx, func() error {
			defer stop()
			for ctx.Err() == nil {
				current.files.session.move(location.Value(), location.Version())
				select {
				case <-moved:
				case <-ctx.Done():
				}
			}
			return nil
		})
	})
	// Load failures of the session's resources go to the worker's log (they also leave the last good load in use).
	self.OnLive(func(ctx host.Ctx) *host.Handled {
		current, err := get(ctx)
		if err != nil {
			return host.Threw(err)
		}
		return host.AwaitingCtx(ctx, func() error {
			stop := current.files.session.subscribe(func(value loaded) {
				if value.err != "" {
					fmt.Fprintln(os.Stderr, "p2 prompt: "+value.err)
				}
			})
			defer stop()
			<-ctx.Done()
			return nil
		})
	})
	store.Contribute(promptFilesNative, &promptFilesParts{
		of: func(ctx context.Context, id durable.ConversationId, read durable.DocumentReader) (loads, error) {
			current, err := get(ctx)
			if err != nil {
				return nil, err
			}
			return current.files.of(ctx, id, read)
		},
	})
	// The latest load where the conversation works. On this machine its first load is awaited (the session's is the input
	// gate's); a remote one loads in the background, so an offline host does not block a turn.
	load := func(ctx context.Context, input durable.PromptInput) (*LoadedPrompt, error) {
		current, err := get(ctx)
		if err != nil {
			return nil, err
		}
		found, err := current.files.of(ctx, input.ConversationId, input.Read)
		if err != nil {
			return nil, err
		}
		environments, err := current.files.environments()
		if err != nil {
			return nil, err
		}
		at, err := environments.Locate(ctx, input.ConversationId, input.Read)
		if err != nil {
			return nil, err
		}
		if at.Env == nil {
			if err := found.started(ctx); err != nil {
				return nil, err
			}
		}
		return found.current(), nil
	}
	// What the conversation's prompt shows: a locked session gets the global instructions only when picked.
	visible := func(ctx context.Context, input durable.PromptInput) (*Resources, error) {
		current, err := load(ctx, input)
		if err != nil {
			return nil, err
		}
		lock, err := p2runtime.LockOf(ctx, input.Read)
		if err != nil {
			return nil, err
		}
		if lock != nil && !lock.Instructions && current.Project != nil {
			return current.Project, nil
		}
		return &current.Resources, nil
	}
	untagged := false
	text := func(pick func(*Resources) string) func(context.Context, durable.PromptInput) (*string, error) {
		return func(ctx context.Context, input durable.PromptInput) (*string, error) {
			resources, err := visible(ctx, input)
			if err != nil || pick(resources) == "" {
				return nil, err
			}
			value := pick(resources)
			return &value, nil
		}
	}
	// Sections are rendered for every generation, for the conversation's location: they read its latest load.
	extension := &p2runtime.P2Extension{Extension: &durable.Extension{
		Name: "p2-prompt",
		Sections: []*durable.PromptSection{
			durableharness.Section("app", func(ctx context.Context, input durable.PromptInput) (*string, error) {
				current, err := get(ctx)
				if err != nil {
					return nil, err
				}
				environments, err := current.files.environments()
				if err != nil {
					return nil, err
				}
				at, err := environments.Locate(ctx, input.ConversationId, input.Read)
				if err != nil {
					return nil, err
				}
				guide := AppGuide(current.worker.Init.App, current.worker.Init.DataDir, at.Env)
				return &guide, nil
			}),
			durableharness.Section("agents-md", text(func(r *Resources) string { return r.Agents }), durableharness.SectionOptions{Tag: &untagged}),
			durableharness.Section("skills", text(func(r *Resources) string { return r.Skills }), durableharness.SectionOptions{Tag: &untagged}),
		},
	}}
	p2runtime.ContributeKeyed(store, p2runtime.Extensions, p2runtime.Entry(extension.Extension.Name, extension))
	// A session's first message waits for its instructions (the first load).
	p2runtime.ContributeKeyed(store, p2runtime.InputGates, p2runtime.Entry("prompt", p2runtime.InputGate(func(ctx host.Ctx) error {
		current, err := get(ctx)
		if err != nil {
			return err
		}
		if current.worker.Location.Value().Env != nil {
			return nil
		}
		return current.files.session.started(ctx)
	})))
	return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
		// Skill files are read when invoked, through the conversation's environment (local or remote).
		"expand": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
			in := objectOf(args)
			conversation, _ := in.Value("conversation").(float64)
			chordctx.Suspend(ctx)
			current, err := get(ctx)
			if err != nil {
				return nil, err
			}
			found, err := current.files.of(ctx, durable.ConversationId(conversation), nil)
			if err != nil {
				return nil, err
			}
			latest := found.current()
			environment, err := found.env(ctx)
			if err != nil {
				return nil, err
			}
			paths, err := pathsOf(ctx, environment)
			if err != nil {
				return nil, err
			}
			reader := EnvFiles(environment, paths)
			expanded, err := ExpandPrompt(ctx, stringOf(in.Value("text")), latest.Templates, latest.SkillFiles, func(ctx context.Context, skill SkillFile) (string, bool, error) {
				return reader.ReadText(ctx, skill.Path)
			})
			if err != nil {
				return nil, err
			}
			answer := delta.JsonObjectOf("text", expanded.Text)
			if expanded.Skill != nil {
				answer.Set("skill", delta.JsonObjectOf("name", *expanded.Skill))
			}
			return answer, nil
		},
	}}}, nil
}

// servePromptResources serves `PromptResources`: participants see the templates and skills where the conversation works and
// expand with them (viewers cannot send).
func servePromptResources(params *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
	who, err := p2runtime.FromChord(principal)
	if err != nil || !p2runtime.CanSend(who, stringOf(params.Value("session"))) {
		return contracts.Implementation{}, p2runtime.Forbidden("participants only")
	}
	conversation, _ := params.Value("conversation").(float64)
	prompt := self.Get(promptLocal, delta.NewJsonObject(0))
	native := p2runtime.NativeOf[promptFilesParts](self.Registry(promptFilesNative), "The prompt files")
	// The loads where the conversation works, followed: each new load (a changed fingerprint) is the next value.
	listed := self.Follow(func(observer host.FollowObserver[any], ctx context.Context) (*host.FollowAttachment[delta.JsonValue], error) {
		parts, err := native()
		if err != nil {
			return nil, err
		}
		found, err := parts.of(ctx, durable.ConversationId(conversation), nil)
		if err != nil {
			return nil, err
		}
		var mu sync.Mutex
		fingerprint, attached := "", false
		stop := found.subscribe(func(value loaded) {
			if value.prompt == nil {
				return
			}
			mu.Lock()
			if !attached || value.prompt.Fingerprint == fingerprint {
				mu.Unlock()
				return
			}
			fingerprint = value.prompt.Fingerprint
			mu.Unlock()
			next := listedOf(value.prompt)
			observer.Advance(chord.WatchCall[any]{Kind: chord.WatchCallValue, Value: next, Ops: []delta.Op{{"r", next}}}, ctx)
		})
		mu.Lock()
		initial := found.current()
		fingerprint, attached = initial.Fingerprint, true
		mu.Unlock()
		return &host.FollowAttachment[delta.JsonValue]{Value: listedOf(initial), Detach: stop}, nil
	})
	return contracts.Implementation{Variant: "participant", Members: contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"templates": self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("templates"), nil }),
			"skills":    self.Derive([]any{listed}, func(values []any) (delta.JsonValue, error) { return objectOf(values[0]).Value("skills"), nil }),
		},
		Methods: map[string]contracts.MethodFunction{
			"expand": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				return prompt.Call("expand", delta.JsonObjectOf("text", objectOf(args).Value("text"), "conversation", float64(conversation)), ctx)
			},
		},
	}}, nil
}

// WorkerFacet serves the worker's parts: the session's `Prompt` and the participants' `PromptResources`.
var WorkerFacet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := p2runtime.ServeNative[promptFilesParts](facet, promptFilesNative); err != nil {
		return err
	}
	if err := facet.Serve(promptLocal, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) { return servePrompt(self) }), host.ServeOptions{}); err != nil {
		return err
	}
	return facet.Serve(contract.PromptResources, host.RemoteFactory(servePromptResources), host.ServeOptions{})
})
