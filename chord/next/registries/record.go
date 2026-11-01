package registries

import (
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/registries/record.ts (a registry on a host, slice 8 §1: its provider (replaced or removed by a reload, slice 7b §3), its
// contributions in table order, the table and its lazy composition, the reports of conflicts and failures, and the
// values its readers follow by version), in its dependency-model final state.

// RegistryErrorKind tags a RegistryError.
type RegistryErrorKind string

const (
	// ErrorRegistryConflict: `compose` reported a conflict it resolved.
	ErrorRegistryConflict RegistryErrorKind = "registry_conflict"
	// ErrorRegistryFailed: `compose` (or an older version's `value` adapter) failed: the last good composition stays.
	ErrorRegistryFailed RegistryErrorKind = "registry_failed"
	// ErrorContributionFailed: a watchable contribution failed or ended, or its `contribution` adapter failed: it is left
	// out until it heals.
	ErrorContributionFailed RegistryErrorKind = "contribution_failed"
	// ErrorContributionRefused: a contribution of a version the provider does not accept (newer, or older without an
	// adapter) is left out.
	ErrorContributionRefused RegistryErrorKind = "contribution_refused"
)

// RegistryError is what a registry reports to its host's `onError`, by Kind; each once per episode. Upstream's tagged
// union is one struct tagged by Kind: every kind has Registry; registry_conflict also Message and Plugins;
// registry_failed also Error; contribution_failed also Plugin, Type (empty for a facet) and Error;
// contribution_refused also Plugin, Type and Version. The other fields are zero.
type RegistryError struct {
	Kind     RegistryErrorKind
	Registry string
	Message  string
	Plugins  []string
	Plugin   string
	Type     string
	Version  int
	Error    error
}

// provided is the provider as served: the defining plugin's version, `compose` and adapters.
type provided struct {
	version  int
	provider RegistryProvider[any, any]
}

// composition is where a composition stands: its last good value (with the contributions it was built from) and its
// failure while `compose` fails. A nil *composition is "never composed".
type composition struct {
	good    *goodComposition
	failure *compositionFailure
}

type goodComposition struct {
	value any
	from  []*RegistryContribution
}

type compositionFailure struct{ err error }

// conflictSet is the conflicts of one composition by key, in the order they were first reported.
type conflictSet struct {
	keys  []string
	items map[string]RegistryConflict
}

func (set *conflictSet) put(key string, conflict RegistryConflict) {
	if _, ok := set.items[key]; !ok {
		set.keys = append(set.keys, key)
	}
	set.items[key] = conflict
}

// RegistryRecord is one registry of a host.
type RegistryRecord struct {
	Name     string
	batch    *state.Batch
	report   func(RegistryError)
	provider *provided
	// contributions are those of live contributors, sorted by plugin, then type (a facet's first); replaced whole on
	// every change, so a composition keeps the list it was built from.
	contributions []*RegistryContribution
	// table is the last table composed, kept so an unchanged table composes nothing; nil before the first.
	table       []*RegistryEntry[any]
	dirty       bool
	composition *composition
	// composing is set while its table is built and composed: a read then is a cycle of registries.
	composing bool
	// conflicts are the keys of the conflicts the last good composition reported: a conflict is reported once per
	// episode.
	conflicts map[string]struct{}
	// values are the values readers follow, by version, in the order their versions were first read.
	values        map[int]*RegistryValueSource
	valueVersions []int
}

// NewRegistryRecord makes the record of registry name on the host whose batch is batch; report receives its
// RegistryErrors.
func NewRegistryRecord(name string, batch *state.Batch, report func(RegistryError)) *RegistryRecord {
	return &RegistryRecord{
		Name:   name,
		batch:  batch,
		report: report,
		dirty:  true,
		values: map[int]*RegistryValueSource{},
	}
}

// ProvidedVersion is the provider's version; false while none serves it.
func (r *RegistryRecord) ProvidedVersion() (int, bool) {
	if r.provider == nil {
		return 0, false
	}
	return r.provider.version, true
}

// Value is the value readers of version follow; made by the first reader of a version.
func (r *RegistryRecord) Value(version int) *RegistryValueSource {
	value, ok := r.values[version]
	if !ok {
		value = NewRegistryValueSource(r, version, r.batch)
		r.values[version] = value
		r.valueVersions = append(r.valueVersions, version)
	}
	return value
}

// CheckProvider checks a registry provider as a setup served it (declaration policy: plugin code is bundled without
// type checks): `compose` is a function.
func CheckProvider(name string, provider RegistryProvider[any, any]) error {
	if provider.Compose == nil {
		return fmt.Errorf("registry %s: compose is a function", name)
	}
	return nil
}

// Serve serves the registry with provider at version (the defining plugin's facet, checked by CheckProvider when its
// setup ran), replacing the provider a reload changed, inside a batch: the table composes again (lazily).
func (r *RegistryRecord) Serve(version int, provider RegistryProvider[any, any]) {
	r.provider = &provided{version: version, provider: provider}
	r.table = nil
	r.invalidate()
}

// Unserve is a reload removing the defining plugin, inside a batch: no provider serves it; its readers' values fail
// `unavailable/undeclared` (a reader parked on it waits `not_served`), its contributions stay in the table.
func (r *RegistryRecord) Unserve() {
	r.provider = nil
	r.table = nil
	r.composition = nil
	r.invalidate()
}

// ComposeServed is the first composition of a served registry, at `load`'s publication or a reload's commit, inside
// its batch.
func (r *RegistryRecord) ComposeServed() error {
	if r.provider == nil {
		return nil
	}
	_, err := r.Start()
	return err
}

// Add adds a live contributor's contribution, inside a batch: the table changes.
func (r *RegistryRecord) Add(contribution *RegistryContribution) {
	at := slices.IndexFunc(r.contributions, func(other *RegistryContribution) bool {
		return compareContributors(contribution, other) < 0
	})
	if at < 0 {
		at = len(r.contributions)
	}
	// A new list per change (contributors come and go rarely): a composition keeps the one it was built from.
	r.contributions = slices.Insert(slices.Clone(r.contributions), at, contribution)
	contribution.Follow()
	r.invalidate()
}

// Remove removes an ending contributor's contribution, inside a batch: the table changes.
func (r *RegistryRecord) Remove(contribution *RegistryContribution) {
	at := slices.Index(r.contributions, contribution)
	if at < 0 {
		return
	}
	r.contributions = slices.Concat(r.contributions[:at], r.contributions[at+1:])
	contribution.Unfollow()
	r.invalidate()
}

// ContributionChanged is a followed contribution changing, inside a batch: the table is marked, composed when read or
// followed.
func (r *RegistryRecord) ContributionChanged() { r.invalidate() }

// Start is the current composition, composed now when the table changed: the value, or `compose`'s failure with the
// last good one; `unavailable/undeclared` while no provider serves it. A read while it composes is a cycle: the error
// is returned (upstream throws it).
func (r *RegistryRecord) Start() (state.NativeStart[any], error) {
	if r.composing {
		return state.NativeStart[any]{}, data.Invalid(data.ReasonCycle, fmt.Sprintf("registry %s is read while it composes", r.Name))
	}
	if r.provider == nil {
		err := data.NewChordError(data.ChordErrorInit{
			Code:    data.CodeUnavailable,
			Reason:  data.ReasonUndeclared,
			Message: fmt.Sprintf("registry %s: no plugin serves it here", r.Name),
		}, nil)
		return state.NativeStart[any]{Kind: state.StartFailed, Error: err}, nil
	}
	if r.dirty {
		r.recompose(*r.provider)
	}
	current := r.composition
	if current == nil {
		// Closed before its first composition (upstream reads an absent value there: a TypeError).
		err := data.NewChordError(data.ChordErrorInit{
			Code:    data.CodeReleased,
			Reason:  data.ReasonClosed,
			Message: fmt.Sprintf("registry %s: closed", r.Name),
		}, nil)
		return state.NativeStart[any]{Kind: state.StartFailed, Error: err}, nil
	}
	if current.failure != nil {
		start := state.NativeStart[any]{Kind: state.StartFailed, Error: current.failure.err}
		if current.good != nil {
			start.HasLast, start.Last = true, current.good.value
		}
		return start, nil
	}
	// A composition without a failure has a value.
	return state.NativeStart[any]{Kind: state.StartValue, Value: current.good.value}, nil
}

// ComposedFrom is the contributions the current good composition was built from; false while there is none.
func (r *RegistryRecord) ComposedFrom() ([]*RegistryContribution, bool) {
	if r.composition == nil || r.composition.good == nil {
		return nil, false
	}
	return r.composition.good.from, true
}

// InTable is whether every contribution of from (what a composition, or a mapping of it, was built from) is still in
// the table (decision 5: one that left may have closed the native its entry held).
func (r *RegistryRecord) InTable(from []*RegistryContribution) bool {
	current := r.contributions
	if slices.Equal(from, current) {
		return true
	}
	for _, each := range from {
		if !slices.Contains(current, each) {
			return false
		}
	}
	return true
}

// ValueAdapter is the adapter mapping the composed value back for readers of version; nil when there is none.
func (r *RegistryRecord) ValueAdapter(version int) func(current any) (any, error) {
	if r.provider == nil {
		return nil
	}
	return r.provider.provider.Accepts[version].Value
}

// Adapt runs an adapter of the provider as a pure compute, counted; a panic in it is its error.
func (r *RegistryRecord) Adapt(adapter func() (any, error)) (any, error) {
	r.batch.Count(state.CounterRegistryAdapts)
	var value any
	err := r.batch.Compute(func() error {
		var err error
		value, err = try(adapter)
		return err
	})
	return value, err
}

// ReportContribution reports a contribution left out (`contribution_failed`, `contribution_refused`; the caller keeps
// the episode).
func (r *RegistryRecord) ReportContribution(err RegistryError) { r.report(err) }

// Failed reports a failure of `compose` or of a value adapter (`registry_failed`; the caller keeps the episode).
func (r *RegistryRecord) Failed(err error) {
	r.report(RegistryError{Kind: ErrorRegistryFailed, Registry: r.Name, Error: state.HandedOut(err)})
}

// Close is the host closing: the contributions stop following their sources, the values end (a value kept by a reader
// still reads the last composition).
func (r *RegistryRecord) Close() {
	for _, contribution := range r.contributions {
		contribution.Unfollow()
	}
	r.contributions = nil
	r.table = nil
	r.dirty = false
	values := r.valueVersions
	for _, version := range values {
		r.values[version].Close()
	}
	r.values = map[int]*RegistryValueSource{}
	r.valueVersions = nil
}

// invalidate is the table changing: every version's value is marked and listed (each lists itself once per batch); while
// it is not composed since an earlier change, only listed (its dependents are marked already).
func (r *RegistryRecord) invalidate() {
	if r.dirty {
		for _, version := range r.valueVersions {
			r.values[version].Relisted()
		}
		return
	}
	r.dirty = true
	for _, version := range r.valueVersions {
		r.values[version].Recomposed()
	}
}

// recompose builds the table and composes it, unless it is the last one composed, entry for entry (`compose` is pure).
// A failure is an episode: reported once, the last good composition kept while every contribution it was built from is
// still in the table (decision 5: one whose contributor ended may hold a closed native); conflicts are reported once
// per episode, as their message and plugins.
func (r *RegistryRecord) recompose(served provided) {
	r.dirty = false
	r.composing = true
	defer func() { r.composing = false }()
	table, same := r.build(served.version, served.provider)
	if same && r.composition != nil {
		return
	}
	r.table = table
	conflicts := conflictSet{items: map[string]RegistryConflict{}}
	context := ComposeContext{Report: func(conflict RegistryConflict) {
		conflicts.put(conflict.Message+"\x00"+strings.Join(conflict.Plugins, "\x00"), conflict)
	}}
	entries := make(RegistryTable[any], len(table))
	for i, entry := range table {
		entries[i] = *entry
	}
	r.batch.Count(state.CounterRegistryComposes)
	var value any
	err := r.batch.Compute(func() error {
		var err error
		value, err = try(func() (any, error) { return served.provider.Compose(entries, context) })
		return err
	})
	if err != nil {
		previous := r.composition
		if previous == nil || previous.failure == nil {
			r.Failed(err)
		}
		var good *goodComposition
		if previous != nil && previous.good != nil && r.InTable(previous.good.from) {
			good = previous.good
		}
		r.composition = &composition{good: good, failure: &compositionFailure{err: err}}
		return
	}
	r.composition = &composition{good: &goodComposition{value: value, from: r.contributions}}
	for _, key := range conflicts.keys {
		if _, seen := r.conflicts[key]; seen {
			continue
		}
		conflict := conflicts.items[key]
		r.report(RegistryError{Kind: ErrorRegistryConflict, Registry: r.Name, Message: conflict.Message, Plugins: conflict.Plugins})
	}
	r.conflicts = make(map[string]struct{}, len(conflicts.keys))
	for _, key := range conflicts.keys {
		r.conflicts[key] = struct{}{}
	}
}

// build is the table of the live contributions now; the last one itself (same) when every entry is the same object.
func (r *RegistryRecord) build(version int, provider RegistryProvider[any, any]) (table []*RegistryEntry[any], same bool) {
	previous := r.table
	table = make([]*RegistryEntry[any], 0, len(r.contributions))
	same = previous != nil
	for _, contribution := range r.contributions {
		entry := contribution.Entry(version, provider)
		if entry == nil {
			continue
		}
		if same && (len(table) >= len(previous) || previous[len(table)] != entry) {
			same = false
		}
		table = append(table, entry)
	}
	if same && len(previous) == len(table) {
		return previous, true
	}
	return table, false
}

// compareContributors is table order: by plugin name, then type, a facet (no type) first; never load order. Names and
// types are ASCII (`[a-z0-9@/._~-]`), where byte order is upstream's UTF-16 order.
func compareContributors(a, b *RegistryContribution) int {
	left, right := a.Contributor, b.Contributor
	if left.Plugin != right.Plugin {
		return strings.Compare(left.Plugin, right.Plugin)
	}
	return strings.Compare(left.Type, right.Type)
}
