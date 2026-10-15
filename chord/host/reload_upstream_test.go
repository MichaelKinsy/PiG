package host

// Ports packages/chord/test/reload.test.ts: local hot reload (reload.ts) through the host runtime (host.ts,
// object.ts, cell.ts). Pi's `await tick()` (one setImmediate) only lets earlier work settle; here each one is a
// wait for the event it waits on (a root reaching its first await, a log entry), never a sleep.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// reloadGreeter is reload.test.ts:32-36 Greeter.
var reloadGreeter = Local("t.greeter", LocalDef{
	Version: 1,
	Input:   tObject("id", tOptional(tString(Schema{"default": "a"}))),
	Members: Fields[any]{{"greet", Method(tString(), tString())}, {"count", tUnknown()}},
})

// reloadCountV1 is reload.test.ts:38 CountV1.
var reloadCountV1 = tObject("n", tNumber())

// reloadGreeterSpec is reload.test.ts:40-47 GreeterSpec. initial makes a fresh initial value (nil: { n: 0 }).
type reloadGreeterSpec struct {
	greeting string
	schema   Schema
	initial  func() any
	bump     func(draft *delta.Object)
	options  ServeOptions
	state    *StateOptions
	extra    func(self Self, input any) error
}

// reloadGreeterFacet is reload.test.ts:49-76 greeterFacet.
func reloadGreeterFacet(spec reloadGreeterSpec) *Facet {
	return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reloadGreeter, func(input any, _ Principal, self Self) (Implementation, error) {
			if spec.extra != nil {
				if err := spec.extra(self, input); err != nil {
					return nil, err
				}
			}
			schema := spec.schema
			if schema == nil {
				schema = reloadCountV1
			}
			var initial any = delta.JsonObjectOf("n", float64(0))
			if spec.initial != nil {
				initial = spec.initial()
			}
			count, err := self.State("count", schema, initial, spec.state)
			if err != nil {
				return nil, err
			}
			bump := spec.bump
			if bump == nil {
				bump = func(draft *delta.Object) { _ = draft.Set("n", reloadNumber(draft.Get("n"))+1) }
			}
			return chordjson.ObjectOf(
				"count", count,
				"greet", methodFunc(func(args any, ctx Ctx) (any, error) {
					if err := count.Change(ctx, func(draft any) { bump(draft.(*delta.Object)) }); err != nil {
						return nil, err
					}
					return spec.greeting + " " + args.(string), nil
				}),
			), nil
		}, spec.options)
	}}}
}

// reloadNumber is `value ?? 0` for a JSON number.
func reloadNumber(value any) float64 {
	number, _ := value.(float64)
	return number
}

// reloadQuiet is reload.test.ts:22 quiet: a host whose background errors are dropped.
func reloadQuiet(t *testing.T) *HostImpl {
	t.Helper()
	host := CreateHost(HostOptions{Self: kindHost(), OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func reloadInstall(t *testing.T, host *HostImpl, facet *Facet) {
	t.Helper()
	if err := host.Install(*facet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
}

func reloadGet(t *testing.T, host *HostImpl, typ *Contract, input any) Ref {
	t.Helper()
	ref, err := host.Hold(typ, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func reloadPlugins(facets ...*Facet) []PluginRef {
	plugins := make([]PluginRef, len(facets))
	for index, facet := range facets {
		plugins[index] = facet
	}
	return plugins
}

// reloadOutcome is a settled promise.
type reloadOutcome struct {
	value any
	err   error
}

// reloadAsync starts fn as Pi starts an unawaited promise; the channel is the promise.
func reloadAsync(fn func() (any, error)) <-chan reloadOutcome {
	done := make(chan reloadOutcome, 1)
	go func() {
		value, err := fn()
		done <- reloadOutcome{value, err}
	}()
	return done
}

func reloadAwait(t *testing.T, promise <-chan reloadOutcome) reloadOutcome {
	t.Helper()
	select {
	case outcome := <-promise:
		return outcome
	case <-time.After(10 * time.Second):
		t.Fatal("promise did not settle")
		return reloadOutcome{}
	}
}

// reloadLatch is reload.test.ts:24-30 latch.
type reloadLatch struct {
	once sync.Once
	ch   chan struct{}
}

func newReloadLatch() *reloadLatch { return &reloadLatch{ch: make(chan struct{})} }
func (l *reloadLatch) open()       { l.once.Do(func() { close(l.ch) }) }
func (l *reloadLatch) wait()       { <-l.ch }

func reloadGreet(t *testing.T, ref Ref, name string) string {
	t.Helper()
	value, err := callMember(ref, "greet", name, nil)
	if err != nil {
		t.Fatalf("greet %s: %v", name, err)
	}
	return value.(string)
}

func reloadIsCode(err error, code ChordErrorCode) bool { return IsChordError(err, code) }

func reloadRetryable(err error) bool {
	var chordError *ChordError
	return errors.As(err, &chordError) && chordError.Retryable
}

// reloadLog is a log several goroutines append to.
type reloadLog struct {
	mu      sync.Mutex
	entries []string
}

func (log *reloadLog) push(entry string) {
	log.mu.Lock()
	log.entries = append(log.entries, entry)
	log.mu.Unlock()
}

func (log *reloadLog) items() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.entries)
}

func (log *reloadLog) reset() {
	log.mu.Lock()
	log.entries = nil
	log.mu.Unlock()
}

// reload.test.ts:87-111
func TestReloadCompatibleReloadNoGatePinnedRootsKeepOldCodeStatePreservedPreviousRetires(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if got := reloadGreet(t, ref, "a"); got != "hello a" {
		t.Fatalf("greet a = %q", got)
	}
	gogo := newReloadLatch()
	entered := newReloadLatch()
	pinned := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			first, err := callMember(ref, "greet", "p", ctx)
			if err != nil {
				return nil, err
			}
			entered.open()
			gogo.wait()
			second, err := callMember(ref, "greet", "p", ctx)
			return []any{first, second}, err
		}, RootOptions{})
	})
	entered.wait()
	var shapesMu sync.Mutex
	var shapes []string
	ref.OnShape(func(shape Shape) {
		shapesMu.Lock()
		shapes = append(shapes, shape.Contract)
		shapesMu.Unlock()
	})
	report, err := host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "hi"})), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 0 || len(report.Prepared) != 1 {
		t.Fatalf("report %+v", report)
	}
	if got := reloadGreet(t, ref, "b"); got != "hi b" {
		t.Fatalf("greet b = %q", got)
	}
	shapesMu.Lock()
	gotShapes := slices.Clone(shapes)
	shapesMu.Unlock()
	if len(gotShapes) != 1 {
		t.Fatalf("shapes = %v, want one shape push at the commit", gotShapes)
	}
	if previous, ok := host.PreviousCodeVersion(); !ok || previous != report.Previous {
		t.Fatalf("previousCodeVersion = %d %v, want %d", previous, ok, report.Previous)
	}
	gogo.open()
	outcome := reloadAwait(t, pinned)
	if outcome.err != nil || jsonText(t, outcome.value) != `["hello p","hello p"]` {
		t.Fatalf("pinned = %v %v", outcome.value, outcome.err)
	}
	if previous, ok := host.PreviousCodeVersion(); ok {
		t.Fatalf("previousCodeVersion = %d, want undefined", previous)
	}
	if got := jsonText(t, countReadable(t, ref).Value()); got != `{"n":4}` {
		t.Fatalf("count = %s", got)
	}
}

// reload.test.ts:113-156
func TestReloadPrepareRunsInRecordingModeWritesThrowAndAFailingFactoryLeavesEverythingUnchanged(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	a := reloadGet(t, host, reloadGreeter, map[string]any{"id": "a"})
	b := reloadGet(t, host, reloadGreeter, map[string]any{"id": "b"})
	if err := readyErr(t, a); err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, b); err != nil {
		t.Fatal(err)
	}
	var captured Ctx
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		captured = ctx
		return nil, nil
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}

	writing := reloadGreeterFacet(reloadGreeterSpec{greeting: "hi", extra: func(self Self, _ any) error {
		probe, err := self.State("probe", tObject(), delta.JsonObjectOf(), nil)
		if err != nil {
			return err
		}
		return probe.Replace(captured, delta.JsonObjectOf(), CellReplaceOptions{})
	}})
	if _, err := host.Reload(reloadPlugins(writing), ReloadOptions{}); err == nil || !regexp.MustCompile(`read_only`).MatchString(err.Error()) {
		t.Fatalf("writing reload: %v, want read_only", err)
	}

	var log reloadLog
	failing := reloadGreeterFacet(reloadGreeterSpec{greeting: "hi", extra: func(self Self, input any) error {
		id := reloadInputID(input)
		if _, err := self.Resource("extra", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { log.push("open " + id); return nil, nil },
			Close:   func(any) error { log.push("close " + id); return nil },
		}); err != nil {
			return err
		}
		if id == "b" {
			return errors.New("b fails")
		}
		return nil
	}})
	codeVersion := host.CodeVersion()
	_, err := host.Reload(reloadPlugins(failing), ReloadOptions{})
	// A plain error from the new factory surfaces as `unavailable`; the message says the reload aborted.
	if !reloadIsCode(err, CodeUnavailable) || !regexp.MustCompile(`reload aborted.*b fails`).MatchString(err.Error()) {
		t.Fatalf("failing reload: %v", err)
	}
	if host.CodeVersion() != codeVersion {
		t.Fatalf("code version = %d, want %d", host.CodeVersion(), codeVersion)
	}
	if got := log.items(); !slices.Equal(got, []string{"open a", "close a"}) {
		t.Fatalf("log = %q", got)
	}
	if got := reloadGreet(t, a, "x"); got != "hello x" {
		t.Fatalf("greet x = %q", got)
	}
}

// reloadInputID is the factory input's id.
func reloadInputID(input any) string {
	switch input := input.(type) {
	case map[string]any:
		id, _ := input["id"].(string)
		return id
	case *chordjson.Object:
		id, _ := input.Value("id").(string)
		return id
	}
	return ""
}

// reload.test.ts:158-174
func TestReloadRefusesAChangedCellSchemaWithoutAVersionBump(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	if err := readyErr(t, reloadGet(t, host, reloadGreeter, map[string]any{})); err != nil {
		t.Fatal(err)
	}
	changed := reloadGreeterFacet(reloadGreeterSpec{
		greeting: "hi",
		schema:   tObject("total", tNumber()),
		initial:  func() any { return delta.JsonObjectOf("total", float64(0)) },
	})
	_, err := host.Reload(reloadPlugins(changed), ReloadOptions{})
	var chordError *ChordError
	if !reloadIsCode(err, CodeInvalid) || !errors.As(err, &chordError) || chordError.Reason != "state" || !regexp.MustCompile(`without a version bump`).MatchString(err.Error()) {
		t.Fatalf("reload: %v", err)
	}
}

// reloadField reads a JSON object's member (map or ordered object).
func reloadField(value any, key string) any {
	switch value := value.(type) {
	case map[string]any:
		return value[key]
	case *chordjson.Object:
		return value.Value(key)
	}
	return nil
}

// reload.test.ts:176-218
func TestReloadStateBumpGateMigrateOldFromVersionIntoTheNewSchemaResetToWatchersOldRootsUpgradeConflict(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	reloadGreet(t, ref, "a")
	reloadGreet(t, ref, "a")
	var eventsMu sync.Mutex
	var events []StateEvent[any]
	countReadable(t, ref).Events(func(event StateEvent[any]) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	})
	gogo := newReloadLatch()
	started := newReloadLatch()
	// Started before the reload, never touched the object: rejected after the commit.
	late := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			started.open()
			gogo.wait()
			return callMember(ref, "greet", "late", ctx)
		}, RootOptions{})
	})
	started.wait()
	var froms []int
	v2 := reloadGreeterFacet(reloadGreeterSpec{
		greeting: "hi",
		schema:   tObject("total", tNumber()),
		initial:  func() any { return delta.JsonObjectOf("total", float64(0)) },
		bump:     func(draft *delta.Object) { _ = draft.Set("total", reloadNumber(draft.Get("total"))+1) },
		state: &StateOptions{Version: 2, Migrate: func(old any, from int) (any, error) {
			froms = append(froms, from)
			return delta.JsonObjectOf("total", reloadField(old, "n")), nil
		}},
	})
	report, err := host.Reload(reloadPlugins(v2), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 1 {
		t.Fatalf("gated = %v", report.Gated)
	}
	if !slices.Equal(froms, []int{1}) {
		t.Fatalf("froms = %v", froms)
	}
	eventsMu.Lock()
	last := events[len(events)-1]
	eventsMu.Unlock()
	if last.Type != StateEventReset || jsonText(t, last.Value) != `{"total":2}` {
		t.Fatalf("last event = %s %s", last.Type, jsonText(t, last.Value))
	}
	if got := reloadGreet(t, ref, "b"); got != "hi b" {
		t.Fatalf("greet b = %q", got)
	}
	if got := jsonText(t, countReadable(t, ref).Value()); got != `{"total":3}` {
		t.Fatalf("count = %s", got)
	}
	gogo.open()
	if outcome := reloadAwait(t, late); !reloadIsCode(outcome.err, CodeUpgradeConflict) || !reloadRetryable(outcome.err) {
		t.Fatalf("late = %v %v", outcome.value, outcome.err)
	}
}

// reload.test.ts:205-243
func TestReloadVersionsArePerCellARaisedOneMigratesOnlyThatCellALoweredOneIsRefused(t *testing.T) {
	host := reloadQuiet(t)
	pairType := Local("t.pair", LocalDef{
		Version: 1,
		Input:   tObject(),
		Members: Fields[any]{{"a", tUnknown()}, {"b", tUnknown()}, {"bump", Method(nil, nil)}},
	})
	var migratedMu sync.Mutex
	var migrated []string
	facet := func(version int) *Facet {
		return &Facet{Name: "pair", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(pairType, func(_ any, _ Principal, self Self) (Implementation, error) {
				a, err := self.State("a", tNumber(), float64(1), nil)
				if err != nil {
					return nil, err
				}
				b, err := self.State("b", tNumber(), float64(1), &StateOptions{Version: version, Migrate: func(old any, from int) (any, error) {
					migratedMu.Lock()
					migrated = append(migrated, fmt.Sprintf("b %d->%d", from, version))
					migratedMu.Unlock()
					return old.(float64) * 10, nil
				}})
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(
					"a", a,
					"b", b,
					"bump", methodFunc(func(_ any, ctx Ctx) (any, error) {
						return nil, a.Replace(ctx, a.Value().(float64)+1, CellReplaceOptions{})
					}),
				), nil
			}, ServeOptions{})
		}}}
	}
	reloadInstall(t, host, facet(1))
	pair := reloadGet(t, host, pairType, map[string]any{})
	if err := readyErr(t, pair); err != nil {
		t.Fatal(err)
	}
	if _, err := callMember(pair, "bump", nil, nil); err != nil {
		t.Fatal(err)
	}
	report, err := host.Reload(reloadPlugins(facet(2)), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 1 {
		t.Fatalf("gated = %v", report.Gated)
	}
	migratedMu.Lock()
	gotMigrated := slices.Clone(migrated)
	migratedMu.Unlock()
	if !slices.Equal(gotMigrated, []string{"b 1->2"}) {
		t.Fatalf("migrated = %q", gotMigrated)
	}
	values := jsonText(t, []any{pair.Member("a").(Readable[any]).Value(), pair.Member("b").(Readable[any]).Value()})
	if values != `[2,10]` {
		t.Fatalf("a kept as it was, b migrated: got %s", values)
	}
	if _, err := host.Reload(reloadPlugins(facet(1)), ReloadOptions{}); err == nil || !regexp.MustCompile(`version of state b lowered from 2 to 1`).MatchString(err.Error()) {
		t.Fatalf("lowered reload: %v", err)
	}
}

// reloadUntilUpgradeConflict is reload.test.ts:233-240 and :547-554: probe greet until the gate refuses it.
func reloadUntilUpgradeConflict(t *testing.T, ref Ref) {
	t.Helper()
	waitFor(t, "upgrade_conflict", func() bool {
		_, err := callMember(ref, "greet", "probe", nil)
		return reloadIsCode(err, CodeUpgradeConflict)
	})
}

// reload.test.ts:220-246
func TestReloadGateLateOldRootsGetUpgradeConflictGrandfatheredRootsReEnterThenCommit(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	gogo := newReloadLatch()
	entered := newReloadLatch()
	grandfathered := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(ref, "greet", "g", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gogo.wait()
			return callMember(ref, "greet", "g", ctx)
		}, RootOptions{})
	})
	entered.wait()
	reload := reloadAsync(func() (any, error) {
		return host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "hi", state: &StateOptions{Version: 2}})), ReloadOptions{})
	})
	reloadUntilUpgradeConflict(t, ref)
	gogo.open()
	if outcome := reloadAwait(t, grandfathered); outcome.err != nil || outcome.value != "hello g" {
		t.Fatalf("grandfathered = %v %v", outcome.value, outcome.err)
	}
	outcome := reloadAwait(t, reload)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if gated := outcome.value.(ReloadReport).Gated; len(gated) != 1 {
		t.Fatalf("gated = %v", gated)
	}
	if got := reloadGreet(t, ref, "n"); got != "hi n" {
		t.Fatalf("greet n = %q", got)
	}
}

// reloadTaskFacet is reload.test.ts:251-269: Greeter with a task that logs its start and stop.
func reloadTaskFacet(log *reloadLog, greeting string, version int) *Facet {
	return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
			if err := self.Task(func(ctx Ctx) error {
				log.push("start " + greeting)
				<-ctx.Signal().Done()
				log.push("stop " + greeting)
				return nil
			}); err != nil {
				return nil, err
			}
			count, err := self.State("count", tNumber(), float64(0), &StateOptions{Version: version})
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(
				"count", count,
				"greet", methodFunc(func(args any, _ Ctx) (any, error) { return greeting + " " + args.(string), nil }),
			), nil
		}, ServeOptions{})
	}}}
}

// reload.test.ts:248-289
func TestReloadGateDeadlineReloadAbortsReportsBlockingRootsNothingChanges(t *testing.T) {
	host := reloadQuiet(t)
	var log reloadLog
	reloadInstall(t, host, reloadTaskFacet(&log, "hello", 1))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	// Pi runs the task's synchronous prefix when the object starts; a Go task is a goroutine, so wait for it.
	waitFor(t, "the task started", func() bool { return len(log.items()) == 1 })
	gogo := newReloadLatch()
	entered := newReloadLatch()
	blocker := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(ref, "greet", "b", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gogo.wait()
			return nil, nil
		}, RootOptions{})
	})
	entered.wait()
	deadline := 30 * time.Millisecond
	_, err := host.Reload(reloadPlugins(reloadTaskFacet(&log, "hi", 2)), ReloadOptions{GateDeadline: &deadline})
	var chordError *ChordError
	if !reloadIsCode(err, CodeAborted) || !errors.As(err, &chordError) || chordError.Reason != "gate_deadline" {
		t.Fatalf("reload: %v", err)
	}
	if !regexp.MustCompile(`blocking roots: host#\w+:\d+`).MatchString(err.Error()) {
		t.Fatalf("message = %q", err.Error())
	}
	if got := reloadGreet(t, ref, "x"); got != "hello x" {
		t.Fatalf("greet x = %q", got)
	}
	waitFor(t, "the task restarted", func() bool { return len(log.items()) >= 3 })
	if got := log.items(); !slices.Equal(got, []string{"start hello", "stop hello", "start hello"}) {
		t.Fatalf("log = %q", got)
	}
	gogo.open()
	if outcome := reloadAwait(t, blocker); outcome.err != nil {
		t.Fatal(outcome.err)
	}
}

// reloadResourceFacet is reload.test.ts:294-317.
func reloadResourceFacet(log *reloadLog, rev int) *Facet {
	return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
			revText := strconv.Itoa(rev)
			lock, err := self.Resource("lock", ResourceDeclaration[any]{
				Version:   rev,
				Exclusive: true,
				Open: func(ResourceOpenOptions) (any, error) {
					log.push("open lock " + revText)
					return float64(rev), nil
				},
				Close: func(any) error { log.push("close lock " + revText); return nil },
			})
			if err != nil {
				return nil, err
			}
			if _, err := self.Resource("cache", ResourceDeclaration[any]{
				Version: rev,
				Open:    func(ResourceOpenOptions) (any, error) { log.push("open cache " + revText); return nil, nil },
				Close:   func(any) error { log.push("close cache " + revText); return nil },
			}); err != nil {
				return nil, err
			}
			count, err := self.State("count", tNumber(), float64(0), nil)
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(
				"count", count,
				"greet", methodFunc(func(args any, _ Ctx) (any, error) {
					value, err := lock.Value()
					if err != nil {
						return nil, err
					}
					return fmt.Sprint(value) + " " + args.(string), nil
				}),
			), nil
		}, ServeOptions{})
	}}}
}

// reload.test.ts:291-327
func TestReloadExclusiveResourcesAreReplacedAtTheGateNonExclusiveOnesAtPrepareAndClosedAtRetirement(t *testing.T) {
	host := reloadQuiet(t)
	var log reloadLog
	reloadInstall(t, host, reloadResourceFacet(&log, 1))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if got := reloadGreet(t, ref, "x"); got != "1 x" {
		t.Fatalf("greet = %q", got)
	}
	log.reset()
	report, err := host.Reload(reloadPlugins(reloadResourceFacet(&log, 2)), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Gated) != 1 {
		t.Fatalf("gated = %v", report.Gated)
	}
	if got := reloadGreet(t, ref, "x"); got != "2 x" {
		t.Fatalf("greet = %q", got)
	}
	waitFor(t, "the old cache closed", func() bool { return len(log.items()) >= 4 })
	if got := log.items(); !slices.Equal(got, []string{"open cache 2", "close lock 1", "open lock 2", "close cache 1"}) {
		t.Fatalf("log = %q", got)
	}
}

// reload.test.ts:329-352
func TestReloadTwoVersionRuleTheNextReloadRetiresThePreviousCodeVersionItsRootsGetSuperseded(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "v1"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	first := host.CodeVersion()
	gogo := newReloadLatch()
	entered := newReloadLatch()
	pinned := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(ref, "greet", "p", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gogo.wait()
			return callMember(ref, "greet", "p", ctx)
		}, RootOptions{})
	})
	entered.wait()
	if _, err := host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "v2"})), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if previous, ok := host.PreviousCodeVersion(); !ok || previous != first {
		t.Fatalf("previousCodeVersion = %d %v, want %d", previous, ok, first)
	}
	retire := 20 * time.Millisecond
	if _, err := host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "v3"})), ReloadOptions{RetireDeadline: &retire}); err != nil {
		t.Fatal(err)
	}
	gogo.open()
	if outcome := reloadAwait(t, pinned); !reloadIsCode(outcome.err, CodeSuperseded) || !reloadRetryable(outcome.err) {
		t.Fatalf("pinned = %v %v", outcome.value, outcome.err)
	}
	if _, err := host.Root(func(Ctx) (any, error) { return float64(1), nil }, RootOptions{CodeVersion: &first}); !reloadIsCode(err, CodeSuperseded) {
		t.Fatalf("root in code version %d: %v", first, err)
	}
	if got := reloadGreet(t, ref, "n"); got != "v3 n" {
		t.Fatalf("greet n = %q", got)
	}
}

// reloadDoc is reload.test.ts:355-369 DocV1/DocBroken/DocV2: a remote t.rdoc with one method.
func reloadDoc(version int, method string, result Schema) *Contract {
	return Remote("t.rdoc", RemoteDef{
		Version:  version,
		Input:    tObject(),
		Host:     NewHostNamed("host"),
		Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{method, Method(nil, result)}}}}},
	})
}

func reloadServeFacet(name string, typ *Contract, implementation func() Implementation, options ServeOptions) *Facet {
	return &Facet{Name: name, FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(typ, func(any, Principal, Self) (Implementation, error) { return implementation(), nil }, options)
	}}}
}

// reload.test.ts:354-404
func TestReloadChecksContractsAtReloadForgottenBumpsLiveConsumersAcceptsAdapters(t *testing.T) {
	docV1 := reloadDoc(1, "read", tObject("text", tString()))
	docBroken := reloadDoc(1, "read", tObject("text", tNumber()))
	docV2 := reloadDoc(2, "fetch", tObject("body", tString()))
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadServeFacet("doc", docV1, func() Implementation {
		return chordjson.ObjectOf("variant", "main", "read", methodFunc(func(any, Ctx) (any, error) {
			return map[string]any{"text": "v1"}, nil
		}))
	}, ServeOptions{}))
	consumer := reloadGet(t, host, docV1, map[string]any{})
	if got := jsonText(t, mustCall(t, consumer, "read", nil, nil)); got != `{"text":"v1"}` {
		t.Fatalf("read = %s", got)
	}

	broken := reloadServeFacet("doc", docBroken, func() Implementation {
		return chordjson.ObjectOf("variant", "main", "read", methodFunc(func(any, Ctx) (any, error) {
			return map[string]any{"text": float64(1)}, nil
		}))
	}, ServeOptions{})
	if _, err := host.Reload(reloadPlugins(broken), ReloadOptions{}); err == nil || !regexp.MustCompile(`without a version bump`).MatchString(err.Error()) {
		t.Fatalf("broken reload: %v", err)
	}

	fetch := methodFunc(func(any, Ctx) (any, error) { return map[string]any{"body": "v2"}, nil })
	v2 := reloadServeFacet("doc", docV2, func() Implementation { return chordjson.ObjectOf("variant", "main", "fetch", fetch) }, ServeOptions{})
	if _, err := host.Reload(reloadPlugins(v2), ReloadOptions{}); !reloadIsCode(err, CodeContractMismatch) {
		t.Fatalf("v2 reload: %v", err)
	}

	adapted := reloadServeFacet("doc", docV2, func() Implementation { return chordjson.ObjectOf("variant", "main", "fetch", fetch) }, ServeOptions{
		Accepts: Accepts{1: func(current Implementation) Implementation {
			return chordjson.ObjectOf("variant", "main", "read", methodFunc(func(any, Ctx) (any, error) {
				fetched, err := current.Value("fetch").(methodFunc)(nil, nil)
				if err != nil {
					return nil, err
				}
				return map[string]any{"text": fmt.Sprint(reloadField(fetched, "body")) + " via adapter"}, nil
			}))
		}},
	})
	if _, err := host.Reload(reloadPlugins(adapted), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, mustCall(t, consumer, "read", nil, nil)); got != `{"text":"v2 via adapter"}` {
		t.Fatalf("read = %s", got)
	}
	modern := reloadGet(t, host, docV2, map[string]any{})
	if got := jsonText(t, mustCall(t, modern, "fetch", nil, nil)); got != `{"body":"v2"}` {
		t.Fatalf("fetch = %s", got)
	}
}

// reloadEvents is reload.test.ts:408-417: an event source whose listeners are a set.
type reloadEvents struct {
	mu        sync.Mutex
	next      int
	listeners map[int]func(event any)
	order     []int
}

func (events *reloadEvents) Subscribe(listener func(event any)) func() {
	events.mu.Lock()
	defer events.mu.Unlock()
	if events.listeners == nil {
		events.listeners = map[int]func(any){}
	}
	id := events.next
	events.next++
	events.listeners[id] = listener
	events.order = append(events.order, id)
	return func() {
		events.mu.Lock()
		defer events.mu.Unlock()
		delete(events.listeners, id)
		events.order = slices.DeleteFunc(events.order, func(other int) bool { return other == id })
	}
}

func (events *reloadEvents) size() int {
	events.mu.Lock()
	defer events.mu.Unlock()
	return len(events.listeners)
}

func (events *reloadEvents) emit(value any) {
	events.mu.Lock()
	var listeners []func(any)
	for _, id := range events.order {
		listeners = append(listeners, events.listeners[id])
	}
	events.mu.Unlock()
	for _, listener := range listeners {
		listener(value)
	}
}

// reloadSum is reload.test.ts:419-423 Sum.
var reloadSum = Local("t.sum", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"sum", tObject("v", tNumber())}}})

// reloadSelectSource is reload.test.ts:432 `(source) => source`: one function (Pi compares select functions by
// source text, PiG by code pointer, so both reloads' selects are the same).
func reloadSelectSource(value any) Subscribable[any] { return value.(Subscribable[any]) }

func reloadSumFacet(events *reloadEvents, log *reloadLog, factor float64) *Facet {
	return &Facet{Name: "sum", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reloadSum, func(_ any, _ Principal, self Self) (Implementation, error) {
			sum, err := self.State("sum", tObject("v", tNumber()), delta.JsonObjectOf("v", float64(0)), nil)
			if err != nil {
				return nil, err
			}
			if err := self.OnSelect("add", Subscribable[any](events), reloadSelectSource, func(value any, ctx Ctx) error {
				return sum.Change(ctx, func(draft any) {
					object := draft.(*delta.Object)
					_ = object.Set("v", reloadNumber(object.Get("v"))+value.(float64)*factor)
				})
			}); err != nil {
				return nil, err
			}
			factorText := strconv.FormatFloat(factor, 'f', -1, 64)
			if err := self.Task(func(ctx Ctx) error {
				log.push("start " + factorText)
				<-ctx.Signal().Done()
				log.push("stop " + factorText)
				return nil
			}); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("sum", sum), nil
		}, ServeOptions{})
	}}}
}

// reload.test.ts:406-460
func TestReloadRebindsListenersToTheNewCodeAndRestartsTasks(t *testing.T) {
	host := reloadQuiet(t)
	events := &reloadEvents{}
	var log reloadLog
	reloadInstall(t, host, reloadSumFacet(events, &log, 1))
	ref := reloadGet(t, host, reloadSum, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	sum := ref.Member("sum").(Readable[any])
	events.emit(float64(1))
	waitFor(t, "the first event", func() bool { return jsonText(t, sum.Value()) == `{"v":1}` })
	// Pi runs a task's synchronous prefix when it starts (the install); a Go task is a goroutine, so wait for it.
	waitFor(t, "the first task started", func() bool { return slices.Contains(log.items(), "start 1") })
	if _, err := host.Reload(reloadPlugins(reloadSumFacet(events, &log, 10)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if size := events.size(); size != 1 {
		t.Fatalf("listeners = %d", size)
	}
	events.emit(float64(1))
	waitFor(t, "the second event", func() bool { return jsonText(t, sum.Value()) != `{"v":1}` })
	if got := jsonText(t, sum.Value()); got != `{"v":11}` {
		t.Fatalf("sum = %s", got)
	}
	waitFor(t, "the new task started", func() bool { return len(log.items()) >= 3 })
	// The replacement starts once the aborted run finished (reload.test.ts: Pi 00d8d049).
	if got := log.items(); !slices.Equal(got, []string{"start 1", "stop 1", "start 10"}) {
		t.Fatalf("log = %q", got)
	}
}

// reload.test.ts:462-496
func TestReloadKeepsRegistryTablesPerCodeVersion(t *testing.T) {
	host := reloadQuiet(t)
	routes := Registry[any](DefineRegistry[string]("t.reload-routes"))
	router := Local("t.router", LocalDef{Version: 1, Input: tObject()})
	facet := func(value string) *Facet {
		return &Facet{Name: "router", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(router, func(_ any, _ Principal, self Self) (Implementation, error) {
				if err := self.Register(routes, []Entry{{Name: "GET /", Value: value}}); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	reloadInstall(t, host, facet("one"))
	var seenMu sync.Mutex
	var seen []string
	host.Registry(routes).Subscribe(func(table RegistryTable[any], codeVersion int) {
		value, _ := table.Get("GET /")
		seenMu.Lock()
		seen = append(seen, fmt.Sprintf("%v@%d", value, codeVersion))
		seenMu.Unlock()
	})
	gogo := newReloadLatch()
	started := newReloadLatch()
	pinned := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			started.open()
			gogo.wait()
			value, _ := host.Registry(routes).Get("GET /", ctx)
			return value, nil
		}, RootOptions{})
	})
	started.wait()
	report, err := host.Reload(reloadPlugins(facet("two")), ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := host.Registry(routes).Get("GET /", nil); value != "two" {
		t.Fatalf("current = %v", value)
	}
	gogo.open()
	if outcome := reloadAwait(t, pinned); outcome.err != nil || outcome.value != "one" {
		t.Fatalf("pinned = %v %v", outcome.value, outcome.err)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if want := []string{fmt.Sprintf("one@%d", report.Previous), fmt.Sprintf("two@%d", report.CodeVersion)}; !slices.Equal(seen, want) {
		t.Fatalf("seen = %q, want %q", seen, want)
	}
}

// reload.test.ts:498-528
func TestReloadAReloadCanAddATypeTogetherWithAConsumerThatRefsIt(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	clock := Local("t.clock", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"now", Method(nil, tNumber())}}})
	clockFacet := reloadServeFacet("clock", clock, func() Implementation {
		return chordjson.ObjectOf("now", methodFunc(func(any, Ctx) (any, error) { return float64(42), nil }))
	}, ServeOptions{})
	consumer := func(fail bool) *Facet {
		return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
				clockRef, err := self.Get(clock, map[string]any{}, nil)
				if err != nil {
					return nil, err
				}
				count, err := self.State("count", reloadCountV1, delta.JsonObjectOf("n", float64(0)), nil)
				if err != nil {
					return nil, err
				}
				if fail {
					return nil, errors.New("consumer fails")
				}
				return chordjson.ObjectOf(
					"count", count,
					"greet", methodFunc(func(args any, ctx Ctx) (any, error) {
						now, err := callMember(clockRef, "now", nil, ctx)
						if err != nil {
							return nil, err
						}
						return fmt.Sprint(now) + " " + args.(string), nil
					}),
				), nil
			}, ServeOptions{})
		}}}
	}
	codeVersion := host.CodeVersion()
	if _, err := host.Reload(reloadPlugins(clockFacet, consumer(true)), ReloadOptions{}); err == nil || !regexp.MustCompile(`consumer fails`).MatchString(err.Error()) {
		t.Fatalf("failing reload: %v", err)
	}
	if host.CodeVersion() != codeVersion {
		t.Fatalf("code version = %d, want %d", host.CodeVersion(), codeVersion)
	}
	if got := reloadGreet(t, ref, "x"); got != "hello x" {
		t.Fatalf("greet = %q", got)
	}
	if _, err := host.Reload(reloadPlugins(clockFacet, consumer(false)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := reloadGreet(t, ref, "x"); got != "42 x" {
		t.Fatalf("greet = %q", got)
	}
}

// reload.test.ts:530-567
func TestReloadTheGateRejectsNewObjectsOfAChangedTypeAfterCommitTheyGetTheNewCode(t *testing.T) {
	host := reloadQuiet(t)
	reloadInstall(t, host, reloadGreeterFacet(reloadGreeterSpec{greeting: "hello"}))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	gogo := newReloadLatch()
	entered := newReloadLatch()
	grandfathered := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(ref, "greet", "g", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gogo.wait()
			return nil, nil
		}, RootOptions{})
	})
	later := newReloadLatch()
	started := newReloadLatch()
	pinned := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) {
			started.open()
			later.wait()
			return callMember(ref, "greet", "old", ctx)
		}, RootOptions{})
	})
	entered.wait()
	started.wait()
	reload := reloadAsync(func() (any, error) {
		return host.Reload(reloadPlugins(reloadGreeterFacet(reloadGreeterSpec{greeting: "hi", state: &StateOptions{Version: 2}})), ReloadOptions{})
	})
	reloadUntilUpgradeConflict(t, ref)
	if _, err := host.Hold(reloadGreeter, map[string]any{"id": "late"}, nil); !reloadIsCode(err, CodeUpgradeConflict) || !reloadRetryable(err) {
		t.Fatalf("get late during the gate: %v", err)
	}
	gogo.open()
	if outcome := reloadAwait(t, grandfathered); outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome := reloadAwait(t, reload); outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if got := reloadGreet(t, reloadGet(t, host, reloadGreeter, map[string]any{"id": "late"}), "l"); got != "hi l" {
		t.Fatalf("greet l = %q", got)
	}
	// The previous code version is still retained (pinned root), but the gated object dropped its old implementation.
	if _, ok := host.PreviousCodeVersion(); !ok {
		t.Fatal("previousCodeVersion is undefined")
	}
	later.open()
	if outcome := reloadAwait(t, pinned); !reloadIsCode(outcome.err, CodeUpgradeConflict) {
		t.Fatalf("pinned = %v %v", outcome.value, outcome.err)
	}
}

// forcedRetirementFacet is reload.test.ts "forced retirement aborts the leftover roots ..." facet(version): a resource
// per version (replaced at every reload, closed with its own close) and a greet that waits for its abort or a latch.
func forcedRetirementFacet(version int, log *reloadLog, hold, waiting, holding *reloadLatch) *Facet {
	return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
			conn, err := self.Resource("conn", ResourceDeclaration[any]{
				Version: version,
				Open:    func(ResourceOpenOptions) (any, error) { return version, nil },
				Close:   func(any) error { log.push(fmt.Sprintf("close %d", version)); return nil },
			})
			if err != nil {
				return nil, err
			}
			count, err := self.State("count", reloadCountV1, delta.JsonObjectOf("n", float64(0)), nil)
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(
				"count", count,
				"greet", methodFunc(func(args any, ctx Ctx) (any, error) {
					name := args.(string)
					if name == "hold" {
						holding.open()
						hold.wait()
						value, err := conn.Value()
						return fmt.Sprintf("held on %v, aborted %v", value, ctx.Signal().Err() != nil), err
					}
					if name != "wait" {
						return name, nil
					}
					waiting.open()
					<-ctx.Signal().Done()
					// Still open: the abort comes first, then the grace, then the close.
					value, err := conn.Value()
					log.push(fmt.Sprintf("aborted, conn %v", value))
					return name, err
				}),
			), nil
		}, ServeOptions{})
	}}}
}

// reload.test.ts "forced retirement aborts the leftover roots before it closes the resources only their code used"
// (Pi 00d8d049).
func TestReloadForcedRetirementAbortsTheLeftoverRootsBeforeItClosesTheResourcesOnlyTheirCodeUsed(t *testing.T) {
	host := reloadQuiet(t)
	var log reloadLog
	hold, waiting, holding := newReloadLatch(), newReloadLatch(), newReloadLatch()
	reloadInstall(t, host, forcedRetirementFacet(1, &log, hold, waiting, holding))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	old := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "greet", "wait", ctx) }, RootOptions{})
	})
	waiting.wait()
	if _, err := host.Reload(reloadPlugins(forcedRetirementFacet(2, &log, hold, waiting, holding)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	// A root of the code version that becomes the previous one: forced retirement of the older one leaves it alone.
	current := reloadAsync(func() (any, error) {
		return host.Root(func(ctx Ctx) (any, error) { return callMember(ref, "greet", "hold", ctx) }, RootOptions{})
	})
	holding.wait()
	if _, err := host.Reload(reloadPlugins(forcedRetirementFacet(3, &log, hold, waiting, holding)), ReloadOptions{RetireDeadline: new(20 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if outcome := reloadAwait(t, old); outcome.err != nil || outcome.value != "wait" {
		t.Fatalf("old = %v %v", outcome.value, outcome.err)
	}
	waitFor(t, "close 1", func() bool { return slices.Contains(log.items(), "close 1") })
	if got := log.items(); !slices.Equal(got, []string{"aborted, conn 1", "close 1"}) {
		t.Fatalf("log = %q", got)
	}
	hold.open()
	if outcome := reloadAwait(t, current); outcome.err != nil || outcome.value != "held on 2, aborted false" {
		t.Fatalf("current = %v %v", outcome.value, outcome.err)
	}
	if got := reloadGreet(t, ref, "x"); got != "x" {
		t.Fatalf("greet x = %q", got)
	}
}

// reload.test.ts "a kept resource closes with the newest declaration's close" (Pi 00d8d049).
func TestReloadAKeptResourceClosesWithTheNewestDeclarationsClose(t *testing.T) {
	host := reloadQuiet(t)
	var log reloadLog
	facet := func(label string) *Facet {
		return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
				if _, err := self.Resource("conn", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { return chordjson.ObjectOf(), nil },
					Close:   func(any) error { log.push("close by " + label); return nil },
				}); err != nil {
					return nil, err
				}
				count, err := self.State("count", reloadCountV1, delta.JsonObjectOf("n", float64(0)), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(
					"count", count,
					"greet", methodFunc(func(args any, _ Ctx) (any, error) { return label + " " + args.(string), nil }),
				), nil
			}, ServeOptions{})
		}}}
	}
	reloadInstall(t, host, facet("a"))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"b", "c"} {
		if _, err := host.Reload(reloadPlugins(facet(label)), ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := reloadGreet(t, ref, "x"); got != "c x" {
		t.Fatalf("greet x = %q", got)
	}
	if got := log.items(); len(got) != 0 {
		t.Fatalf("log = %q, want kept, not reopened", got)
	}
	ref.Release()
	waitFor(t, "the resource to close", func() bool { return len(log.items()) > 0 })
	if got := log.items(); !slices.Equal(got, []string{"close by c"}) {
		t.Fatalf("log = %q", got)
	}
}

// reload.test.ts "a task that ignores its abort delays its replacement at most taskStopDeadline (reported)" (Pi
// 00d8d049). Pi fakes its timers and advances them by 29 and 1 ms; real timers need a longer deadline and a pause
// well inside it.
func TestReloadATaskThatIgnoresItsAbortDelaysItsReplacementAtMostTaskStopDeadlineReported(t *testing.T) {
	const deadline = 400 * time.Millisecond
	var errorsMu sync.Mutex
	var reported []error
	host := CreateHost(HostOptions{Self: kindHost(),
		OnError:          func(err error) { errorsMu.Lock(); reported = append(reported, err); errorsMu.Unlock() },
		TaskStopDeadline: new(deadline),
	})
	t.Cleanup(func() { _ = host.Close() })
	var log reloadLog
	stuck := newReloadLatch()
	t.Cleanup(stuck.open)
	facet := func(label string) *Facet {
		return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
				if err := self.Task(func(Ctx) error {
					log.push("start " + label)
					if label == "a" {
						stuck.wait() // ignores ctx.Signal()
					}
					return nil
				}); err != nil {
					return nil, err
				}
				count, err := self.State("count", reloadCountV1, delta.JsonObjectOf("n", float64(0)), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(
					"count", count,
					"greet", methodFunc(func(args any, _ Ctx) (any, error) { return args, nil }),
				), nil
			}, ServeOptions{})
		}}}
	}
	reloadInstall(t, host, facet("a"))
	ref := reloadGet(t, host, reloadGreeter, map[string]any{})
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first task started", func() bool { return slices.Contains(log.items(), "start a") })
	if _, err := host.Reload(reloadPlugins(facet("b")), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(deadline / 4)
	if got := log.items(); !slices.Equal(got, []string{"start a"}) {
		t.Fatalf("log = %q, want the replacement to wait for the old run", got)
	}
	errorsMu.Lock()
	early := len(reported)
	errorsMu.Unlock()
	if early != 0 {
		t.Fatalf("reported %v before the deadline", reported)
	}
	waitFor(t, "the replacement to start", func() bool { return slices.Contains(log.items(), "start b") })
	if got := log.items(); !slices.Equal(got, []string{"start a", "start b"}) {
		t.Fatalf("log = %q", got)
	}
	errorsMu.Lock()
	defer errorsMu.Unlock()
	if !slices.ContainsFunc(reported, func(err error) bool {
		return IsChordError(err, CodeAborted) && regexp.MustCompile(`still running after 400 ms`).MatchString(err.Error())
	}) {
		t.Fatalf("reported %v, want aborted: still running after 400 ms", reported)
	}
}

// reload.test.ts "a facet's signal aborts when its run is replaced or removed, when its reload aborts, and when the
// host closes" (Pi 00d8d049).
func TestReloadAFacetsSignalAbortsWhenItsRunIsReplacedOrRemovedWhenItsReloadAbortsAndWhenTheHostCloses(t *testing.T) {
	host := reloadQuiet(t)
	var mu sync.Mutex
	signals := map[string]context.Context{}
	aborted := func(label string) bool {
		mu.Lock()
		defer mu.Unlock()
		return signals[label].Err() != nil
	}
	facet := func(label string, fail bool) *Facet {
		return &Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			mu.Lock()
			signals[label] = api.Signal
			mu.Unlock()
			return api.Serve(reloadGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
				if fail {
					return nil, errors.New("broken")
				}
				count, err := self.State("count", reloadCountV1, delta.JsonObjectOf("n", float64(0)), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(
					"count", count,
					"greet", methodFunc(func(args any, _ Ctx) (any, error) { return args, nil }),
				), nil
			}, ServeOptions{})
		}}}
	}
	other := &Facet{Name: "other", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		mu.Lock()
		signals["other"] = api.Signal
		mu.Unlock()
		return nil
	}}}
	reloadInstall(t, host, facet("a", false))
	reloadInstall(t, host, other)
	if err := readyErr(t, reloadGet(t, host, reloadGreeter, map[string]any{})); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Reload(reloadPlugins(facet("b", true)), ReloadOptions{}); err == nil {
		t.Fatal("the reload of a broken facet succeeded")
	}
	if !aborted("b") {
		t.Fatal("the aborted reload's run is not aborted")
	}
	if aborted("a") {
		t.Fatal("a is aborted while still installed")
	}
	if _, err := host.Reload(reloadPlugins(facet("c", false)), ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if !aborted("a") {
		t.Fatal("a is not aborted once replaced")
	}
	if aborted("c") {
		t.Fatal("c is aborted while installed")
	}
	if _, err := host.Reload(nil, ReloadOptions{Remove: []string{"other"}}); err != nil {
		t.Fatal(err)
	}
	if !aborted("other") {
		t.Fatal("other is not aborted once removed")
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
	if !aborted("c") {
		t.Fatal("c is not aborted once the host closed")
	}
}
