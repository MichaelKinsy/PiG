package closure

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CampaignAuditEntry is one appended line of the campaign audit trail. It records
// the manifest identity, the plan partition counts, and the leases granted this
// step. It carries no wall clock: the append order is the temporal record and
// keeps the entry deterministic and testable.
type CampaignAuditEntry struct {
	UpstreamVersion string   `json:"upstreamVersion"`
	TargetCommit    string   `json:"targetCommit"`
	Holder          string   `json:"holder"`
	Grantable       int      `json:"grantable"`
	Parked          int      `json:"parked"`
	Blocked         int      `json:"blocked"`
	Deferred        int      `json:"deferred"`
	Held            int      `json:"held"`
	BudgetHalted    bool     `json:"budgetHalted"`
	GrantedLeaseIDs []string `json:"grantedLeaseIds"`
}

// CampaignStepResult reports one controller step: the derived plan, the leases it
// granted, and the audit entry it appended.
type CampaignStepResult struct {
	Plan          GrantPlan
	GrantedLeases []Lease
	Audit         CampaignAuditEntry
}

// CampaignStep drives one headless controller step against the store: it loads
// the current graph, plans the grantable frontier against the persisted active
// leases and the manifest budget, and grants each grantable unit through the
// serialized store mutation. The scheduler guarantees the grantable set is
// mutually compatible, so the sequential grants cannot contend. The step is
// resumable: it reads active leases from the store, so a killed campaign resumes
// without re-granting held work. It appends one audit line per step next to the
// store.
func CampaignStep(ctx context.Context, path string, manifest CampaignManifest) (CampaignStepResult, error) {
	graph, err := loadStoreGraph(ctx, path)
	if err != nil {
		return CampaignStepResult{}, err
	}
	plan, err := PlanCampaignGrants(graph, manifest, ActiveLeases(graph))
	if err != nil {
		return CampaignStepResult{}, err
	}

	granted := make([]Lease, 0, len(plan.Grantable))
	grantedIDs := make([]string, 0, len(plan.Grantable))
	for _, unit := range plan.Grantable {
		encoded, err := GrantLeaseInStore(ctx, path, unit.ID, manifest.Holder)
		if err != nil {
			return CampaignStepResult{}, fmt.Errorf("campaign step: grant %s: %w", unit.ID, err)
		}
		var lease Lease
		if err := json.Unmarshal(encoded, &lease); err != nil {
			return CampaignStepResult{}, fmt.Errorf("campaign step: decode granted lease: %w", err)
		}
		granted = append(granted, lease)
		grantedIDs = append(grantedIDs, lease.ID)
	}

	entry := CampaignAuditEntry{
		UpstreamVersion: manifest.UpstreamVersion, TargetCommit: manifest.TargetCommit, Holder: manifest.Holder,
		Grantable: len(plan.Grantable), Parked: len(plan.Parked), Blocked: len(plan.Blocked),
		Deferred: len(plan.Deferred), Held: len(plan.Held), BudgetHalted: plan.BudgetHalted,
		GrantedLeaseIDs: grantedIDs,
	}
	if err := appendCampaignAudit(campaignAuditPath(path), entry); err != nil {
		return CampaignStepResult{}, err
	}
	return CampaignStepResult{Plan: plan, GrantedLeases: granted, Audit: entry}, nil
}

// loadStoreGraph opens the store, reads its records, and builds the graph.
func loadStoreGraph(ctx context.Context, path string) (*Graph, error) {
	database, err := openStore(path)
	if err != nil {
		return nil, err
	}
	records, err := readRecords(ctx, database)
	if closeErr := database.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	return Build(records)
}

// campaignAuditPath is the append-only audit ledger beside the store file.
func campaignAuditPath(storePath string) string {
	return filepath.Join(filepath.Dir(storePath), "campaign-audit.jsonl")
}

func appendCampaignAudit(auditPath string, entry CampaignAuditEntry) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	writer := bufio.NewWriter(file)
	if _, err := writer.Write(append(line, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
