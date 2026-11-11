package runtime_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

var loopHolder = func() *contracts.Contract {
	contract, err := contracts.Local("p2.test.loop-holder", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("hold", contracts.NewMethod())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// Durable's scheduler reads the registry's Snapshot under its own lock, while a commit's publication runs in a section of the
// host's loop and waits for that lock: a Snapshot that waited for the loop (a registry value is read as a section of it)
// deadlocked the two (a worker with subagents under load stopped answering). The Snapshot answers while a section holds the
// loop: here the section is a method handler, which holds the loop until it returns.
func TestDurableRegistrySnapshotAnswersWhileASectionHoldsTheLoop(t *testing.T) {
	inSection, release := make(chan struct{}), make(chan struct{})
	holding := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(loopHolder, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				// A handler runs in the section that admitted its call, and holds the host's loop until it returns or suspends.
				"hold": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					close(inSection)
					<-release
					return nil, nil
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Worker: true, Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil), holding}})
	extensions, err := fixture.Host.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	switches, err := fixture.Host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registry := p2runtime.DurableRegistry(extensions, switches)
	holder, err := fixture.Host.Get(p2test.Background, loopHolder, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	go func() {
		defer close(opened)
		_, _ = holder.Call("hold", nil, p2test.Background)
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-opened
	}()
	<-inSection
	answered := make(chan struct{})
	go func() {
		registry.Snapshot()
		close(answered)
	}()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Error("Snapshot waited for the host's loop")
	}
}

var lateOffer = func() *contracts.Contract {
	contract, err := contracts.Local("p2.test.late-offer", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("offer", contracts.NewMethod()), contracts.Mem("switchOff", contracts.NewMethod())},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// lateExtension is a registry composed in a worker whose plugin contributes its extension only when the test calls `offer`
// (the way the agent plugin's extension and the MCP plugin's tools, which follow the servers' view, arrive in a live worker),
// and switches it off when the test calls `switchOff` (the way the `codemode` setting does).
type lateExtension struct {
	registry   harness.RegistryReader
	extensions *registries.RegistryValueSource
	switches   *registries.RegistryValueSource
	late       *host.Reference
	// woken counts the calls of the listener the registry was subscribed to by `subscribe`.
	woken *atomic.Int32
}

func (late lateExtension) subscribe() func() {
	return late.registry.Subscribe(func() { late.woken.Add(1) })
}

func newLateExtension(t *testing.T) lateExtension {
	t.Helper()
	var offered, switched state.Writable[any]
	facet := host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := p2runtime.Contribute(facet, "p2.test.late-extension", func(self host.StoreSelf) error {
			offered = self.Writable(p2runtime.Keyed[*p2runtime.P2Extension]{})
			self.Contribute(p2runtime.Extensions.Registry, offered)
			switched = self.Writable(p2runtime.Keyed[*bool]{})
			self.Contribute(p2runtime.ExtensionSwitches.Registry, switched)
			return nil
		}); err != nil {
			return err
		}
		return facet.Serve(lateOffer, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"offer": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return nil, offered.Set(p2runtime.Keyed[*p2runtime.P2Extension]{
						p2runtime.Entry("late", &p2runtime.P2Extension{Extension: &durable.Extension{Name: "late"}}),
					})
				},
				"switchOff": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					off := false
					return nil, switched.Set(p2runtime.Keyed[*bool]{p2runtime.Entry("late", &off)})
				},
			}}}, nil
		}, host.ServeOptions{})
	})
	fixture := p2test.NewTestHost(t, p2test.TestHostOptions{Worker: true, Facets: []host.Facet{p2test.WorkerFacet(&p2runtime.HarnessParts{}, nil, nil), facet}})
	extensions, err := fixture.Host.Registry(p2test.Background, p2runtime.Extensions.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	switches, err := fixture.Host.Registry(p2test.Background, p2runtime.ExtensionSwitches.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	late, err := fixture.Host.Get(p2test.Background, lateOffer, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return lateExtension{registry: p2runtime.DurableRegistry(extensions, switches), extensions: extensions, switches: switches, late: late, woken: &atomic.Int32{}}
}

// holdingObserver is an observer of a registry's composed value that stops the batch that composes the value it waits for
// (holds says which): it tells the test the batch is in its observers, and keeps the host's loop until the test lets it go.
type holdingObserver struct {
	holds    func(composed any) bool
	inBatch  chan struct{}
	release  chan struct{}
	notified sync.Once
}

func newHoldingObserver(holds func(composed any) bool) *holdingObserver {
	return &holdingObserver{holds: holds, inBatch: make(chan struct{}), release: make(chan struct{})}
}

func (o *holdingObserver) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	if call.Kind != chord.WatchCallValue || !o.holds(call.Value) {
		return
	}
	o.notified.Do(func() { close(o.inBatch) })
	<-o.release
}

func (*holdingObserver) End(chord.WatchEnd) {}

var _ state.NativeObserver[any] = (*holdingObserver)(nil)

// whileHeld calls method of the late plugin, holds the batch it composes in a later observer of source's value (the one
// that holds says), and returns the registry's Snapshot taken then, and how many times its listener was called by then.
func (late lateExtension) whileHeld(t *testing.T, source *registries.RegistryValueSource, holds func(composed any) bool, method string) (snapshot durable.RegistrySnapshot, woken int32) {
	t.Helper()
	observer := newHoldingObserver(holds)
	observation, err := source.Observe(observer, p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	defer observation.Detach()
	called := make(chan error, 1)
	go func() {
		_, err := late.late.Call(method, nil, p2test.Background)
		called <- err
	}()
	released := false
	defer func() {
		if !released {
			close(observer.release)
		}
	}()
	<-observer.inBatch
	snapshot, woken = late.registry.Snapshot(), late.woken.Load()
	close(observer.release)
	released = true
	if err := <-called; err != nil {
		t.Fatal(err)
	}
	return snapshot, woken
}

func hasLate(composed any) bool {
	table, _ := composed.(*p2runtime.KeyedTable[*p2runtime.P2Extension])
	if table == nil {
		return false
	}
	_, ok := table.Get("late")
	return ok
}

func lateSwitchedOff(composed any) bool {
	table, _ := composed.(*p2runtime.KeyedTable[*bool])
	if table == nil {
		return false
	}
	on, ok := table.Get("late")
	return ok && on != nil && !*on
}

// Pi's registry reads `extensions.value` (durable.ts `snapshot`): the composition of the batch that made it, for any task
// that runs after the batch. The Snapshot is read off the host's loop, so what it reads is recorded in the batch that
// composed, by the registry's own observer: the observers after it, and everything after the batch, see it recorded. A
// refresh delivered after the batch (a watch's delivery goroutine) left a window after the composition in which a turn
// took a Snapshot without it (the first turn of a worker, listing code mode's tools, missing the agent's or an MCP tool).
// Here the batch is held in a later observer of the same value: the Snapshot of the registry, taken then, lists the
// contribution.
func TestDurableRegistrySnapshotListsAContributionAsSoonAsTheBatchThatComposedItNotifiedItsObservers(t *testing.T) {
	late := newLateExtension(t)
	late.subscribe()
	snapshot, woken := late.whileHeld(t, late.extensions, hasLate, "offer")
	if snapshot.Extension("late") == nil {
		t.Error("the Snapshot taken in the batch that composed the extension does not list it")
	}
	// The listener wakes the scheduler that reads the Snapshot: it is called in that batch, after the Snapshot reads it.
	if woken != 1 {
		t.Errorf("the listener was called %d times in the batch that composed the extension, want 1", woken)
	}
}

// The switches are composed likewise: an extension switched off (the `codemode` setting turned off, with the agent
// plugin staying) is left out of the Snapshot of any turn that starts after the batch that switched it.
func TestDurableRegistrySnapshotLeavesOutAnExtensionAsSoonAsTheBatchThatSwitchedItOffNotifiedItsObservers(t *testing.T) {
	late := newLateExtension(t)
	if _, err := late.late.Call("offer", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	late.subscribe()
	if late.registry.Snapshot().Extension("late") == nil {
		t.Fatal("the Snapshot does not list the extension before it is switched off")
	}
	snapshot, woken := late.whileHeld(t, late.switches, lateSwitchedOff, "switchOff")
	if snapshot.Extension("late") != nil {
		t.Error("the Snapshot taken in the batch that switched the extension off lists it")
	}
	if woken != 1 {
		t.Errorf("the listener was called %d times in the batch that switched the extension off, want 1", woken)
	}
}

// A composition made after the registry was made and before it was subscribed to (the Harness opens the storage between
// them) is in the Snapshot once Subscribe returns.
func TestDurableRegistrySnapshotListsAContributionMadeBeforeItWasSubscribedTo(t *testing.T) {
	late := newLateExtension(t)
	if _, err := late.late.Call("offer", nil, p2test.Background); err != nil {
		t.Fatal(err)
	}
	stop := late.subscribe()
	defer stop()
	if late.registry.Snapshot().Extension("late") == nil {
		t.Error("the Snapshot taken after Subscribe does not list the extension composed before it")
	}
}
