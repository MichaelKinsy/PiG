package host

// Ports packages/chord/src/reload.ts.
//
// Async mapping: Pi's reload is one async function on the host's event loop. Here a reload runs off the loop (the
// blocking calls of runReload and the coordinator), every synchronous section between two of Pi's awaits runs as one
// host.run section, and each await is a blocking call made off the loop.

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// defaultDeadline is Pi's 5000 ms default of every reload deadline.
const defaultDeadline = 5000 * time.Millisecond

// ReloadOptions are the options of HostImpl.Reload (Pi reload.ts:21-34). A nil deadline is Pi's undefined (5000 ms;
// the vote deadline defaults to gate + retire + 5000 ms); a nil map is undefined.
type ReloadOptions struct {
	// Remove names the facets to remove.
	Remove []string
	// GateDeadline is how long the gate waits for grandfathered roots. Missed: the reload aborts.
	GateDeadline *time.Duration
	// RetireDeadline is how long to wait for the previous code version to retire first. Missed: it is retired anyway.
	RetireDeadline *time.Duration
	// Remote is, on a server, the facet modules for child hosts by host-name prefix (longest wins). Every connected
	// host participates.
	Remote map[string][]string
	// RemoteRemove is, on a server, the facet names to remove on child hosts, by the same host-name prefix rules.
	RemoteRemove map[string][]string
	// VoteDeadline is, on a server, how long to wait for every child's vote.
	VoteDeadline *time.Duration
}

// ReloadReport is what a reload committed (Pi reload.ts:36-50).
type ReloadReport struct {
	CodeVersion int
	Previous    int
	Changed     []string
	Removed     []string
	// Prepared are the addresses of objects whose new factory was installed.
	Prepared []string
	// Gated are the addresses of objects that went through the gate.
	Gated []string
	// Children is, for cluster reloads, what each participating child host prepared, by host name (its vote; the child
	// commits it after the server). Nil for local reloads.
	Children map[string]ReloadReport
}

type candidate struct {
	object  *ObjectImpl
	service *Service
	run     *Run
	// reasons say why the object needs the gate (empty: compatible).
	reasons []string
	// atGate are the new exclusive resources and their new dependents, opened at the gate.
	atGate []*ResourceImpl
	// replaced are the old exclusive resources closed at the gate.
	replaced []*ResourceImpl
	migrated *orderedMap[*Cell[any], any]
	// derived are the values of the run's derivations over the candidate (migrated) values, validated.
	derived map[any]any
	// staged are the listener subscriptions made at the end of prepare, activated by the commit.
	staged *StagedListeners
}

// deadlineOr is Pi's `deadline ?? 5000`.
func deadlineOr(deadline *time.Duration) time.Duration {
	if deadline == nil {
		return defaultDeadline
	}
	return *deadline
}

// formatMs writes a deadline as Pi's template literal writes its millisecond number.
func formatMs(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64)
}

// dryRun computes the new derivation functions over the candidate values and validates every prospective value
// (migrated cells and derivations) against every schema that will apply after the commit: the cell's new schema and
// the new implementation's remote state-member schemas. A failure fails prepare; the commit then installs without
// re-checking (Pi reload.ts:71-95).
func dryRun(candidate *candidate) error {
	run, migrated, service, object := candidate.run, candidate.migrated, candidate.service, candidate.object
	exposed := memberSchemas(service, run.implementation)
	for _, cell := range migrated.Keys() {
		value, _ := migrated.Get(cell)
		for _, schema := range exposed[cell] {
			if err := assertValid("state", fmt.Sprintf("%s: migrated state %s", object.address, cell.Key), schema, value); err != nil {
				return err
			}
		}
	}
	values := map[any]any{}
	for _, cell := range migrated.Keys() {
		values[cell], _ = migrated.Get(cell)
	}
	names := run.decls.derives.Keys()
	// Array.prototype.sort is stable.
	slices.SortStableFunc(names, func(a, b string) int {
		left, _ := run.decls.derives.Get(a)
		right, _ := run.decls.derives.Get(b)
		return cmp.Compare(left.Seq, right.Seq)
	})
	for _, name := range names {
		derived, _ := run.decls.derives.Get(name)
		value, err := derived.Compute(func(source Readable[any]) any {
			if value, ok := values[source]; ok {
				return value
			}
			return candidateValue(source)
		})
		if err != nil {
			return err
		}
		for _, schema := range exposed[derived] {
			if err := assertValid("state", fmt.Sprintf("%s: derivation %s", object.address, name), schema, value); err != nil {
				return err
			}
		}
		values[derived] = value
	}
	candidate.derived = values
	return nil
}

// candidateValue is a source's value for a dry run. A reference to a provider this reload upgrades too reads nothing
// before the commit (its new contract does not bind the old code): the dry run computes without it, the commit with the
// real value (reload.ts candidateValue).
func candidateValue(source Readable[any]) any {
	value, err := readValue(source)
	if err != nil {
		if chordError, ok := err.(*ChordError); ok && (chordError.Code == CodeNotReady || chordError.Code == CodeNotInVariant) { //nolint:errorlint // Pi's isChordError does not unwrap
			return nil
		}
		panic(err)
	}
	return value
}

var reloadAbortedPrefix = regexp.MustCompile(`^[^:]*: `)

// reloadError keeps the underlying code (contract_mismatch, invalid, ...) of err and says it aborted the reload (Pi
// reload.ts:97-106).
func reloadError(err error) *ChordError {
	cause := asChordError(err, "reload")
	if strings.HasPrefix(reloadAbortedPrefix.ReplaceAllString(cause.Message, ""), "reload aborted") {
		return cause
	}
	return NewChordError(cause.Code, "reload aborted: "+cause.Message, ChordErrorOptions{Cause: cause, Reason: cause.Reason, Details: cause.Details})
}

// runReload runs one local reload; a second reload while one runs is rejected. It blocks and is called off the loop
// (Pi reload.ts:108-121).
func runReload(host *HostImpl, plugins []PluginRef, options ReloadOptions) (ReloadReport, error) {
	reload, err := beginReload(host, plugins, options, nil)
	if err != nil {
		return ReloadReport{}, err
	}
	defer endReload(host)
	if err := reload.prepare(); err != nil {
		return ReloadReport{}, err
	}
	var report ReloadReport
	host.run(func() { report, err = reload.commit(nil) })
	return report, err
}

// beginReload takes the host's reload slot and loads the plugin modules. Pair it with endReload. It blocks and is
// called off the loop (Pi reload.ts:123-140).
func beginReload(host *HostImpl, plugins []PluginRef, options ReloadOptions, codeVersion *int) (*Reload, error) {
	var running bool
	host.run(func() {
		running = host.reloading
		host.reloading = true
	})
	if running {
		return nil, NewChordError(CodeUnavailable, "a reload is already running", ChordErrorOptions{Reason: "reload_running"})
	}
	facets, err := host.resolve(plugins)
	if err != nil {
		host.run(host.reloadEnded)
		return nil, reloadError(err)
	}
	return newReload(host, facets, options, codeVersion), nil
}

// endReload releases the host's reload slot (Pi reload.ts:142-145).
func endReload(host *HostImpl) {
	host.run(host.reloadEnded)
}

// reloadBetweenPreparePasses, when set (package tests only), runs off the loop between prepare's two passes. Pi's
// second pass (reload.ts:179-183) exists for activations that start in the microtask between the first pass settling
// and the gating assignment; a test reaches that window through this hook. It is nil in production.
var reloadBetweenPreparePasses func(host *HostImpl)

// Reload is one reload on one host: prepare (load, prepare, gate), then commit or rollback (Pi reload.ts:147-659).
// Its state is touched on the host's loop.
type Reload struct {
	host    *HostImpl
	facets  []Facet
	options ReloadOptions
	// code version is the code version id to commit (issued by the coordinating server); nil: a local reload allocates one.
	codeVersion *int
	candidates  *orderedMap[*ObjectImpl, *candidate]
	abort       context.Context
	abortWith   context.CancelCauseFunc
	closed      []closedResource
	services    *orderedMap[string, *Service]
	changed     *orderedMap[string, struct{}]
	removed     *orderedMap[string, struct{}]
	tables      map[string]RegistryTable[any]
	gated       []*candidate
	rolledBack  bool
	// deferred are the objects of types this reload adds, with their staged listeners: their runtime starts at
	// commit.
	deferred *orderedMap[*ObjectImpl, *StagedListeners]
	// facetRuns is this reload's run of each facet it loads (FacetApi.Signal): installed at commit, cancelled at
	// rollback.
	facetRuns *orderedMap[string, *facetRun]
	// starts is work that may start new-code version roots, run after installation (and after the coordinator's commit
	// frames).
	starts []func()
	// stops are the aborts of the tasks install stopped, applied after the commit (see commit).
	stops []func()
}

type closedResource struct {
	object   *ObjectImpl
	resource *ResourceImpl
}

func newReload(host *HostImpl, facets []Facet, options ReloadOptions, codeVersion *int) *Reload {
	//portlint:allow ctxbackground reload.ts:155 `readonly abort = new AbortController()`: the reload's own controller
	abort, abortWith := context.WithCancelCause(context.Background())
	return &Reload{
		host:        host,
		facets:      facets,
		options:     options,
		codeVersion: codeVersion,
		candidates:  newOrderedMap[*ObjectImpl, *candidate](),
		abort:       abort,
		abortWith:   abortWith,
		services:    newOrderedMap[string, *Service](),
		changed:     newOrderedMap[string, struct{}](),
		removed:     newOrderedMap[string, struct{}](),
		tables:      map[string]RegistryTable[any]{},
		deferred:    newOrderedMap[*ObjectImpl, *StagedListeners](),
		facetRuns:   newOrderedMap[string, *facetRun](),
	}
}

// section runs fn as one synchronous section on the host's loop and returns its error.
func (r *Reload) section(fn func() error) error {
	var err error
	r.host.run(func() { err = fn() })
	return err
}

// prepare runs everything up to the commit. On failure the host is rolled back and the underlying error is returned
// as a reload error. It blocks and is called off the loop (Pi reload.ts:173-193).
func (r *Reload) prepare() error {
	if err := r.prepareSteps(); err != nil {
		r.rollback()
		return reloadError(err)
	}
	return nil
}

func (r *Reload) prepareSteps() error {
	r.waitForRetirement()
	if err := r.section(func() error {
		if err := r.load(); err != nil {
			return err
		}
		return r.checkProviders()
	}); err != nil {
		return err
	}
	if err := r.prepareAll(); err != nil {
		return err
	}
	if reloadBetweenPreparePasses != nil {
		reloadBetweenPreparePasses(r.host)
	}
	// From here new objects of changed types are rejected (upgrade_conflict, retryable) until commit or abort, so the
	// candidate set is final once the activations already in flight have been prepared.
	r.host.run(func() {
		gating := map[string]bool{}
		for _, name := range r.changed.Keys() {
			gating[name] = true
		}
		r.host.gating = gating
	})
	if err := r.withDeadline(r.prepareAll, "activations in flight did not finish"); err != nil {
		return err
	}
	if err := r.section(func() error {
		if err := r.checkRefs(); err != nil {
			return err
		}
		tables, err := r.host.buildTables(r.entries())
		r.tables = tables
		return err
	}); err != nil {
		return err
	}
	if err := r.gate(); err != nil {
		return err
	}
	return r.section(func() error {
		if err := r.alive(); err != nil {
			return err
		}
		return r.stage()
	})
}

// summary is what a successful prepare will commit (a child sends it with its vote). It runs on the loop (Pi
// reload.ts:195-206).
func (r *Reload) summary() ReloadReport {
	codeVersion := r.host.current.id
	if r.host.pending != nil {
		codeVersion = r.host.pending.id
	}
	report := ReloadReport{
		CodeVersion: codeVersion,
		Previous:    r.host.current.id,
		Changed:     r.changed.Keys(),
		Removed:     r.removed.Keys(),
		Prepared:    []string{},
		Gated:       r.gatedAddresses(),
	}
	for _, candidate := range r.candidates.Values() {
		if candidate.object.status == "live" {
			report.Prepared = append(report.Prepared, candidate.object.address)
		}
	}
	return report
}

func (r *Reload) gatedAddresses() []string {
	addresses := make([]string, 0, len(r.gated))
	for _, candidate := range r.gated {
		addresses = append(addresses, candidate.object.address)
	}
	return addresses
}

// alive is checked after every await of prepare: a rollback meanwhile (abort from the coordinator) ends it (Pi
// reload.ts:208-211).
func (r *Reload) alive() error {
	if r.rolledBack {
		return NewChordError(CodeAborted, "reload aborted while preparing", ChordErrorOptions{Reason: "aborted"})
	}
	return nil
}

// stage subscribes every new listener now, buffering its events, so a failing source fails prepare instead of
// leaving a half-installed code version. Objects running only the new code (types this reload adds) are staged too (Pi
// reload.ts:213-226).
func (r *Reload) stage() error {
	for _, candidate := range r.candidates.Values() {
		if candidate.object.status == "live" {
			staged, err := candidate.object.stageListeners(candidate.run.decls)
			if err != nil {
				return err
			}
			candidate.staged = staged
		}
	}
	for _, object := range r.host.objects.Values() {
		if object.status == "live" && object.deferredRuntime {
			staged, err := object.stageListeners(object.decls)
			if err != nil {
				return err
			}
			r.deferred.Set(object, staged)
		}
	}
	return nil
}

// abortedRootsGrace is the most a forced retirement waits for aborted roots to settle before it closes what they used.
const abortedRootsGrace = time.Second

// until blocks until ready() holds (checked on the loop whenever retirement-relevant state changes), true; or for d,
// false (Pi reload.ts until). It runs off the loop.
func until(host *HostImpl, ready func() bool, d time.Duration) bool {
	changed := make(chan struct{}, 1)
	var stop func()
	var holds bool
	host.run(func() {
		if holds = ready(); holds {
			return
		}
		stop = host.watchRetirement(func() {
			if ready() {
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		})
	})
	if holds {
		return true
	}
	timer := time.NewTimer(nodeDelay(d))
	defer timer.Stop()
	defer host.run(stop)
	select {
	case <-changed:
		return true
	case <-timer.C:
		return false
	}
}

// waitForRetirement retires the previous code version before the next one is loaded (a host keeps two). It waits until the
// previous code version's roots ended and, on a child host, its parent retired it too (retire frame), at most the retire
// deadline. Past the deadline it is retired anyway: its remaining roots are aborted first and get a short grace to
// stop, so they see cancellation before the resources only that code version used are closed; their next call is superseded
// (Pi reload.ts:228-242).
func (r *Reload) waitForRetirement() {
	host := r.host
	var previous *CodeVersionState
	host.run(func() { previous = host.previous })
	if previous == nil {
		return
	}
	deadline := deadlineOr(r.options.RetireDeadline)
	gone := func() bool { return host.previous != previous }
	if !until(host, func() bool { return gone() || host.retirable() }, deadline) {
		var forced bool
		host.run(func() {
			if forced = !gone(); forced {
				previous.abortWith(NewChordError(CodeSuperseded, fmt.Sprintf("code version %d was retired", previous.id), ChordErrorOptions{}))
			}
		})
		if forced {
			until(host, func() bool { return gone() || len(previous.roots) == 0 }, min(deadline, abortedRootsGrace))
		}
	}
	host.run(func() {
		if !gone() {
			host.retire()
		}
	})
}

// load builds the candidate service table: unchanged services plus everything the given facets serve. It runs on
// the loop (Pi reload.ts:244-263).
func (r *Reload) load() error {
	replaced := map[string]bool{}
	for _, facet := range r.facets {
		replaced[facet.Name] = true
	}
	for _, name := range r.options.Remove {
		replaced[name] = true
	}
	current := r.host.current.services
	r.services = newOrderedMap[string, *Service]()
	for _, name := range current.Keys() {
		service, _ := current.Get(name)
		if !replaced[service.facet] {
			r.services.Set(name, service)
		}
	}
	for _, facet := range r.facets {
		name := facet.Name
		serve := Serve(func(typ *Contract, factory Factory, options ServeOptions) error {
			service, err := makeService(typ, factory, options, name)
			if err != nil {
				return err
			}
			return r.host.serveInto(r.services, service)
		})
		run := newFacetRun()
		r.facetRuns.Set(name, run)
		if err := facet.Setup(r.host.facetApi(serve, run.signal)); err != nil {
			return err
		}
	}
	for _, name := range r.services.Keys() {
		service, _ := r.services.Get(name)
		if old, ok := current.Get(name); !ok || old != service {
			r.changed.Set(name, struct{}{})
		}
	}
	for _, name := range current.Keys() {
		if !r.services.Has(name) {
			r.removed.Set(name, struct{}{})
		}
	}
	id, err := r.host.nextCodeVersionId(r.codeVersion)
	if err != nil {
		return err
	}
	r.host.pending = newCodeVersionState(id, r.services)
	return nil
}

// withDeadline runs fn, failing with `aborted` once the gate deadline passes first. Like Pi's Promise.race, fn keeps
// running after the deadline, as a task the host owns (Pi reload.ts:265-279).
func (r *Reload) withDeadline(fn func() error, what string) error {
	deadline := deadlineOr(r.options.GateDeadline)
	done := make(chan error, 1)
	r.host.goTask(func() { done <- fn() })
	timer := time.NewTimer(nodeDelay(deadline))
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return NewChordError(CodeAborted, fmt.Sprintf("%s within %s ms", what, formatMs(deadline)), ChordErrorOptions{Reason: "deadline"})
	}
}

// checkProviders: the same contract version must cover the old description (forgotten bump); facets' uses must bind.
// It runs on the loop (Pi reload.ts:281-305).
func (r *Reload) checkProviders() error {
	for _, name := range r.changed.Keys() {
		service, _ := r.services.Get(name)
		next := service.contract
		old, ok := r.host.current.services.Get(name)
		if !ok || old.contract.Version != next.Version {
			continue
		}
		if reason := Coverage(next.Description, old.contract.Description); reason != "" {
			return NewChordError(CodeContractMismatch, fmt.Sprintf("%s@%d changed incompatibly without a version bump (%s)", name, next.Version, reason), ChordErrorOptions{})
		}
	}
	facets := newOrderedMap[string, Facet]()
	for _, name := range r.host.facets.Keys() {
		facet, _ := r.host.facets.Get(name)
		facets.Set(name, facet)
	}
	for _, facet := range r.facets {
		facets.Set(facet.Name, facet)
	}
	for _, name := range r.options.Remove {
		facets.Delete(name)
	}
	for _, facet := range facets.Values() {
		for _, token := range facet.Uses {
			service, ok := r.services.Get(token.Name)
			if ok && r.changed.Has(token.Name) {
				if _, err := Bind(service.contract, service.options.Accepts, token); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// prepareAll prepares every live object of a changed type, including objects that activate meanwhile, to a
// fixpoint. Objects already running the new code (types this reload adds) are skipped. It blocks and is called off
// the loop (Pi reload.ts:307-330).
func (r *Reload) prepareAll() error {
	for {
		var todo []*ObjectImpl
		var ready []*Promise[struct{}]
		r.host.run(func() {
			for _, object := range r.host.objects.Values() {
				service, _ := r.services.Get(object.typeName)
				if r.changed.Has(object.typeName) && !r.candidates.Has(object) && object.service != service &&
					(object.status == "live" || object.status == "activating") {
					todo = append(todo, object)
					ready = append(ready, object.ready)
				}
			}
		})
		if len(todo) == 0 {
			return nil
		}
		// Promise.allSettled: every activation settles; failures are the activation's own.
		//portlint:allow ctxbackground reload.ts:321 awaits Promise.allSettled over every pending object's ready, which has no abort
		_, _ = promiseAllSettled(&r.host.loop, ready).Await(context.Background())
		if err := r.section(r.alive); err != nil {
			return err
		}
		for _, object := range todo {
			var prepare bool
			r.host.run(func() {
				service, _ := r.services.Get(object.typeName)
				prepare = object.status == "live" && object.service != service
			})
			if !prepare {
				continue
			}
			if err := r.prepareObject(object); err != nil {
				return err
			}
			if err := r.section(r.alive); err != nil {
				return err
			}
		}
	}
}

// prepareObject runs the new factory in recording mode and opens what can be opened without the gate. It blocks and
// is called off the loop (Pi reload.ts:332-377).
func (r *Reload) prepareObject(object *ObjectImpl) error {
	var open []*ResourceImpl
	var refs []RefCore
	var added *candidate
	if err := r.section(func() error {
		service, _ := r.services.Get(object.typeName)
		run, err := object.run(service, object.decls)
		if err != nil {
			return err
		}
		candidate := &candidate{
			object:   object,
			service:  service,
			run:      run,
			reasons:  []string{},
			migrated: newOrderedMap[*Cell[any], any](),
			derived:  map[any]any{},
		}
		r.candidates.Set(object, candidate)
		// Each cell carries its own version: a raised one gates and migrates, a schema change without one is refused.
		for _, name := range run.decls.cells.Keys() {
			cell, _ := run.decls.cells.Get(name)
			if oldCell, _ := object.decls.cells.Get(name); oldCell != cell {
				continue
			}
			from := cellVersion(object.decls, name)
			to := cellVersion(run.decls, name)
			schema, _ := run.decls.cellSchemas.Get(name)
			switch {
			case to < from:
				return Invalid("state", fmt.Sprintf("%s: version of state %s lowered from %d to %d", object.address, name, from, to), nil)
			case to > from:
				candidate.reasons = append(candidate.reasons, fmt.Sprintf("state %s version %d -> %d", name, from, to))
			case !DeepEqual(cell.Schema, schema):
				return Invalid("state", fmt.Sprintf("%s: schema of state %s changed without a version bump", object.address, name), nil)
			}
		}
		deferred := map[*ResourceImpl]bool{}
		var deferredOrder []*ResourceImpl
		for _, resource := range run.created.resources {
			atGate := resource.exclusive()
			for _, dep := range resource.deps {
				if depResource, ok := dep.(*ResourceImpl); ok && deferred[depResource] {
					atGate = true
				}
			}
			if atGate {
				deferred[resource] = true
				deferredOrder = append(deferredOrder, resource)
			}
		}
		for _, resource := range deferredOrder {
			candidate.atGate = append(candidate.atGate, resource)
			old, ok := object.decls.resources.Get(resource.key)
			if resource.exclusive() && ok && old.exclusive() && old.state == "open" {
				candidate.replaced = append(candidate.replaced, old)
			}
		}
		if len(deferredOrder) > 0 {
			candidate.reasons = append(candidate.reasons, "exclusive resource replaced")
		}
		for _, resource := range run.created.resources {
			if !deferred[resource] {
				open = append(open, resource)
			}
		}
		refs = run.created.refs
		added = candidate
		return nil
	}); err != nil {
		return err
	}
	if err := object.open(open, refs, r.abort); err != nil {
		return err
	}
	var hydrated *Promise[struct{}]
	object.host.run(func() { hydrated = object.waitHydrated(added.run) })
	//portlint:allow ctxbackground reload.ts:415 awaits object.waitHydrated(run), which carries no signal; the readyTimeout bounds it
	if _, err := hydrated.Await(context.Background()); err != nil {
		return err
	}
	// Cells this reload adds read their first value once the new resources are open (gated objects: at the gate); the
	// derivations over them are checked after.
	if len(added.reasons) == 0 {
		if err := object.initCells(added.run, r.abort); err != nil {
			return err
		}
		return r.section(func() error { return dryRun(added) })
	}
	return nil
}

// checkRefs: every hold that survives into the new code version must bind to the new provider. Browser holds don't block a
// reload: after the commit they get upgrade_required instead. It runs on the loop (Pi reload.ts:379-399).
func (r *Reload) checkRefs() error {
	dropped := map[RefCore]bool{}
	for _, candidate := range r.candidates.Values() {
		kept := candidate.run.decls.refs.Values()
		for _, hold := range candidate.object.decls.refs.Values() {
			if !slices.Contains(kept, hold) {
				dropped[hold] = true
			}
		}
	}
	for _, object := range r.host.objects.Values() {
		service, ok := r.services.Get(object.typeName)
		if !ok || !r.changed.Has(object.typeName) {
			continue
		}
		for _, hold := range object.refs.Keys() {
			// The host's bootstrap reference follows this provider at commit; it is not an old-code consumer.
			if pinned, ok := r.host.hostObjects.Get(object.typeName); ok && hold == pinned {
				continue
			}
			if !dropped[hold] && hold.owner != "client" {
				if _, err := Bind(service.contract, service.options.Accepts, hold.contract); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// entries are the live objects' declarations with each candidate's new ones, without removed types. It runs on the
// loop (Pi reload.ts:401-406).
func (r *Reload) entries() []declsEntry {
	var entries []declsEntry
	for _, entry := range r.host.liveDecls(true) {
		if r.removed.Has(entry.object.typeName) {
			continue
		}
		if candidate, ok := r.candidates.Get(entry.object); ok {
			entry.decls = candidate.run.decls
		}
		entries = append(entries, entry)
	}
	return entries
}

// gate is the admission cut: wait for grandfathered roots (deadline), replace exclusive resources, migrate into
// private copies. Old tasks of gated objects are stopped at the cut; listener events are queued until commit or
// abort. It blocks and is called off the loop (Pi reload.ts:408-456).
func (r *Reload) gate() error {
	var drained []chan struct{}
	r.host.run(func() {
		r.gated = nil
		for _, candidate := range r.candidates.Values() {
			if len(candidate.reasons) > 0 && candidate.object.status == "live" {
				r.gated = append(r.gated, candidate)
			}
		}
		for _, candidate := range r.gated {
			object := candidate.object
			grandfathered := newOrderedMap[string, struct{}]()
			for _, root := range object.leases.Keys() {
				grandfathered.Set(root, struct{}{})
			}
			done := make(chan struct{})
			var once sync.Once
			resolve := func() { once.Do(func() { close(done) }) }
			object.gate = &objectGate{grandfathered: grandfathered, drained: resolve}
			object.stopTasks()
			if grandfathered.Len() == 0 {
				resolve()
			}
			drained = append(drained, done)
		}
	})
	deadline := deadlineOr(r.options.GateDeadline)
	timer := time.NewTimer(nodeDelay(deadline))
	timedOut := false
	for _, done := range drained {
		if timedOut {
			break
		}
		select {
		case <-done:
		case <-timer.C:
			timedOut = true
		}
	}
	timer.Stop()
	if timedOut {
		var blocking []string
		r.host.run(func() {
			for _, candidate := range r.gated {
				if candidate.object.gate != nil {
					blocking = append(blocking, candidate.object.gate.grandfathered.Keys()...)
				}
			}
		})
		if blocking == nil {
			blocking = []string{}
		}
		return NewChordError(CodeAborted, fmt.Sprintf("gate deadline (%s ms) missed; blocking roots: %s", formatMs(deadline), strings.Join(blocking, ", ")), ChordErrorOptions{Reason: "gate_deadline", Details: map[string]any{"blocking": blocking}})
	}
	if err := r.section(r.alive); err != nil {
		return err
	}
	var gated []*candidate
	r.host.run(func() { gated = slices.Clone(r.gated) })
	for _, candidate := range gated {
		object := candidate.object
		for _, old := range candidate.replaced {
			// Owned by the rollback before closing: a failing close must still be restored.
			r.host.run(func() { r.closed = append(r.closed, closedResource{object, old}) })
			if err := old.close(&object.openOrder); err != nil {
				return err
			}
		}
		if err := object.open(candidate.atGate, nil, r.abort); err != nil {
			return err
		}
		if err := r.section(r.alive); err != nil {
			return err
		}
		if err := object.initCells(candidate.run, r.abort); err != nil {
			return err
		}
		if err := r.section(r.alive); err != nil {
			return err
		}
	}
	return r.section(func() error {
		for _, candidate := range r.gated {
			if err := r.migrate(candidate); err != nil {
				return err
			}
		}
		return nil
	})
}

// migrate computes the candidate values of a gated object's kept cells: a migrate function's result on a version
// bump, else the current value under a changed schema; each is validated. It runs on the loop (Pi
// reload.ts:458-474).
func (r *Reload) migrate(candidate *candidate) error {
	object, run := candidate.object, candidate.run
	for _, name := range run.decls.cells.Keys() {
		cell, _ := run.decls.cells.Get(name)
		if oldCell, _ := object.decls.cells.Get(name); oldCell != cell {
			continue
		}
		schema, _ := run.decls.cellSchemas.Get(name)
		from := cellVersion(object.decls, name)
		fn, _ := run.decls.cellMigrates.Get(name)
		var value any
		switch {
		case cellVersion(run.decls, name) > from && fn != nil:
			old, err := delta.CloneJSON(cell.Value())
			if err != nil {
				return err
			}
			migrated, err := fn(old, from)
			if err != nil {
				return err
			}
			value = migrated
		case !DeepEqual(cell.Schema, schema):
			value = cell.Value()
		default:
			continue
		}
		if err := assertValid("state", fmt.Sprintf("%s: migrated state %s", object.address, name), schema, value); err != nil {
			return err
		}
		candidate.migrated.Set(cell, value)
	}
	return dryRun(candidate)
}

// rollback undoes a prepare: staged listeners, candidate runs, closed exclusive resources, the gate and objects of
// added types. It blocks and is called off the loop (Pi reload.ts:476-511).
func (r *Reload) rollback() {
	var stop bool
	var candidates []*candidate
	r.host.run(func() {
		if r.rolledBack {
			stop = true
			return
		}
		r.rolledBack = true
		r.abortWith(NewChordError(CodeAborted, "reload aborted", ChordErrorOptions{}))
		for _, run := range r.facetRuns.Values() {
			run.cancel(NewChordError(CodeAborted, "reload aborted", ChordErrorOptions{}))
		}
		for _, candidate := range r.candidates.Values() {
			if candidate.staged != nil {
				candidate.staged.discard()
			}
		}
		for _, staged := range r.deferred.Values() {
			staged.discard()
		}
		candidates = r.candidates.Values()
	})
	if stop {
		return
	}
	report := func(err error) { r.host.run(func() { r.host.report(err) }) }
	for _, candidate := range candidates {
		r.host.run(func() { candidate.object.pending.Delete(candidate.run.decls) })
		if err := candidate.object.unwind(candidate.run); err != nil {
			report(err)
		}
	}
	// Replaced exclusive resources are restored; one that cannot be is marked unavailable, never silently closed.
	var closed []closedResource
	r.host.run(func() { closed = slices.Clone(r.closed) })
	for _, entry := range closed {
		var restore bool
		r.host.run(func() { restore = entry.resource.state == "closed" })
		if !restore {
			continue
		}
		//portlint:allow ctxbackground reload.ts:490 restores with `resource.open(new AbortController().signal, ...)`, a signal nobody aborts
		if err := entry.resource.open(context.Background(), &entry.object.openOrder); err != nil {
			r.host.run(func() {
				entry.resource.markUnavailable()
				r.host.report(NewChordError(CodeUnavailable, fmt.Sprintf("%s: resource %s could not be restored", entry.object.address, entry.resource.key), ChordErrorOptions{Cause: err}))
			})
		}
	}
	var disposals []*ObjectImpl
	r.host.run(func() {
		for _, candidate := range r.gated {
			object := candidate.object
			if object.gate == nil {
				continue
			}
			object.openGate()
			object.startTasks()
		}
		for _, object := range r.host.objects.Values() {
			if !r.host.current.services.Has(object.typeName) {
				disposals = append(disposals, object)
			}
		}
	})
	// Objects of types this reload would have added lose their code.
	reason := NewChordError(CodeGone, "reload aborted; type not served", ChordErrorOptions{})
	for _, object := range disposals {
		var disposal *Promise[struct{}]
		r.host.run(func() { disposal = object.dispose(true, reason) })
		//portlint:allow ctxbackground reload.ts:507 `await object.dispose(true, reason)` has no abort
		if _, err := disposal.Await(context.Background()); err != nil {
			report(err)
		}
	}
	r.host.run(func() {
		r.host.pending = nil
		r.host.gating = nil
	})
}

// commit publishes the new code version in one synchronous step. Everything fallible ran in prepare (factories, opens,
// migration and derivation checks against the prospective schemas, listener subscriptions), so this only installs.
// Observers (state, shapes, registries, listener roots) are notified after the whole step. If an install still fails,
// the code version is half-published: the host fails terminally and admits no more work. It runs on the loop (Pi
// reload.ts:513-533).
func (r *Reload) commit(beforeStart func()) (ReloadReport, error) {
	type result struct {
		report ReloadReport
		err    error
	}
	// Pi reload.ts:519-531 wraps the whole step in try/catch: anything an install or start throws fails the host
	// terminally (a panic is Go's throw).
	outcome := holdPublications(&r.host.loop, func() (outcome result) {
		thrown := callReporting(func() {
			report, err := r.publish()
			if err != nil {
				outcome = result{err: err}
				return
			}
			// Installed, not yet started: the coordinator queues its commit frames here, so nothing this code version starts
			// (tasks, eager activations, then the held listener/registry/state notifications) can reach a child first.
			if beforeStart != nil {
				beforeStart()
			}
			starts := r.starts
			r.starts = nil
			for _, start := range starts {
				start()
			}
			outcome = result{report: report}
		})
		if thrown != nil {
			outcome = result{err: thrown}
		}
		return outcome
	})
	// Pi's install aborts the old tasks synchronously, but their code after the abort runs in a later microtask, after
	// the commit's synchronous step and the tasks it started: the aborts are applied here.
	stops := r.stops
	r.stops = nil
	for _, stop := range stops {
		stop()
	}
	if outcome.err != nil {
		r.host.fail(outcome.err)
		return ReloadReport{}, reloadError(outcome.err)
	}
	return outcome.report, nil
}

// publish installs the pending code version (Pi reload.ts:538-598).
func (r *Reload) publish() (ReloadReport, error) {
	host := r.host
	previous := host.current
	next := host.pending
	host.pending = nil
	host.codeVersions.Set(next.id, next)
	host.previous = previous
	host.current = next
	prepared := []string{}
	for _, object := range host.objects.Values() {
		if r.removed.Has(object.typeName) {
			continue
		}
		candidate, ok := r.candidates.Get(object)
		if !ok {
			if installed, ok := object.implementations.Get(previous.id); ok {
				copied := *installed
				copied.codeVersion = next.id
				object.implementations.Set(next.id, &copied)
			}
			continue
		}
		if object.status != "live" {
			continue
		}
		if err := r.install(candidate, next.id); err != nil {
			return ReloadReport{}, err
		}
		prepared = append(prepared, object.address)
	}
	for _, candidate := range r.candidates.Values() {
		object := candidate.object
		if object.status == "live" {
			continue
		}
		if candidate.staged != nil {
			candidate.staged.discard()
		}
		object.pending.Delete(candidate.run.decls)
		run := candidate.run
		host.goTask(func() {
			if err := object.unwind(run); err != nil {
				host.run(func() { host.report(err) })
			}
		})
	}
	// Objects of types this reload adds start their runtime now.
	for _, object := range r.deferred.Keys() {
		staged, _ := r.deferred.Get(object)
		if object.status != "live" {
			staged.discard()
			continue
		}
		object.deferredRuntime = false
		staged.activate()
		r.starts = append(r.starts, func() { objectStartTasks(object) })
	}
	host.installTables(next, r.tables, true)
	reason := NewChordError(CodeGone, "type no longer served", ChordErrorOptions{})
	for _, object := range host.objects.Values() {
		if r.removed.Has(object.typeName) {
			object.dispose(true, reason).then(func(_ struct{}, err error) {
				if err != nil {
					host.report(err)
				}
			})
		}
	}
	for _, name := range r.removed.Keys() {
		host.hostObjects.Delete(name)
	}
	for _, facet := range r.facets {
		host.facets.Set(facet.Name, facet)
	}
	for _, name := range r.options.Remove {
		host.facets.Delete(name)
	}
	// The replaced and removed runs' signals are cancelled after the install, like other observers.
	for _, name := range r.facetRuns.Keys() {
		run, _ := r.facetRuns.Get(name)
		r.starts = append(r.starts, func() { host.facetRunChanged(name, run) })
	}
	for _, name := range r.options.Remove {
		r.starts = append(r.starts, func() { host.facetRunChanged(name, nil) })
	}
	host.gating = nil
	for _, name := range r.services.Keys() {
		service, _ := r.services.Get(name)
		pinned, ok := host.hostObjects.Get(name)
		if hostLifetime(service) && r.changed.Has(name) && (!ok || pinned.refObject() == nil || pinned.refObject().status != "live") {
			r.starts = append(r.starts, func() {
				if _, err := host.startHostObject(service); err != nil {
					host.report(err)
				}
			})
		}
		// A type whose lifetime became "referenced" lives on only while someone else references its object.
		if !hostLifetime(service) && ok {
			host.hostObjects.Delete(name)
			r.starts = append(r.starts, pinned.Release)
		}
	}
	return ReloadReport{
		CodeVersion: next.id,
		Previous:    previous.id,
		Changed:     r.changed.Keys(),
		Removed:     r.removed.Keys(),
		Prepared:    prepared,
		Gated:       r.gatedAddresses(),
	}, nil
}

// install puts a prepared candidate's new implementation, declarations, schemas and values in place (Pi reload.ts:600-658).
func (r *Reload) install(candidate *candidate, codeVersion int) error {
	object, run, service := candidate.object, candidate.run, candidate.service
	gated := len(candidate.reasons) > 0
	object.pending.Delete(run.decls)
	r.stops = append(r.stops, object.detachTasks()...)
	if gated {
		// Old derivations would compute over migrated values; the old implementation can no longer run anyway.
		kept := map[*Derived]bool{}
		for _, derived := range run.decls.derives.Values() {
			kept[derived] = true
		}
		for _, derived := range object.decls.derives.Values() {
			if !kept[derived] {
				derived.Detach()
			}
		}
	}
	object.decls = run.decls
	object.service = service
	// Kept resources close with the new code's close; replaced ones keep their own (closed at retirement).
	for _, resource := range run.decls.adopted.Keys() {
		spec, _ := run.decls.adopted.Get(resource)
		resource.adopt(spec)
	}
	object.implementations.Set(codeVersion, &Installed{codeVersion: codeVersion, service: service, implementation: run.implementation, decls: run.decls})
	if gated {
		// Old code can never run over migrated state: drop the previous implementation now, not at retirement.
		object.minCodeVersion = codeVersion
		for _, old := range object.implementations.Keys() {
			if old != codeVersion {
				r.host.retireImplementation(object, old)
			}
		}
	}
	// Schemas first, then the values validated against them in prepare.
	object.refreshSchemas()

	for _, name := range run.decls.cells.Keys() {
		cell, _ := run.decls.cells.Get(name)
		schema, _ := run.decls.cellSchemas.Get(name)
		if value, ok := candidate.migrated.Get(cell); ok {
			if err := cell.Reset(value, schema); err != nil {
				return err
			}
		} else {
			cell.Schema = schema
		}
	}
	created := map[*Derived]bool{}
	for _, derived := range run.created.derives {
		created[derived] = true
	}
	for _, derived := range run.created.derives {
		if err := derived.Attach(candidate.derived); err != nil {
			return err
		}
	}
	for _, derived := range run.decls.derives.Values() {
		if !created[derived] {
			derived.Refresh()
		}
	}
	if candidate.staged != nil {
		candidate.staged.activate()
	}
	r.starts = append(r.starts, func() { objectStartTasks(object) })
	object.openGate()
	if pinned, ok := r.host.hostObjects.Get(object.typeName); ok && pinned.refObject() == object && pinned.Contract().Fingerprint != service.contract.Fingerprint {
		replacement := newRefImpl(object, service.contract, "host")
		// Acquire first: releasing the only reference must not dispose/reopen the surviving host object.
		object.addRef(replacement)
		r.host.hostObjects.Set(object.typeName, replacement)
		pinned.Release()
	}
	for _, ref := range object.refs.Keys() {
		// Browser holds whose contract no longer binds are told to reload their code (they never block a reload).
		if ref.owner == "client" {
			if _, err := Bind(service.contract, service.options.Accepts, ref.contract); err != nil {
				ref.gone(NewChordError(CodeUpgradeRequired, object.typeName+" changed; reload the web facets", ChordErrorOptions{Cause: err}))
				object.release(ref)
				continue
			}
		}
		ref.shapeChanged()
	}
	return nil
}

// objectStartTasks is the commit's start step of one object (Pi reload.ts:573, 631 `object.startTasks()`). It is a
// variable so a white-box test can break an install step, as review.test.ts:136-157 replaces startTasks.
var objectStartTasks = (*ObjectImpl).startTasks
