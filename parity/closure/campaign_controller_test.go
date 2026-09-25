package closure

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// threeUnitStore rebuilds a real store with three disjoint open work units and
// returns the store path.
func threeUnitStore(t *testing.T) string {
	t.Helper()
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:a", "ai/openai.go")...)
	records = append(records, behaviorBundle(snapshot, "beta", "wire:b", "ai/anthropic.go")...)
	records = append(records, behaviorBundle(snapshot, "gamma", "wire:g", "ai/bedrock.go")...)
	graph, err := Build(records)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "graph.db")
	if err := RebuildStore(context.Background(), path, graph); err != nil {
		t.Fatal(err)
	}
	return path
}

func auditLines(t *testing.T, storePath string) []CampaignAuditEntry {
	t.Helper()
	file, err := os.Open(campaignAuditPath(storePath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var entries []CampaignAuditEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry CampaignAuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestCampaignStepGrantsFrontierAndResumesWithoutRegrant(t *testing.T) {
	path := threeUnitStore(t)
	ctx := context.Background()
	manifest := campaignManifest()
	manifest.Budget = CampaignBudget{MaxGrants: 2}

	first, err := CampaignStep(ctx, path, manifest)
	if err != nil {
		t.Fatalf("first step: %v", err)
	}
	if len(first.GrantedLeases) != 2 || len(first.Plan.Deferred) != 1 || !first.Plan.BudgetHalted {
		t.Fatalf("first step = %s, granted %d, want 2 granted / 1 deferred / halted", first.Plan, len(first.GrantedLeases))
	}

	// Resuming with the two leases now active grants only the remaining unit and
	// never re-grants held work.
	second, err := CampaignStep(ctx, path, manifest)
	if err != nil {
		t.Fatalf("second step: %v", err)
	}
	if len(second.GrantedLeases) != 1 || len(second.Plan.Held) != 2 {
		t.Fatalf("second step = %s, granted %d, want 1 granted / 2 held", second.Plan, len(second.GrantedLeases))
	}
	for _, granted := range second.GrantedLeases {
		for _, prior := range first.GrantedLeases {
			if granted.ID == prior.ID {
				t.Fatalf("held lease re-granted: %s", granted.ID)
			}
		}
	}

	// A third step grants nothing: all three units are held.
	third, err := CampaignStep(ctx, path, manifest)
	if err != nil {
		t.Fatalf("third step: %v", err)
	}
	if len(third.GrantedLeases) != 0 || len(third.Plan.Held) != 3 {
		t.Fatalf("third step = %s, want 0 granted / 3 held", third.Plan)
	}

	// The audit trail accrued one line per step, in order.
	entries := auditLines(t, path)
	if len(entries) != 3 {
		t.Fatalf("audit lines = %d, want 3", len(entries))
	}
	if entries[0].Grantable != 2 || entries[1].Grantable != 1 || entries[2].Grantable != 0 {
		t.Fatalf("audit grantable sequence = %d,%d,%d, want 2,1,0", entries[0].Grantable, entries[1].Grantable, entries[2].Grantable)
	}
	if entries[2].Held != 3 || entries[0].TargetCommit != manifest.TargetCommit {
		t.Fatalf("audit content mismatch: %#v", entries[2])
	}
}

func TestCampaignStepRejectsInvalidManifest(t *testing.T) {
	path := threeUnitStore(t)
	manifest := campaignManifest()
	manifest.Holder = ""
	if _, err := CampaignStep(context.Background(), path, manifest); err == nil {
		t.Fatal("expected invalid manifest rejection")
	}
	// A rejected step writes no audit line.
	if _, err := os.Stat(campaignAuditPath(path)); !os.IsNotExist(err) {
		t.Fatalf("audit file created for rejected step: %v", err)
	}
}
