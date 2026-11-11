package surfaces

import (
	"context"
	"errors"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	next "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	"github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports packages/chord/src/node/surfaces/surface-build.ts. A client surface's build (owner decision "A" on startup cost): its base
// bundle and its kind's first published set, made off the server's start: the kit starts it right after the server listens, and
// every request for the page or a surface file waits for it (and starts it when nothing did). A base that does not build fails only
// what needs it: those requests answer 503, the server runs on. Built from the real files at every start.
//
// Forced Go differences: Pi's `Promise<Outcome>` stored and awaited by several parties is a once-started goroutine and a channel
// closed when the outcome is set; a waiter's context ends only its own wait (Pi's awaits take none, but a request's abort and a
// rollout's are Go's contexts). A build in esbuild cannot be cancelled, and, as in Pi, what it finishes after the server closed is
// dropped. Where Pi leaves the build's Promise unawaited, the goroutine is owned: the app's close does not wait for it (close, which
// only stops a build not started yet from starting), and the app's Wait joins it (wait), bounded by its context.

// SurfaceBuilt is how a surface's build ended: built (its base bundle's hash and the files it read), or failed (esbuild's failure,
// or the first set's).
type SurfaceBuilt struct {
	// Kind is `built` or `failed` (Pi's discriminator `kind`): Error is set exactly when it is failed.
	Kind SurfaceBuiltKind
	// Base is the base bundle's hash; Inputs the files it was built from, with their bytes' hashes.
	Base   string
	Inputs model.BaseInputs
	// Error is why it failed: none built.
	Error error
}

// SurfaceBuiltKind is how a surface's build ended.
type SurfaceBuiltKind string

const (
	// SurfaceBuiltKindBuilt is a build that made the base bundle and published the first set.
	SurfaceBuiltKindBuilt SurfaceBuiltKind = "built"
	// SurfaceBuiltKindFailed is a build that did not.
	SurfaceBuiltKindFailed SurfaceBuiltKind = "failed"
)

// surfaceBuildOptions is what a surface builds: its kind, entry, conditions and the plugins component.
type surfaceBuildOptions struct {
	kind       string
	entry      string
	conditions []string
	plugins    *plugins.Plugins
}

// buildOutcome is a surface build's parts, or why there are none.
type buildOutcome struct {
	base      *BaseBundle
	published *PublishedScripts
	err       error
}

// surfaceBuild is a surface's base bundle and first set, built once, after the server's load.
type surfaceBuild struct {
	surface  SurfaceName
	platform BasePlatform
	prefix   string
	options  surfaceBuildOptions
	server   host.Host

	// done is closed once the outcome is set.
	done chan struct{}

	mu sync.Mutex
	// running is set once the build started (its goroutine, or its failed outcome after the close); closed once close ran: a
	// build not started by then never starts.
	running, closed bool
	outcome         *buildOutcome
	// live is the published scripts once they took the first set (before it is published: the scripts object starts there).
	live *PublishedScripts
}

func newSurfaceBuild(surface SurfaceName, platform BasePlatform, prefix string, options surfaceBuildOptions, server host.Host) *surfaceBuild {
	return &surfaceBuild{surface: surface, platform: platform, prefix: prefix, options: options, server: server, done: make(chan struct{})}
}

// set is the set published now: the kind's first one once taken, an empty one before (no client holds it then: the page and the
// start wait).
func (b *surfaceBuild) set() next.ScriptSet {
	b.mu.Lock()
	live := b.live
	b.mu.Unlock()
	if live == nil {
		return next.ScriptSet{}
	}
	return live.Set()
}

// built is how the build ended, once it did (nil before).
func (b *surfaceBuild) built() *SurfaceBuilt {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.outcome == nil {
		return nil
	}
	return publicOutcome(b.outcome)
}

// build builds the base bundle and publishes the kind's first set (its plugins' facets built on first use) to the scripts object,
// once (after the server's load: the object must be served). Shared by every waiter: no caller's abort ends it (the server's close
// fails it). It returns how it ended.
func (b *surfaceBuild) build() SurfaceBuilt {
	b.start()
	<-b.done
	return *b.built()
}

// errClosed is why a build that was not started when the surface closed failed.
var errClosed = errors.New("the surface was closed before its build started")

// start starts the build when nothing did; after close, a build not started fails at once, with no goroutine.
func (b *surfaceBuild) start() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return
	}
	b.running = true
	if b.closed {
		b.outcome = &buildOutcome{err: errClosed}
		close(b.done)
		return
	}
	//portlint:allow golifetime the build is shared by every waiter and ends with the base bundle's; esbuild cannot be cancelled, so the goroutine ends when the build does: wait joins it, bounded by its context
	go func() {
		outcome := b.run(chordctx.Background())
		b.mu.Lock()
		b.outcome = outcome
		b.mu.Unlock()
		close(b.done)
	}()
}

// close ends the surface's builds without waiting: one not started never starts (a later request answers 503), one in flight goes on
// (its result dropped as the server's close fails its publication).
func (b *surfaceBuild) close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
}

// wait returns once the build, if started, ended, or with ctx's error when ctx ends first (the goroutine is left to finish).
func (b *surfaceBuild) wait(ctx context.Context) error {
	b.mu.Lock()
	running := b.running
	b.mu.Unlock()
	if !running {
		return nil
	}
	select {
	case <-b.done:
		return nil
	default:
	}
	select {
	case <-b.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ready waits for the build (started when nothing did) as long as ctx lasts.
func (b *surfaceBuild) ready(ctx context.Context) (*buildOutcome, error) {
	b.start()
	select {
	case <-b.done:
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.outcome, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// file is the response for a file of the built surface (its base bundle's, a published set's), 503 when the build failed.
func (b *surfaceBuild) file(path string, ctx context.Context) (http.HttpResponse, error) {
	outcome, err := b.ready(ctx)
	if err != nil {
		return http.HttpResponse{}, err
	}
	if outcome.err != nil {
		return http.HttpResponse{Kind: http.KindError, Status: 503}, nil
	}
	if found, ok := outcome.base.Files[path]; ok {
		return Served(&found), nil
	}
	return Served(outcome.published.File(path)), nil
}

// parts are the built parts for a request (the build started when nothing did); nil when it failed.
func (b *surfaceBuild) parts(ctx context.Context) (*buildOutcome, error) {
	outcome, err := b.ready(ctx)
	if err != nil || outcome.err != nil {
		return nil, err
	}
	return outcome, nil
}

// step is the rollout step publishing the kind's current set, after the build: a failed build fails the step's one host (name),
// whose plugins stay dirty; the rollout's abort ends its wait for the build (the step fails, the rollout stops there; the build
// goes on).
func (b *surfaceBuild) step(name string) plugins.RolloutStep {
	return plugins.RolloutStep{
		Name: name, Kinds: []string{b.options.kind},
		Reload: func(built *plugins.Built, applied map[string][]plugins.PluginScript, ctx context.Context) (plugins.StepResult, error) {
			outcome, err := b.ready(ctx)
			if err != nil {
				return plugins.StepResult{}, err
			}
			if outcome.err != nil {
				return plugins.StepResult{Hosts: []plugins.RolloutHost{{Kind: plugins.RolloutErrored, Host: name, Error: outcome.err}}}, nil
			}
			return outcome.published.Step(name).Reload(built, applied, ctx)
		},
	}
}

// run builds the base bundle, takes the kind's current set as the first and publishes it.
func (b *surfaceBuild) run(ctx context.Context) *buildOutcome {
	base, err := BuildBase(b.options.entry, b.platform, b.prefix, b.options.conditions)
	if err != nil {
		return &buildOutcome{err: err}
	}
	published := NewPublishedScripts(b.surface, b.options.kind, b.prefix, base.Hash, b.options.plugins, b.server)
	if _, err := published.Start(ctx); err != nil {
		return &buildOutcome{err: err}
	}
	b.mu.Lock()
	b.live = published
	b.mu.Unlock()
	if err := PublishTo(b.server, b.surface, published.Set(), ctx); err != nil {
		return &buildOutcome{err: err}
	}
	return &buildOutcome{base: base, published: published}
}

// publicOutcome is outcome as callers see it.
func publicOutcome(outcome *buildOutcome) *SurfaceBuilt {
	if outcome.err != nil {
		return &SurfaceBuilt{Kind: SurfaceBuiltKindFailed, Error: outcome.err}
	}
	return &SurfaceBuilt{Kind: SurfaceBuiltKindBuilt, Base: outcome.base.Hash, Inputs: outcome.base.Inputs}
}
