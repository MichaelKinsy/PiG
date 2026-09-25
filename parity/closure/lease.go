package closure

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/parity/correspondence"
)

// WorkUnit is a graph-derived parallel ownership unit rooted at one open
// behavior. It carries the exact repository paths a Translator may write and the
// upstream semantic boundaries the behavior depends on. Two units conflict when
// their write footprints overlap textually or their semantic boundaries
// intersect, even across disjoint files. Work units are derived, never authored.
type WorkUnit struct {
	ID                  string
	SnapshotID          string
	BehaviorID          string
	ObligationIDs       []string
	WritePaths          []string
	TestPaths           []string
	FixturePaths        []string
	SemanticBoundaryIDs []string
}

// LeaseState is the persisted lifecycle of a lease. A lease is active while its
// holder works and integrated once its patch has landed on canonical; only
// active leases participate in the overlap-admission set.
type LeaseState string

const (
	LeaseActive     LeaseState = "active"
	LeaseIntegrated LeaseState = "integrated"
)

// Lease grants one work unit to one holder against an exact base fingerprint.
// Integration rejects a lease whose base no longer matches the current graph, so
// a worker cannot integrate a patch built on stale support. A lease is a durable
// receipt of a derived grant: every field is derived at grant time, never
// authored.
type Lease struct {
	Kind            Kind       `json:"kind"`
	ID              string     `json:"id"`
	WorkUnitID      string     `json:"workUnitId"`
	SnapshotID      string     `json:"snapshotId"`
	Holder          string     `json:"holder"`
	BaseFingerprint string     `json:"baseFingerprint"`
	State           LeaseState `json:"state"`
}

func (r *Lease) RecordKind() Kind { return r.Kind }
func (r *Lease) RecordID() string { return r.ID }

// ActiveLeases returns the persisted active leases in deterministic ID order.
// Integrated leases are excluded because they no longer contend for overlap.
func ActiveLeases(graph *Graph) []Lease {
	active := make([]Lease, 0)
	for _, id := range graph.recordIDs(KindLease) {
		lease := graph.Records[id].(*Lease)
		if lease.State == LeaseActive {
			active = append(active, *lease)
		}
	}
	slices.SortFunc(active, func(left, right Lease) int { return strings.Compare(left.ID, right.ID) })
	return active
}

// PlanWorkUnits derives one work unit per open behavior root from the closure
// graph. The result is deterministic: units are sorted by ID and every path and
// boundary list is sorted and de-duplicated.
func PlanWorkUnits(graph *Graph) ([]WorkUnit, error) {
	if graph == nil {
		return nil, errors.New("plan work units: nil graph")
	}
	openByBehavior := make(map[string][]string)
	for _, id := range sortedKeys(graph.Verdicts) {
		verdict := graph.Verdicts[id]
		if verdict.State != VerdictOpen {
			continue
		}
		obligation, ok := graph.Records[verdict.ObligationID].(*Obligation)
		if !ok {
			return nil, fmt.Errorf("plan work units: verdict %s has no obligation", id)
		}
		openByBehavior[obligation.BehaviorID] = append(openByBehavior[obligation.BehaviorID], obligation.ID)
	}
	units := make([]WorkUnit, 0, len(openByBehavior))
	for _, behaviorID := range sortedStringKeys(openByBehavior) {
		behavior, ok := graph.Records[behaviorID].(*Behavior)
		if !ok {
			return nil, fmt.Errorf("plan work units: obligation references missing behavior %s", behaviorID)
		}
		unit, err := graph.deriveWorkUnit(behavior, openByBehavior[behaviorID])
		if err != nil {
			return nil, err
		}
		units = append(units, unit)
	}
	slices.SortFunc(units, func(left, right WorkUnit) int { return strings.Compare(left.ID, right.ID) })
	return units, nil
}

func (g *Graph) deriveWorkUnit(behavior *Behavior, obligationIDs []string) (WorkUnit, error) {
	snapshotID, err := g.behaviorSnapshotID(behavior)
	if err != nil {
		return WorkUnit{}, err
	}
	writes := newStringSet()
	tests := newStringSet()
	fixtures := newStringSet()
	boundaries := newStringSet()

	for _, pinID := range behavior.OriginPinIDs {
		pin, ok := g.Records[pinID].(*Pin)
		if !ok {
			return WorkUnit{}, fmt.Errorf("plan work units: behavior %s references missing pin %s", behavior.ID, pinID)
		}
		if pin.SemanticID != "" {
			boundaries.add(pin.SemanticID)
		}
	}
	for _, mappingID := range g.recordIDs(KindMapping) {
		mapping := g.Records[mappingID].(*Mapping)
		if mapping.BehaviorID != behavior.ID {
			continue
		}
		for _, targetID := range mapping.TargetIDs {
			target, ok := g.Records[targetID].(*Target)
			if !ok {
				return WorkUnit{}, fmt.Errorf("plan work units: mapping %s references missing target %s", mappingID, targetID)
			}
			if target.Language != "go" {
				continue
			}
			if err := g.collectPinPaths(target.PinIDs, writes); err != nil {
				return WorkUnit{}, err
			}
		}
	}
	for _, reachabilityID := range g.recordIDs(KindReachability) {
		reachability := g.Records[reachabilityID].(*Reachability)
		if reachability.BehaviorID != behavior.ID || reachability.Class != "prod-reachable" {
			continue
		}
		if err := g.collectPinPaths(reachability.RootPinIDs, writes); err != nil {
			return WorkUnit{}, err
		}
	}
	for _, assertionID := range g.recordIDs(KindAssertion) {
		assertion := g.Records[assertionID].(*Assertion)
		if assertion.BehaviorID != behavior.ID {
			continue
		}
		test, ok := g.Records[assertion.TestID].(*Test)
		if !ok {
			return WorkUnit{}, fmt.Errorf("plan work units: assertion %s references missing test %s", assertionID, assertion.TestID)
		}
		if err := g.collectPinPaths([]string{test.PinID}, tests); err != nil {
			return WorkUnit{}, err
		}
		if err := g.collectPinPaths(test.FixturePinIDs, fixtures); err != nil {
			return WorkUnit{}, err
		}
	}

	return WorkUnit{
		ID:                  "work-unit:" + strings.TrimPrefix(HashBytes([]byte(behavior.ID)), "sha256:"),
		SnapshotID:          snapshotID,
		BehaviorID:          behavior.ID,
		ObligationIDs:       sortedUniqueStrings(obligationIDs),
		WritePaths:          writes.sorted(),
		TestPaths:           tests.sorted(),
		FixturePaths:        fixtures.sorted(),
		SemanticBoundaryIDs: boundaries.sorted(),
	}, nil
}

func (g *Graph) behaviorSnapshotID(behavior *Behavior) (string, error) {
	for _, pinID := range behavior.OriginPinIDs {
		if pin, ok := g.Records[pinID].(*Pin); ok {
			return pin.SnapshotID, nil
		}
	}
	return "", fmt.Errorf("plan work units: behavior %s has no origin pin", behavior.ID)
}

func (g *Graph) collectPinPaths(pinIDs []string, into *stringSet) error {
	for _, pinID := range pinIDs {
		pin, ok := g.Records[pinID].(*Pin)
		if !ok {
			return fmt.Errorf("plan work units: missing pin %s", pinID)
		}
		if pin.Path != "" {
			into.add(pin.Path)
		}
	}
	return nil
}

// WorkUnitBaseFingerprint hashes the unit's snapshot and the current support of
// its open obligations. It changes whenever any support the unit depends on
// changes, so a lease granted against one fingerprint is detectably stale after
// integration of overlapping or prerequisite work.
func WorkUnitBaseFingerprint(graph *Graph, unit WorkUnit) (string, error) {
	if graph == nil {
		return "", errors.New("base fingerprint: nil graph")
	}
	material := make([]string, 0, len(unit.ObligationIDs)+1)
	material = append(material, unit.SnapshotID)
	for _, obligationID := range unit.ObligationIDs {
		verdict, ok := graph.Verdicts[obligationID]
		if !ok {
			return "", fmt.Errorf("base fingerprint: unit %s references missing verdict %s", unit.ID, obligationID)
		}
		support := slices.Clone(verdict.SupportHashes)
		slices.Sort(support)
		material = append(material, obligationID+"="+string(verdict.State)+"|"+strings.Join(support, ","))
	}
	return HashBytes([]byte(strings.Join(material, "\x00"))), nil
}

// GrantLease atomically admits a new lease against the active set. It fails
// closed on an unknown unit, a stale base fingerprint, or any overlap with an
// active lease: textual write/test/fixture path intersection, or semantic
// boundary intersection across otherwise disjoint files.
func GrantLease(graph *Graph, active []Lease, units map[string]WorkUnit, workUnitID, holder string) (Lease, error) {
	if strings.TrimSpace(workUnitID) == "" || strings.TrimSpace(holder) == "" {
		return Lease{}, errors.New("grant lease: work unit and holder are required")
	}
	unit, ok := units[workUnitID]
	if !ok {
		return Lease{}, fmt.Errorf("grant lease: unknown work unit %s", workUnitID)
	}
	base, err := WorkUnitBaseFingerprint(graph, unit)
	if err != nil {
		return Lease{}, err
	}
	held := make(map[string]struct{}, len(active))
	for _, lease := range active {
		if lease.WorkUnitID == workUnitID {
			return Lease{}, fmt.Errorf("grant lease: work unit %s already leased", workUnitID)
		}
		other, ok := units[lease.WorkUnitID]
		if !ok {
			return Lease{}, fmt.Errorf("grant lease: active lease %s references unknown work unit %s", lease.ID, lease.WorkUnitID)
		}
		if path, overlap := firstOverlap(unit.writeFootprint(), other.writeFootprint()); overlap {
			return Lease{}, fmt.Errorf("grant lease: work unit %s conflicts with active lease %s on path %s", workUnitID, lease.ID, path)
		}
		if boundary, overlap := firstOverlap(unit.SemanticBoundaryIDs, other.SemanticBoundaryIDs); overlap {
			return Lease{}, fmt.Errorf("grant lease: work unit %s conflicts with active lease %s on semantic boundary %s", workUnitID, lease.ID, boundary)
		}
		held[lease.ID] = struct{}{}
	}
	id := "lease:" + strings.TrimPrefix(HashBytes([]byte(workUnitID+"\x00"+holder+"\x00"+base)), "sha256:")
	if _, duplicate := held[id]; duplicate {
		return Lease{}, fmt.Errorf("grant lease: duplicate lease %s", id)
	}
	return Lease{Kind: KindLease, ID: id, WorkUnitID: workUnitID, SnapshotID: unit.SnapshotID, Holder: holder, BaseFingerprint: base, State: LeaseActive}, nil
}

func (unit WorkUnit) writeFootprint() []string {
	footprint := newStringSet()
	for _, path := range unit.WritePaths {
		footprint.add(path)
	}
	for _, path := range unit.TestPaths {
		footprint.add(path)
	}
	for _, path := range unit.FixturePaths {
		footprint.add(path)
	}
	return footprint.sorted()
}

// ConfinePatchToLease rejects a Translator patch that writes outside the exact
// paths its leased work unit owns. Production edits must land in the unit write
// set and test edits in its test or fixture set, so a worker holding a lease can
// never touch another unit's files even when those files are valid targets
// elsewhere.
func ConfinePatchToLease(lease Lease, unit WorkUnit, bundle *correspondence.TranslatorBundle) error {
	if bundle == nil {
		return errors.New("confine patch: nil bundle")
	}
	if lease.WorkUnitID != unit.ID {
		return fmt.Errorf("confine patch: lease %s holds work unit %s, not %s", lease.ID, lease.WorkUnitID, unit.ID)
	}
	writable := make(map[string]struct{}, len(unit.WritePaths))
	for _, path := range unit.WritePaths {
		writable[path] = struct{}{}
	}
	testable := make(map[string]struct{}, len(unit.TestPaths)+len(unit.FixturePaths))
	for _, path := range unit.TestPaths {
		testable[path] = struct{}{}
	}
	for _, path := range unit.FixturePaths {
		testable[path] = struct{}{}
	}
	for _, translation := range bundle.Translations {
		for _, edit := range translation.ProductionEdits {
			if _, ok := writable[edit.Path]; !ok {
				return fmt.Errorf("confine patch: production edit %s is outside work unit %s write set", edit.Path, unit.ID)
			}
		}
		for _, edit := range translation.TestEdits {
			if _, ok := testable[edit.Path]; !ok {
				return fmt.Errorf("confine patch: test edit %s is outside work unit %s test set", edit.Path, unit.ID)
			}
		}
	}
	return nil
}

// DeltaBrief tells an active lease holder that support their work unit depends on
// moved during serial integration of another lease, so their base is stale and
// their patch must be rebased and re-verified before it can integrate.
type DeltaBrief struct {
	LeaseID        string
	WorkUnitID     string
	OldFingerprint string
	NewFingerprint string
}

// IntegrateLease admits a lease into canonical integration only if its recorded
// base still matches the current graph. Canonical integration is serial, so a
// lease granted before an earlier integration advanced shared support is
// detected as stale here and must rebase rather than integrate on a stale base.
func IntegrateLease(current *Graph, lease Lease, units map[string]WorkUnit) error {
	unit, ok := units[lease.WorkUnitID]
	if !ok {
		return fmt.Errorf("integrate lease: unknown work unit %s", lease.WorkUnitID)
	}
	base, err := WorkUnitBaseFingerprint(current, unit)
	if err != nil {
		return err
	}
	if lease.BaseFingerprint != base {
		return fmt.Errorf("integrate lease: lease %s base is stale (recorded %s, current %s)", lease.ID, lease.BaseFingerprint, base)
	}
	return nil
}

// DeltaBriefs returns, in deterministic lease order, a brief for every active
// lease whose work-unit base fingerprint changed between the pre- and
// post-integration graphs. It is how serial integration recomputes support and
// notifies exactly the affected workers, and no others.
func DeltaBriefs(before, after *Graph, active []Lease, units map[string]WorkUnit) ([]DeltaBrief, error) {
	briefs := make([]DeltaBrief, 0)
	ordered := slices.Clone(active)
	slices.SortFunc(ordered, func(left, right Lease) int { return strings.Compare(left.ID, right.ID) })
	for _, lease := range ordered {
		unit, ok := units[lease.WorkUnitID]
		if !ok {
			return nil, fmt.Errorf("delta briefs: unknown work unit %s", lease.WorkUnitID)
		}
		old, err := WorkUnitBaseFingerprint(before, unit)
		if err != nil {
			return nil, err
		}
		current, err := WorkUnitBaseFingerprint(after, unit)
		if err != nil {
			return nil, err
		}
		if old != current {
			briefs = append(briefs, DeltaBrief{LeaseID: lease.ID, WorkUnitID: lease.WorkUnitID, OldFingerprint: old, NewFingerprint: current})
		}
	}
	return briefs, nil
}

func firstOverlap(left, right []string) (string, bool) {
	set := make(map[string]struct{}, len(left))
	for _, value := range left {
		set[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := set[value]; ok {
			return value, true
		}
	}
	return "", false
}

type stringSet struct {
	values map[string]struct{}
}

func newStringSet() *stringSet { return &stringSet{values: make(map[string]struct{})} }
func (s *stringSet) add(value string) {
	s.values[value] = struct{}{}
}
func (s *stringSet) sorted() []string {
	result := make([]string, 0, len(s.values))
	for value := range s.values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func sortedUniqueStrings(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func sortedStringKeys[V any](m map[string]V) []string { return sortedKeys(m) }
