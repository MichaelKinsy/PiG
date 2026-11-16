package host

import (
	"context"
	"sort"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/src/host/read-declarations.ts, its pure checks (slice 14 §3, §4): what `self.queries`,
// `self.liveQuery`, `self.count` and `self.list` check, so an attempt and staging's recording `self` make the same ones
// (a reload whose new code declares a read wrongly keeps the plugin on its old code). Pi's checks of the list's sources'
// shape (`oneSource`'s `[[query, args]]`) and of the builders' being an object of functions check what Go's types state,
// and have no Go form.

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER: Pi's `Number.isSafeInteger` bound of a page size.
const maxSafeInteger = 1<<53 - 1

// firstOf checks the page size `{ first: n }` of a live query or a list: a positive integer (a bound is required).
func firstOf(label, declaration string, first int64) error {
	if first < 1 || first > maxSafeInteger {
		return declarationError("%s: self.%s: { first: n } is required, a positive integer", label, declaration)
	}
	return nil
}

// listName checks a list's name (non-empty, once per object: its bookmarks are kept by it) and remembers it in listed.
func listName(label, name string, listed map[string]struct{}) error {
	if name == "" {
		return declarationError("%s: self.list: a list has a name", label)
	}
	if _, twice := listed[name]; twice {
		return declarationError(`%s: list "%s" is declared twice`, label, name)
	}
	listed[name] = struct{}{}
	return nil
}

// oneSource checks the sources of a list: one `[query, args]` (lists over several sources are not built yet: `slices.md`,
// "Carry-over", slice 14).
func oneSource(label string, sources []ListSource) (ListSource, error) {
	if len(sources) != 1 {
		return ListSource{}, declarationError("%s: self.list: one source, [[namedQuery, args]]", label)
	}
	return sources[0], nil
}

// namedQueryExpected is the refusal of what `self.liveQuery`, `self.count` or `self.list` (declaration) was given as
// its named query.
func namedQueryExpected(label, declaration string) error {
	return declarationError("%s: self.%s reads a named query; a table of this object is read with self.query", label, declaration)
}

// undeclaredTable is the refusal of a table given to `self.query` or `self.queries` (declaration) that the factory did
// not declare.
func undeclaredTable(label, declaration string) error {
	return declarationError("%s: self.%s: the table is not one this factory declared", label, declaration)
}

// namedBuilder is one entry of `self.queries`' builders, checked: its builder, and its argument schema when it brings one.
type namedBuilder struct {
	name    string
	builder func(args delta.JsonValue) *store.Query
	args    *contracts.Snapshot
}

// namedBuilders is the builders `self.queries` was given, checked (each has a builder; a private query's schema is closed),
// in name order.
func namedBuilders(label string, builders map[string]QueryBuilder) ([]namedBuilder, error) {
	names := make([]string, 0, len(builders))
	for name := range builders {
		names = append(names, name)
	}
	sort.Strings(names)
	checked := make([]namedBuilder, 0, len(names))
	for _, name := range names {
		entry := builders[name]
		if entry.Build == nil {
			return nil, declarationError(`%s: self.queries: "%s" has no builder`, label, name)
		}
		var args *contracts.Snapshot
		if entry.Args.Node != nil {
			snapshot, err := contracts.SnapshotArguments(entry.Args, label+`: query "`+name+`": args`)
			if err != nil {
				return nil, err
			}
			args = snapshot
		}
		// The builder is plugin code: what it returns is checked when a read opens.
		checked = append(checked, namedBuilder{name: name, builder: entry.Build, args: args})
	}
	return checked, nil
}

// openReaders opens each of readers in turn (slice 14 §3.2), named to long as label while its first run is awaited, as one
// step of a parked attempt (or of an admission): ready once all have values; else parked on the first that is not, `loading`
// while its read has not run yet (a table it reads is being created or migrated), `failed { waiting }` on a read it waits
// on; or broken (its own code). Parked, it says why (cause), as a parked dependency does: an admission parked on a read is
// its attempt's first check, which a reload's report waits for. A reader that is not ready is followed by wake from then on:
// its next adoption (its table ready, a good run after a failed one) checks again. A reader's Open blocks on the store's
// first run: it runs on an owned task, and how it ended is read in the section that settles it (awaitTail), the same turn as
// the follow below; settle is called in a section.
func openReaders(host *chordHost, readers []*reader, wake state.Dependent, long blocked, label string, ctx context.Context, settle func(parkResult)) {
	var open func(index int)
	open = func(index int) {
		if index == len(readers) {
			settle(parkResult{kind: parkingReady})
			return
		}
		each := readers[index]
		long.blockedBy(&blocker{label: label, state: blockerLoading})
		// `const opened = await reader.open(context); if (opened.kind === "ready") continue; reader.source.dependents.add(wake)`
		// (read-declarations.ts:43-45): the check of the reading is Open's tail, read in the section that settles the await and
		// followed, when the reader is not ready, in the same turn: an adoption between the two would make the reader ready and
		// tell nobody, and the attempt would park on a ready reader for good.
		awaitTail(host, func() func() Opened { return each.Open(ctx) }).then(func(opened Opened, _ error) {
			if opened.Kind == OpenedReady {
				open(index + 1)
				return
			}
			each.source().DependentSet().Add(wake)
			if opened.Kind == OpenedLoading {
				loading := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: label + " is loading"}, nil)
				cause := data.WaitingFor(label, loading)
				settle(parkResult{kind: parkingParked, shown: refLoading, firstPass: false, cause: &cause})
				return
			}
			long.blockedBy(&blocker{label: label, state: blockerFailed})
			err := data.AsChordError(opened.Err, label)
			if opened.Kind == OpenedBroken {
				settle(parkResult{kind: parkingBroken, err: err})
				return
			}
			shown := waitingOn(label, err)
			settle(parkResult{kind: parkingParked, shown: shown, firstPass: true, cause: &shown.Cause})
		})
	}
	open(0)
}

// ReaderSite is where an attempt's readers live: the store and batch, the object's label, its failure reports, its wake and
// the loop task a reader's un-awaited read runs on.
type ReaderSite struct {
	Site  store.ReadSite
	Label string
	// Report reports a failed run of query after the reader went live (`query_failed`, once per episode).
	Report func(query string, err error)
	// Wake tells the attempt, waiting on a reader, to open it again.
	Wake func()
	// Go runs task as an owned task of the host's loop: the reader's read for a source that moved while it is open (Pi:
	// `void this.#reading.read(...)`, query-reader.ts:123).
	Go func(task func())
}

// readerSite is where the readers label declares on host live (an attempt's, an admission's): a reader needs the host's
// store. wake tells whoever waits on a reader to open it again; spawn is the loop's Go.
func readerSite(host *chordHost, label string, wake func(), spawn func(task func())) (ReaderSite, error) {
	if host.store == nil {
		return ReaderSite{}, declarationError("%s: reading a named query needs a store", label)
	}
	return ReaderSite{
		Site:  store.ReadSite{Store: host.store, Batch: host.batch},
		Label: label,
		Report: func(query string, err error) {
			host.report(HostError{Kind: ErrQueryFailed, Object: label, Query: query, Error: err})
		},
		Wake: wake,
		Go:   spawn,
	}, nil
}

// rowsReader is the reader of `self.liveQuery(query, args, first)`: its value the first `first` rows as `{ rows, more }`.
func rowsReader(at ReaderSite, query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args any, first int64) (*reader, *store.LiveQuery, error) {
	if err := firstOf(at.Label, "liveQuery", first); err != nil {
		return nil, nil, err
	}
	shape := store.LiveShape{
		Kind: "rows",
		// Checked above: a positive integer.
		Range: store.Range{Limit: int(first)},
		Present: func(rows []delta.JsonValue, more bool) delta.JsonValue {
			return delta.JsonObjectOf("rows", rows, "more", more)
		},
	}
	return liveReader(at, "liveQuery", query, args, shape)
}

// liveReader is the reader of `self.liveQuery(query, args, first)` (shape `rows`) or of `self.count(query, args)`
// (`count`).
func liveReader(at ReaderSite, declaration string, query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args any, shape store.LiveShape) (*reader, *store.LiveQuery, error) {
	given, err := givenQuery(at, declaration, query, args)
	if err != nil {
		return nil, nil, err
	}
	live := store.NewLiveQuery(at.Site, readerOwnerOf(at, given), shape)
	return newReader(given, newLiveReading(live), at.Wake, at.Go), live, nil
}

// listReader is the reader of `self.list(name, [[query, args]], { first })`; memory is what the object keeps of the list.
func listReader(at ReaderSite, name string, source ListSource, first int, memory *ListMemory) (*reader, *list, error) {
	given, err := givenQuery(at, `list "`+name+`"`, source.Query, source.Args)
	if err != nil {
		return nil, nil, err
	}
	l := newList(at.Site, readerOwnerOf(at, given), first, memory, at.Go)
	return newReader(given, l, at.Wake, at.Go), l, nil
}

// givenQuery is the named query a reader was given with its arguments, checked: a named query (a table of the object is
// the old form's argument), and arguments that are JSON (their schema checks them when the read opens) or a watchable of
// them.
func givenQuery(at ReaderSite, declaration string, query contracts.NamedQuery[delta.JsonValue, delta.JsonValue], args any) (*GivenQuery, error) {
	handle, ok := query.(NamedQueryHandle)
	if !ok {
		return nil, namedQueryExpected(at.Label, declaration)
	}
	source, err := resolveWatchable(unwrapMember(args))
	if err != nil {
		return nil, err
	}
	given := GivenArguments{Kind: GivenFixed, Value: args}
	if source != nil {
		given = GivenArguments{Kind: GivenFollowing, Source: source}
	}
	return NewGivenQuery(handle, given, at.Site.Batch), nil
}

// readerOwner is who a reader's source belongs to: its object, reporting failures under the query's name.
type readerOwner struct {
	at    ReaderSite
	given *GivenQuery
}

// Label implements state.FramedOwner.
func (o readerOwner) Label() string { return o.at.Label }

// Report implements state.FramedOwner.
func (o readerOwner) Report(err error) { o.at.Report(o.given.Handle.Label(), err) }

func readerOwnerOf(at ReaderSite, given *GivenQuery) state.FramedOwner {
	return readerOwner{at: at, given: given}
}
