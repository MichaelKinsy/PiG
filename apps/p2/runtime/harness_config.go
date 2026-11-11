package runtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports apps/p2/src/runtime/worker.ts (`config.value.get("default")`, read by the Harness's `models` and `settings`).

// HarnessConfigView is the composition of the HarnessConfig registry as the last batch that composed it left it. Pi reads
// `config.value` wherever the Harness needs model access or a setting, and the scheduler reads the settings in the middle of
// a pass (`#restartLimit` of a task cut off, the contexts' retention). Here the scheduler holds its own lock for the pass,
// and a read of a registry's value is a section of the host's loop: a section that held the loop while it waited for that
// lock (a batch's observer waking the scheduler, as Durable's registry does) and a pass that held the lock while it waited
// for the loop deadlocked (a worker restarted after a kill never resumed its script). The view is recorded by a synchronous
// observer (Pi's `observe`, which runs inside the batch that composed), so a read waits for nothing.
type HarnessConfigView struct {
	table atomic.Pointer[KeyedTable[HarnessConfigEntry]]
	// recording orders the observer's records with the record of the value the observation started at.
	recording sync.Mutex
	advanced  bool
	detach    func()
}

// ObserveHarnessConfig is the view of config from its current composition on. It returns Observe's error: a registry with no
// composition to start at.
func ObserveHarnessConfig(config *registries.RegistryValueSource) (*HarnessConfigView, error) {
	view := &HarnessConfigView{}
	observation, err := config.Observe(&harnessConfigObserver{view: view}, chordctx.Background())
	if err != nil {
		return nil, err
	}
	view.recording.Lock()
	defer view.recording.Unlock()
	// A composition recorded since the observer attached is newer than the one the observation started at.
	if !view.advanced {
		view.table.Store(harnessConfigTable(observation.Value))
	}
	view.detach = observation.Detach
	return view, nil
}

// Default is the models plugin's configuration entry.
func (view *HarnessConfigView) Default() (HarnessConfigEntry, bool) {
	return view.table.Load().Get("default")
}

// Stop ends the observation.
func (view *HarnessConfigView) Stop() {
	view.recording.Lock()
	detach := view.detach
	view.recording.Unlock()
	if detach != nil {
		detach()
	}
}

func harnessConfigTable(value any) *KeyedTable[HarnessConfigEntry] {
	table, _ := value.(*KeyedTable[HarnessConfigEntry])
	return table
}

// harnessConfigObserver records each composition of the registry, inside the batch that made it. It is used as a pointer:
// the source finds an observer to detach by comparing interfaces, which panics on a struct holding a func.
type harnessConfigObserver struct{ view *HarnessConfigView }

func (observer *harnessConfigObserver) Advance(call chord.NativeWatchCall[any], _ context.Context) {
	// A failed composition keeps the last one: nothing to reconsider.
	if call.Kind != chord.WatchCallValue {
		return
	}
	view := observer.view
	view.recording.Lock()
	defer view.recording.Unlock()
	view.advanced = true
	view.table.Store(harnessConfigTable(call.Value))
}

func (*harnessConfigObserver) End(chord.WatchEnd) {}

var _ state.NativeObserver[any] = (*harnessConfigObserver)(nil)
