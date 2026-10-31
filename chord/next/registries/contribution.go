package registries

import (
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/registries/contribution.ts (one contributor's contribution to a registry, slice 8 §1:
// its table entry, made once per value, followed when it is a Chord watchable, left out and reported when it fails or
// is refused).

// Contributor is who contributes: a facet (by plugin, empty Type) or a host-lifetime object (by plugin and type).
type Contributor struct {
	Plugin string
	// Type is empty for a facet (upstream's `undefined`).
	Type string
}

// ContributionKind tags a ContributionContent.
type ContributionKind string

const (
	// ContributionPlain is a plain value, whatever methods it has (`undefined` included: a nil Value).
	ContributionPlain ContributionKind = "plain"
	// ContributionJSON is a Chord JSON watchable (a source or a constant).
	ContributionJSON ContributionKind = "json"
	// ContributionNative is a native source.
	ContributionNative ContributionKind = "native"
)

// ContributionContent is what a contribution is: a plain value, a Chord JSON watchable (a source or a constant), or a
// native source; the watchables are followed for their value. Upstream's tagged union is a struct tagged by Kind,
// with one field per variant: Value (plain), Source (json), Native (native); upstream's native `source` is Native here
// because the two sources have different Go types.
type ContributionContent struct {
	Kind   ContributionKind
	Value  any
	Source state.DeriveSource
	Native state.NativeSourceHandle
}

// outOf is why a contribution was left out since its last entry.
type outOf int

const (
	outNone outOf = iota
	outFailed
	outRefused
)

// adaptation is the last adaptation of an older contribution: the value and adapter it was made from, and its result.
type adaptation struct {
	from    any
	adapter func(old any) (any, error)
	value   any
}

// RegistryContribution is one contributor's contribution to a registry: a plain value, or a Chord source it follows
// as a dependent (a change marks the registry's table). Its table entry is made once per value (an older version's
// adapted once per value); a failure or refusal leaves it out, reported once per episode.
type RegistryContribution struct {
	// Record is the registry it contributes to.
	Record      *RegistryRecord
	Contributor Contributor
	// Version is the registry version its contributor was built against.
	Version int
	content ContributionContent
	adapted *adaptation
	entry   *RegistryEntry[any]
	// out is why it is left out since its last entry: failing (reported once), or refused (reported once).
	out outOf
}

var _ state.Dependent = (*RegistryContribution)(nil)

// NewRegistryContribution makes the contribution of contributor, built against version, to record.
func NewRegistryContribution(record *RegistryRecord, contributor Contributor, version int, content ContributionContent) *RegistryContribution {
	return &RegistryContribution{Record: record, Contributor: contributor, Version: version, content: content}
}

// Invalidate is its source changing (inside a batch): the registry's table is marked.
func (c *RegistryContribution) Invalidate() { c.Record.ContributionChanged() }

// SourceRetired is a retirement of its source's document: it changes nothing beyond the value.
func (c *RegistryContribution) SourceRetired(map[state.Dependent]struct{}) {}

// Follow starts following its source, if it has one.
func (c *RegistryContribution) Follow() {
	if dependents := c.followed(); dependents != nil {
		dependents.Add(c)
	}
}

// Unfollow stops following its source.
func (c *RegistryContribution) Unfollow() {
	if dependents := c.followed(); dependents != nil {
		dependents.Delete(c)
	}
}

// followed is the dependents of its source when it changes (a constant never does); nil when there is none.
func (c *RegistryContribution) followed() *state.DependentSet {
	switch c.content.Kind {
	case ContributionNative:
		if c.content.Native != nil {
			return c.content.Native.DependentSet()
		}
	case ContributionJSON:
		if c.content.Source != nil {
			return c.content.Source.DependentSet()
		}
	}
	return nil
}

// Entry is its entry in a table of the provider at version: the same object while its value is the same; nil when left
// out (a refused version, a failing source, a failing adapter).
func (c *RegistryContribution) Entry(version int, provider RegistryProvider[any, any]) *RegistryEntry[any] {
	var adapter func(old any) (any, error)
	if c.Version < version {
		adapter = provider.Accepts[c.Version].Contribution
	}
	if c.Version > version || (c.Version < version && adapter == nil) {
		if c.out != outRefused {
			c.refused()
		}
		return nil
	}
	value, err := c.value(adapter)
	if err != nil {
		if c.out != outFailed {
			c.failed(err)
		}
		return nil
	}
	c.out = outNone
	// `undefined` is a value: the entry, not its field, says whether there was one.
	if c.entry != nil && state.Identical(c.entry.Value, value) {
		return c.entry
	}
	c.entry = &RegistryEntry[any]{Plugin: c.Contributor.Plugin, Type: c.Contributor.Type, Version: c.Version, Value: value}
	return c.entry
}

// value is the contributed value now, adapted when adapter is not nil.
func (c *RegistryContribution) value(adapter func(old any) (any, error)) (any, error) {
	raw, err := c.raw()
	if err != nil || adapter == nil {
		return raw, err
	}
	return c.adapt(raw, adapter)
}

// raw is the contributed value now: a JSON source's value fails while it is failed or ended; a native source that
// failed (a registry whose `compose` threw) keeps a last good value for its readers, but is left out here as failed.
func (c *RegistryContribution) raw() (any, error) {
	switch c.content.Kind {
	case ContributionJSON:
		return try(func() (any, error) { return c.content.Source.Value() })
	case ContributionNative:
		return try(c.content.Native.CurrentAny)
	}
	return c.content.Value, nil
}

func (c *RegistryContribution) adapt(raw any, adapter func(old any) (any, error)) (any, error) {
	if adapted := c.adapted; adapted != nil && state.Identical(adapted.from, raw) && sameFunc(adapted.adapter, adapter) {
		return adapted.value, nil
	}
	value, err := c.Record.Adapt(func() (any, error) { return adapter(raw) })
	if err != nil {
		return nil, err
	}
	c.adapted = &adaptation{from: raw, adapter: adapter, value: value}
	return value, nil
}

func (c *RegistryContribution) refused() {
	c.out = outRefused
	c.Record.ReportContribution(RegistryError{
		Kind:     ErrorContributionRefused,
		Registry: c.Record.Name,
		Plugin:   c.Contributor.Plugin,
		Type:     c.Contributor.Type,
		Version:  c.Version,
	})
}

func (c *RegistryContribution) failed(err error) {
	c.out = outFailed
	c.Record.ReportContribution(RegistryError{
		Kind:     ErrorContributionFailed,
		Registry: c.Record.Name,
		Plugin:   c.Contributor.Plugin,
		Type:     c.Contributor.Type,
		Error:    state.HandedOut(err),
	})
}
