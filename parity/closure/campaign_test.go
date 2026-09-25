package closure

import (
	"slices"
	"strings"
	"testing"
)

func campaignManifest() CampaignManifest {
	return CampaignManifest{UpstreamVersion: "0.84.0", TargetCommit: strings.Repeat("b", 40), Holder: "worker-1"}
}

func threeUnitGraph(t *testing.T) (*Graph, map[string]WorkUnit) {
	t.Helper()
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:a", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:b", "ai/anthropic.go")...)
	records = append(records, behaviorBundle(snapshot, "gamma", "wire:g", "ai/bedrock.go")...)
	return planUnits(t, records)
}

func activeLeaseFor(t *testing.T, graph *Graph, units map[string]WorkUnit, behaviorID string) Lease {
	t.Helper()
	unit := unitFor(t, units, behaviorID)
	lease, err := GrantLease(graph, nil, units, unit.ID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestPlanCampaignGrantsPartitionsAllOpenUnitsWhenUnbounded(t *testing.T) {
	graph, _ := threeUnitGraph(t)
	plan, err := PlanCampaignGrants(graph, campaignManifest(), nil)
	if err != nil {
		t.Fatalf("PlanCampaignGrants() = %v", err)
	}
	if len(plan.Grantable) != 3 || plan.BudgetHalted || len(plan.Parked)+len(plan.Blocked)+len(plan.Deferred)+len(plan.Held) != 0 {
		t.Fatalf("plan = %s, want 3 grantable and nothing else", plan)
	}
	// Deterministic order.
	got := []string{plan.Grantable[0].BehaviorID, plan.Grantable[1].BehaviorID, plan.Grantable[2].BehaviorID}
	if !slices.IsSorted(got) {
		t.Fatalf("grantable order not deterministic: %v", got)
	}
}

func TestPlanCampaignGrantsBudgetHaltsIntakeWithoutDroppingUnits(t *testing.T) {
	graph, _ := threeUnitGraph(t)
	manifest := campaignManifest()
	manifest.Budget = CampaignBudget{MaxGrants: 1}
	plan, err := PlanCampaignGrants(graph, manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Grantable) != 1 || len(plan.Deferred) != 2 || !plan.BudgetHalted {
		t.Fatalf("plan = %s, want 1 grantable, 2 deferred, halted", plan)
	}
	// No unit is lost: grantable + deferred accounts for the whole frontier.
	if len(plan.Grantable)+len(plan.Deferred) != 3 {
		t.Fatalf("budget dropped units: %s", plan)
	}
}

func TestPlanCampaignGrantsHeldUnitsAreNotRegranted(t *testing.T) {
	graph, units := threeUnitGraph(t)
	active := []Lease{activeLeaseFor(t, graph, units, "behavior:alpha")}
	plan, err := PlanCampaignGrants(graph, campaignManifest(), active)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Held) != 1 || len(plan.Grantable) != 2 {
		t.Fatalf("plan = %s, want 1 held and 2 grantable", plan)
	}
	for _, unit := range plan.Grantable {
		if unit.BehaviorID == "behavior:alpha" {
			t.Fatalf("held unit re-granted: %v", unit)
		}
	}
}

func TestPlanCampaignGrantsRespectsActiveLeaseHeadroom(t *testing.T) {
	graph, units := threeUnitGraph(t)
	active := []Lease{activeLeaseFor(t, graph, units, "behavior:alpha")}
	manifest := campaignManifest()
	manifest.Budget = CampaignBudget{MaxActiveLeases: 2}
	plan, err := PlanCampaignGrants(graph, manifest, active)
	if err != nil {
		t.Fatal(err)
	}
	// One lease already active plus a cap of two leaves headroom for one grant.
	if len(plan.Grantable) != 1 || len(plan.Deferred) != 1 || !plan.BudgetHalted {
		t.Fatalf("plan = %s, want 1 grantable, 1 deferred, halted", plan)
	}
}

func TestPlanCampaignGrantsBlocksUnitsThatOverlapAnActiveLease(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:a", "shared.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:b", "shared.go")...)
	graph, units := planUnits(t, records)
	active := []Lease{activeLeaseFor(t, graph, units, "behavior:alpha")}
	plan, err := PlanCampaignGrants(graph, campaignManifest(), active)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Held) != 1 || len(plan.Blocked) != 1 || len(plan.Grantable) != 0 {
		t.Fatalf("plan = %s, want 1 held, 1 blocked, 0 grantable", plan)
	}
	if plan.Blocked[0].BehaviorID != "behavior:beta" {
		t.Fatalf("blocked = %v, want behavior:beta", plan.Blocked[0])
	}
}

func TestPlanCampaignGrantsParksHumanGatedUnits(t *testing.T) {
	graph, _ := threeUnitGraph(t)
	// Inject an unresolved adversary finding against one behavior; the scheduler
	// parks its unit while the others proceed. Full finding validation is proven
	// in agent_proposals_test.go.
	graph.Records["hypothesis:agent:park"] = &Hypothesis{
		Kind: KindHypothesis, ID: "hypothesis:agent:park",
		HypothesisType: adversaryProposalType, SubjectID: "behavior:beta",
	}
	plan, err := PlanCampaignGrants(graph, campaignManifest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Parked) != 1 || plan.Parked[0].BehaviorID != "behavior:beta" || len(plan.Grantable) != 2 {
		t.Fatalf("plan = %s, want beta parked and 2 grantable", plan)
	}
}

func TestCampaignManifestValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*CampaignManifest)
		want string
	}{
		{"bad commit", func(m *CampaignManifest) { m.TargetCommit = "short" }, "targetCommit"},
		{"empty holder", func(m *CampaignManifest) { m.Holder = "" }, "holder is required"},
		{"empty version", func(m *CampaignManifest) { m.UpstreamVersion = "" }, "upstreamVersion"},
		{"negative budget", func(m *CampaignManifest) { m.Budget.MaxGrants = -1 }, "non-negative"},
	}
	graph, _ := threeUnitGraph(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := campaignManifest()
			tc.mut(&manifest)
			if _, err := PlanCampaignGrants(graph, manifest, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestPlanCampaignGrantsBlocksMutuallyOverlappingCandidatesInOneStep(t *testing.T) {
	// alpha and beta both write shared.go, so they contend with each other even
	// with no active lease; only one may be granted this step.
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:a", "shared.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:b", "shared.go")...)
	records = append(records, behaviorBundle(snapshot, "gamma", "wire:g", "ai/other.go")...)
	graph, _ := planUnits(t, records)
	plan, err := PlanCampaignGrants(graph, campaignManifest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// gamma is independent and grantable; exactly one of alpha/beta is grantable,
	// the other blocked. Nothing is lost.
	if len(plan.Grantable) != 2 || len(plan.Blocked) != 1 {
		t.Fatalf("plan = %s, want 2 grantable (gamma + one of alpha/beta), 1 blocked", plan)
	}
	// The grantable set must be mutually compatible.
	for i := range plan.Grantable {
		for j := i + 1; j < len(plan.Grantable); j++ {
			if plan.Grantable[i].overlaps(plan.Grantable[j]) {
				t.Fatalf("grantable set contends: %v and %v", plan.Grantable[i], plan.Grantable[j])
			}
		}
	}
}
