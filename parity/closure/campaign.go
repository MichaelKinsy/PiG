package closure

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CampaignBudget bounds one scheduling step. A zero field means unbounded. The
// budget caps how much work the controller admits; it never removes a ready unit
// from the frontier, so exhausting the budget halts intake without shrinking the
// campaign.
type CampaignBudget struct {
	MaxActiveLeases int
	MaxGrants       int
}

func (b CampaignBudget) validate() error {
	if b.MaxActiveLeases < 0 || b.MaxGrants < 0 {
		return errors.New("campaign budget: limits must be non-negative")
	}
	return nil
}

// CampaignManifest is the replayable definition of a headless campaign. It pins
// the upstream authority and target commit so a resumed run drives the same
// frontier, and carries the budget the controller enforces each step.
type CampaignManifest struct {
	UpstreamVersion string         `json:"upstreamVersion"`
	TargetCommit    string         `json:"targetCommit"`
	Holder          string         `json:"holder"`
	Budget          CampaignBudget `json:"budget"`
}

func (m CampaignManifest) validate() error {
	if strings.TrimSpace(m.UpstreamVersion) == "" {
		return errors.New("campaign manifest: upstreamVersion is required")
	}
	if !validCommit(m.TargetCommit) {
		return errors.New("campaign manifest: targetCommit must be a 40-character lowercase commit")
	}
	if strings.TrimSpace(m.Holder) == "" {
		return errors.New("campaign manifest: holder is required")
	}
	return m.Budget.validate()
}

// GrantPlan is the deterministic outcome of one scheduling step. Every open work
// unit lands in exactly one bucket, so the controller can grant Grantable now,
// leave Parked for a human, and revisit Deferred and Blocked on a later step
// without losing, duplicating, or silently skipping any unit.
type GrantPlan struct {
	Grantable    []WorkUnit
	Parked       []WorkUnit
	Blocked      []WorkUnit
	Deferred     []WorkUnit
	Held         []string
	BudgetHalted bool
}

// PlanCampaignGrants selects the next units to lease from the current graph and
// active lease set. It is pure and resumable: a unit already covered by an active
// lease is Held and never re-granted, a unit whose behavior awaits a human
// decision is Parked while others proceed, a unit that would overlap an active
// lease is Blocked, and a ready unit beyond the budget is Deferred with
// BudgetHalted set. Re-running it after the granted leases become active yields
// the same partition minus the newly held units, so a killed campaign resumes
// without drift.
func PlanCampaignGrants(graph *Graph, manifest CampaignManifest, active []Lease) (GrantPlan, error) {
	if graph == nil {
		return GrantPlan{}, errors.New("plan campaign: nil graph")
	}
	if err := manifest.validate(); err != nil {
		return GrantPlan{}, err
	}
	units, err := PlanWorkUnits(graph)
	if err != nil {
		return GrantPlan{}, err
	}
	unitByID := indexWorkUnits(units)
	heldUnits := make(map[string]struct{}, len(active))
	for _, lease := range active {
		heldUnits[lease.WorkUnitID] = struct{}{}
	}

	plan := GrantPlan{Grantable: []WorkUnit{}, Parked: []WorkUnit{}, Blocked: []WorkUnit{}, Deferred: []WorkUnit{}, Held: []string{}}
	budget := grantBudget(manifest.Budget, len(active))
	for _, unit := range units {
		if _, held := heldUnits[unit.ID]; held {
			plan.Held = append(plan.Held, unit.ID)
			continue
		}
		if graph.humanGated(unit.BehaviorID) {
			plan.Parked = append(plan.Parked, unit)
			continue
		}
		if _, conflict := unit.conflictWithActive(active, unitByID); conflict {
			plan.Blocked = append(plan.Blocked, unit)
			continue
		}
		// A grantable set must be mutually compatible: two candidates that share
		// a write path or boundary cannot both hold a lease this step, so the
		// later one is Blocked and revisited once the first integrates.
		if unit.overlapsAny(plan.Grantable) {
			plan.Blocked = append(plan.Blocked, unit)
			continue
		}
		if budget <= 0 {
			plan.Deferred = append(plan.Deferred, unit)
			plan.BudgetHalted = true
			continue
		}
		plan.Grantable = append(plan.Grantable, unit)
		budget--
	}
	slices.Sort(plan.Held)
	return plan, nil
}

// grantBudget is the number of grants this step may admit: the smaller of the
// remaining active-lease headroom and the per-step grant cap. A zero limit is
// unbounded and is represented as the total unit ceiling so it never binds.
func grantBudget(budget CampaignBudget, activeCount int) int {
	const unbounded = int(^uint(0) >> 1)
	headroom := unbounded
	if budget.MaxActiveLeases > 0 {
		headroom = max(budget.MaxActiveLeases-activeCount, 0)
	}
	perStep := unbounded
	if budget.MaxGrants > 0 {
		perStep = budget.MaxGrants
	}
	return min(headroom, perStep)
}

// humanGated reports whether a behavior awaits human resolution: an unresolved
// adversary finding blocks promotion, so its units park rather than proceed.
func (g *Graph) humanGated(behaviorID string) bool {
	return len(g.unresolvedAdversaryFindings(behaviorID)) > 0
}

// conflictWithActive reports the first active lease whose leased unit shares a
// write-footprint path or a semantic boundary with this unit, mirroring the
// admission gate GrantLease enforces.
func (unit WorkUnit) conflictWithActive(active []Lease, units map[string]WorkUnit) (Lease, bool) {
	for _, lease := range active {
		other, ok := units[lease.WorkUnitID]
		if !ok {
			continue
		}
		if unit.overlaps(other) {
			return lease, true
		}
	}
	return Lease{}, false
}

// overlaps reports whether two units share a write-footprint path or a semantic
// boundary, the same contention GrantLease refuses to grant concurrently.
func (unit WorkUnit) overlaps(other WorkUnit) bool {
	if _, ok := firstOverlap(unit.writeFootprint(), other.writeFootprint()); ok {
		return true
	}
	if _, ok := firstOverlap(unit.SemanticBoundaryIDs, other.SemanticBoundaryIDs); ok {
		return true
	}
	return false
}

// overlapsAny reports whether this unit contends with any unit in the set.
func (unit WorkUnit) overlapsAny(others []WorkUnit) bool {
	return slices.ContainsFunc(others, unit.overlaps)
}

func (p GrantPlan) String() string {
	return fmt.Sprintf("grantable=%d parked=%d blocked=%d deferred=%d held=%d budgetHalted=%t",
		len(p.Grantable), len(p.Parked), len(p.Blocked), len(p.Deferred), len(p.Held), p.BudgetHalted)
}
