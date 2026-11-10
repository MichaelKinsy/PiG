package host

import (
	"context"
	"slices"
	"strings"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/hostdata"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/host/host-data.ts
//
// Host data operations on a live host (decision "Documents and table rows can belong to a host"; spec
// `docs/host-keyed/spec.md` §5): the gate that serializes them and keeps activations' load phases out while one runs,
// and the coordination of one import or delete with the live objects owning data of the address: their lines held,
// what they hold of it read again inside the commit step, and adopted in one host batch after `COMMIT`.
//
// Forced Go differences:
//   - Async contract: `turn`, `importInto` and `deleteFrom` are Pi's `async` functions; each blocks the calling goroutine
//     (never one of the host's sections) and returns its error, and a rejection is the returned error. The wait for the
//     operation's turn, for the load phases under way and for each object's line ends with the caller's ctx (Pi's
//     `awaitWithContext`); an abandoned wait for a line releases it when it comes, on an owned task of the host's loop.
//     What Pi does in the synchronous parts of these functions (the gate's counters, the objects, their slots and live
//     queries, the adoption) runs in sections of the host. The reads inside the commit step run on the goroutine
//     committing, outside any section, taking sections only where documents do.
//   - `enter` is called inside a section (the activation's) and returns a keyedPromise, nil where Pi returns `undefined`.
//   - `turn`'s work and `exclusive`'s section return only an error: a result is a variable the closures share, since a Go
//     func value cannot be generic (Pi's `Promise<T>` and `<R>`).
//   - Pi's `Set<string>` of owners is a map, and an ordered unique slice where the order decides (the owners to activate).
//   - A site is a struct of the host's own pieces (Pi's structural type): `objects` and `activate` are closures, `enter`
//     runs a section of the host.
//   - Objects are sorted by identity in byte order; every operation sorts them the same way, which is all a lock order needs.

// hostDataGate is the host data gate: operations (writers) one at a time in call order; activations' load phases (readers)
// while none is exclusive. A writer becomes exclusive after its preparation (which may activate owners, as readers) and
// waits for the load phases under way; activations arriving meanwhile wait until it ends.
type hostDataGate struct {
	loop sectionLoop
	// tail is closed once the last operation queued ended (and every earlier one): the turn of the next one. Operations
	// queue from any goroutine, in the order they reach mu.
	mu      sync.Mutex
	tail    chan struct{}
	readers int
	// isExclusive is whether an operation is exclusive (readers wait).
	isExclusive bool
	// opened are the readers waiting for the exclusive operation's end; drained the operation waiting for the readers.
	opened  []func()
	drained func()
}

// newHostDataGate is a gate with no operation queued, whose counters are sections of loop.
func newHostDataGate(loop sectionLoop) *hostDataGate {
	free := make(chan struct{})
	close(free)
	return &hostDataGate{loop: loop, tail: free}
}

// enter enters an activation's load phase, in a section: at once (nil, no extra turn on the common path) unless an operation
// is exclusive; then the returned promise settles once it ended. Every entry is paired with `leave`.
func (g *hostDataGate) enter() *keyedPromise[struct{}] {
	if !g.isExclusive {
		g.readers++
		return nil
	}
	entered := newKeyedPromise[struct{}](g.loop)
	var wait func()
	wait = func() {
		if g.isExclusive {
			// The operation's end resolves this wait; the reader resumes in a later turn and looks again.
			g.opened = append(g.opened, func() { g.loop.Queue(wait) })
			return
		}
		g.readers++
		entered.resolve(struct{}{})
	}
	wait()
	return entered
}

// leave leaves a load phase `enter` began, in a section.
func (g *hostDataGate) leave() {
	g.readers--
	if g.readers == 0 && g.drained != nil {
		drained := g.drained
		g.drained = nil
		drained()
	}
}

// queue takes the turn after the last one queued: the channel to wait for, and the one this operation closes when its
// turn ends.
func (g *hostDataGate) queue() (previous <-chan struct{}, done chan struct{}) {
	done = make(chan struct{})
	g.mu.Lock()
	defer g.mu.Unlock()
	previous, g.tail = g.tail, done
	return previous, done
}

// turn runs one operation in its turn (operations one at a time, in call order): work gets `exclusive`, which runs its
// section once the load phases under way ended, with activations kept out until it returns; between exclusive sections
// activations load (an owner the operation activates). The waits end with ctx's abort; the turn passes on.
func (g *hostDataGate) turn(work func(exclusive func(section func() error) error) error, ctx context.Context) error {
	previous, done := g.queue()
	if _, err := chordctx.Receive(ctx, previous); err != nil {
		// Given up while waiting for its turn: the next one waits for the earlier ones still.
		g.loop.Go(func() {
			<-previous
			close(done)
		})
		return err
	}
	defer close(done)
	return work(func(section func() error) error { return g.exclusive(section, ctx) })
}

// exclusive runs section exclusively: activations wait from now on; the load phases under way are waited for first.
func (g *hostDataGate) exclusive(section func() error, ctx context.Context) error {
	var drained chan struct{}
	g.loop.Run(func() {
		g.isExclusive = true
		if g.readers > 0 {
			drained = make(chan struct{})
			g.drained = func() { close(drained) }
		}
	})
	defer g.loop.Run(func() {
		g.isExclusive = false
		g.drained = nil
		opened := g.opened
		g.opened = nil
		for _, open := range opened {
			open()
		}
	})
	if drained != nil {
		if _, err := chordctx.Receive(ctx, drained); err != nil {
			return err
		}
	}
	return section()
}

// hostDataSite is what a host data operation needs of its host.
type hostDataSite struct {
	store *store.ChordStore
	batch *state.Batch
	gate  *hostDataGate
	// enter runs work as a section of the host.
	enter func(work func())
	// objects are the host's objects; in a section.
	objects func() []*chordObject
	// activate activates the served `local` type owner (empty params) and returns its release; nil when it is not served here.
	activate func(owner string, ctx context.Context) (release func(), err error)
}

// importSectionKind is what one exclusive section of an import found.
type importSectionKind int

const (
	// importActivate: owners to activate first.
	importActivate importSectionKind = iota
	// importDone: the import done.
	importDone
)

// importSection is one exclusive section of an import: owners to activate first, or the import done.
type importSection struct {
	kind   importSectionKind
	owners []string
}

// importInto imports a host's data archive into the site's store under `options.Address` or the archive's, coordinated with
// the live owners (spec §5): read and checked; then, in the operation's turn and exclusively, against the catalog as the
// load phases left it: the tables whose rows must migrate and their owners' histories. An owner not live for one is
// activated (between exclusive sections) and the section runs again; with every history at hand, the commit with what the
// owners hold of the address adopted. Owners activated for it are released when the operation ends, however.
func importInto(site hostDataSite, parts store.PartSource, ctx context.Context, options store.HostImportOptions) (store.HostDataCounts, error) {
	archive, err := store.ReadHostArchive(parts, ctx)
	if err != nil {
		return store.HostDataCounts{}, err
	}
	target := archive.Host
	if options.Address != nil {
		if target, err = store.StoredText(options.Address.Key, "importHostData: address"); err != nil {
			return store.HostDataCounts{}, err
		}
	}
	existing := options.Existing
	if existing == "" {
		existing = store.ExistingRefuse
	}
	var counts store.HostDataCounts
	err = site.gate.turn(func(exclusive func(section func() error) error) error {
		activated := map[string]func(){}
		var order []string
		defer func() {
			for _, owner := range order {
				activated[owner]()
			}
		}()
		for {
			var section importSection
			err := exclusive(func() error {
				migrating := hostdata.TablesToMigrate(site.store, archive)
				var histories map[string]any
				site.enter(func() { histories = historiesOf(site.objects(), migrating) })
				var missing []string
				for _, table := range migrating {
					if _, found := histories[table]; found {
						continue
					}
					if owner := ownerOf(table); !slices.Contains(missing, owner) {
						missing = append(missing, owner)
					}
				}
				if len(missing) > 0 {
					section = importSection{kind: importActivate, owners: missing}
					return nil
				}
				owners, err := hostdata.Owners(site.store, target, archive, ctx)
				if err != nil {
					return err
				}
				plan := hostdata.Plan{Target: target, Existing: existing, Histories: histories}
				counts, err = coordinated(site, owners, target, ctx, func(commit hostdata.Commit) (store.HostDataCounts, error) {
					return hostdata.Import(site.store, archive, plan, commit, ctx)
				})
				section = importSection{kind: importDone}
				return err
			})
			if err != nil {
				return err
			}
			if section.kind == importDone {
				return nil
			}
			for _, owner := range section.owners {
				// Activated once already, and still without a declaration of the table: it has none to migrate it.
				var release func()
				if _, again := activated[owner]; !again {
					if release, err = site.activate(owner, ctx); err != nil {
						return err
					}
				}
				if release == nil {
					return data.Invalid(data.ReasonArgs, "importHostData: "+owner+" is not served here to migrate its tables")
				}
				activated[owner] = release
				order = append(order, owner)
			}
		}
	}, ctx)
	return counts, err
}

// compareUTF16 orders strings by UTF-16 code unit, as JavaScript's `<` does (not by byte or code point).
func compareUTF16(a, b string) int {
	return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b))
}

// ownerOf is the owner of table `table` (its SQL name `<owner>:<table>`).
func ownerOf(table string) string {
	return table[:strings.IndexByte(table, ':')]
}

// deleteFrom deletes the data of address from the site's store, coordinated with the live owners (spec §5).
func deleteFrom(site hostDataSite, address contracts.Address, ctx context.Context) (store.HostDataCounts, error) {
	key, err := store.StoredText(address.Key, "deleteHostData: address")
	if err != nil {
		return store.HostDataCounts{}, err
	}
	var counts store.HostDataCounts
	err = site.gate.turn(func(exclusive func(section func() error) error) error {
		return exclusive(func() error {
			owners, err := hostdata.Owners(site.store, key, nil, ctx)
			if err != nil {
				return err
			}
			counts, err = coordinated(site, owners, key, ctx, func(commit hostdata.Commit) (store.HostDataCounts, error) {
				return hostdata.Delete(site.store, key, commit, ctx)
			})
			return err
		})
	}, ctx)
	return counts, err
}

// historiesOf are the live objects' declarations of tables (SQL names): the histories their rows migrate through.
func historiesOf(objects []*chordObject, tables []string) map[string]any {
	histories := map[string]any{}
	for _, table := range tables {
		for _, object := range objects {
			if definition := object.declaredTable(table); definition != nil {
				histories[table] = definition
			}
		}
	}
	return histories
}

// slotRead is a host-keyed slot's key a change reads again and adopts.
type slotRead struct {
	slot    *documents.KeyedSlot
	changed documents.ChangedDocument
}

// queryRead is a live query a change re-runs and adopts.
type queryRead struct {
	query *store.LiveQuery
	run   store.QueryRun
}

// hostDataReads is what the live owners hold of a host's data, read again inside the commit step, after its writes.
type hostDataReads struct {
	read func(step *store.Step) error
}

func (hostDataReads) StageOrder() store.WriteOrder { return store.OrderWrite }

func (reads hostDataReads) Apply(step *store.Step) ([]store.StoreWrite, error) {
	return nil, reads.read(step)
}

// coordinated runs change (one commit changing the data of host key) holding the lines of the live objects of owners and of
// those with persisted documents keyed by the key's kind, taken in identity order (cancellable). Inside the commit step it
// reads what those objects hold of the key (resident and pinned documents, live queries over tables with a host column of
// the kind); after `COMMIT` it adopts all of it in one host batch. The commit is admitted only while ctx is not aborted
// (`released` otherwise, nothing written). A document an object cannot read fails that object after the batch.
func coordinated[T any](site hostDataSite, owners map[string]struct{}, key string, ctx context.Context, change func(commit hostdata.Commit) (T, error)) (T, error) {
	var none T
	kind := contracts.KindOfKey(key)
	var objects []*chordObject
	site.enter(func() {
		for _, object := range site.objects() {
			if _, owned := owners[object.owner()]; owned || len(object.documents.HostSlots(kind)) > 0 {
				objects = append(objects, object)
			}
		}
	})
	slices.SortFunc(objects, func(a, b *chordObject) int { return compareUTF16(a.identity, b.identity) })
	var releases []func()
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	for _, object := range objects {
		release, err := acquireLine(site.gate.loop, object.documents, ctx)
		if err != nil {
			return none, err
		}
		releases = append(releases, release)
	}
	var slots []*slotRead
	var queries []*queryRead
	site.enter(func() {
		for _, object := range objects {
			for _, slot := range object.documents.HostSlots(kind) {
				if slot.Holds(key) {
					slots = append(slots, &slotRead{slot: slot, changed: documents.ChangedDocument{Kind: documents.ChangedUnchanged}})
				}
			}
		}
		for _, object := range objects {
			for _, query := range object.queries().OnHostKind(kind) {
				queries = append(queries, &queryRead{query: query})
			}
		}
	})
	// Counted and adopted under the caller's telemetry; nothing after admission is cancelled by its signal.
	quiet := chordctx.WithoutAbortSignal(ctx)
	reads := hostDataReads{read: func(step *store.Step) error {
		for _, each := range slots {
			changed, err := each.slot.ReadChanged(key, step, quiet)
			if err != nil {
				return err
			}
			each.changed = changed
		}
		for _, each := range queries {
			each.run = each.query.Rerun(step, quiet)
		}
		return nil
	}}
	commit := hostdata.Commit{
		Fence: hostdata.Fence{
			Admit: func() bool { return ctx.Err() == nil },
			Adopt: func(int64, []int64, int) error {
				var failed []*slotRead
				var err error
				site.enter(func() {
					err = site.batch.Commit(func() error {
						for _, each := range slots {
							if err := each.slot.AdoptChanged(key, each.changed); err != nil {
								return err
							}
							if each.changed.Kind == documents.ChangedFailed {
								failed = append(failed, each)
							}
						}
						// Each read ran in the step before `COMMIT`.
						for _, each := range queries {
							each.query.Adopt(each.run)
						}
						return nil
					}, quiet)
					if err != nil {
						return
					}
					// Outside the batch: failing an object ends its attempt, which publishes on its own.
					for _, each := range failed {
						each.slot.Owner().Fail(each.changed.Error)
					}
				})
				return err
			},
		},
		After: []any{reads},
	}
	return change(commit)
}

// acquireLine is the line of owned, waited for until ctx aborts (an abandoned wait releases the line when it comes).
func acquireLine(loop sectionLoop, owned *documents.Documents, ctx context.Context) (func(), error) {
	acquired := make(chan func())
	abandoned := make(chan struct{})
	loop.Go(func() {
		release := owned.Line().Acquire(chordctx.Background())
		select {
		case acquired <- release:
		case <-abandoned:
			release()
		}
	})
	if ctx.Err() != nil {
		close(abandoned)
		return nil, context.Cause(ctx)
	}
	select {
	case release := <-acquired:
		return release, nil
	case <-ctx.Done():
		close(abandoned)
		return nil, context.Cause(ctx)
	}
}
