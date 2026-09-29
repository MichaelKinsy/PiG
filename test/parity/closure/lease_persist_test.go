package closure

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func leaseSnapshotRecord() *Snapshot {
	h := HashBytes([]byte("lease-persist"))
	return &Snapshot{Kind: KindSnapshot, ID: "snapshot:lease", UpstreamCommit: strings.Repeat("b", 40), TargetCommit: strings.Repeat("a", 40), ToolchainHash: h, EnvironmentHash: h}
}

func validLeaseRecord(snapshotID string, state LeaseState) *Lease {
	return &Lease{Kind: KindLease, ID: "lease:" + strings.TrimPrefix(HashBytes([]byte(string(state)+snapshotID)), "sha256:"), WorkUnitID: "work-unit:x", SnapshotID: snapshotID, Holder: "worker-1", BaseFingerprint: HashBytes([]byte("base")), State: state}
}

func TestLeaseRecordPersistsAndRoundTrips(t *testing.T) {
	snapshot := leaseSnapshotRecord()
	lease := validLeaseRecord(snapshot.ID, LeaseActive)
	graph, err := Build([]Record{snapshot, lease})
	if err != nil {
		t.Fatalf("Build([lease]) = %v", err)
	}
	root := t.TempDir()
	if err := RebuildStore(context.Background(), filepath.Join(root, "closure.db"), graph); err != nil {
		t.Fatal(err)
	}
	database, err := openStore(filepath.Join(root, "closure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	records, err := readRecords(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range records {
		if got, ok := record.(*Lease); ok {
			found = true
			if got.ID != lease.ID || got.WorkUnitID != lease.WorkUnitID || got.Holder != lease.Holder || got.BaseFingerprint != lease.BaseFingerprint || got.State != LeaseActive {
				t.Fatalf("round-tripped lease = %#v", got)
			}
		}
	}
	if !found {
		t.Fatal("lease record not persisted")
	}
}

func TestBuildRejectsInvalidLeaseRecords(t *testing.T) {
	snapshot := leaseSnapshotRecord()
	cases := []struct {
		name  string
		lease *Lease
		want  string
	}{
		{"dangling snapshot", func() *Lease {
			l := validLeaseRecord(snapshot.ID, LeaseActive)
			l.SnapshotID = "snapshot:missing"
			return l
		}(), "snapshot:missing"},
		{"empty holder", func() *Lease { l := validLeaseRecord(snapshot.ID, LeaseActive); l.Holder = ""; return l }(), "workUnitId and holder"},
		{"empty work unit", func() *Lease { l := validLeaseRecord(snapshot.ID, LeaseActive); l.WorkUnitID = ""; return l }(), "workUnitId and holder"},
		{"invalid state", func() *Lease { l := validLeaseRecord(snapshot.ID, "released"); return l }(), "invalid lease state"},
		{"bad base hash", func() *Lease {
			l := validLeaseRecord(snapshot.ID, LeaseActive)
			l.BaseFingerprint = "notahash"
			return l
		}(), "baseFingerprint"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build([]Record{snapshot, tc.lease})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestActiveLeasesExcludesIntegrated(t *testing.T) {
	snapshot := leaseSnapshotRecord()
	activeLease := validLeaseRecord(snapshot.ID, LeaseActive)
	integratedLease := validLeaseRecord(snapshot.ID, LeaseIntegrated)
	graph, err := Build([]Record{snapshot, activeLease, integratedLease})
	if err != nil {
		t.Fatal(err)
	}
	active := ActiveLeases(graph)
	if len(active) != 1 || active[0].ID != activeLease.ID {
		t.Fatalf("ActiveLeases() = %#v, want only %s", active, activeLease.ID)
	}
}

func TestGrantLeaseProducesValidPersistableRecord(t *testing.T) {
	snapshot := newLeaseSnapshot()
	records := []Record{snapshot}
	records = append(records, behaviorBundle(snapshot, "alpha", "wire:a", "ai/openai.go")...)
	graph, units := planUnits(t, records)
	unit := unitFor(t, units, "behavior:alpha")
	lease, err := GrantLease(graph, nil, units, unit.ID, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if lease.Kind != KindLease || lease.State != LeaseActive {
		t.Fatalf("granted lease is not a persistable record: %#v", lease)
	}
	// The derived lease validates as a graph record against its snapshot.
	if _, err := Build([]Record{snapshot, &lease}); err != nil {
		t.Fatalf("granted lease is not a valid record: %v", err)
	}
}

func TestLandingEvidenceRemovesWorkUnitFromFrontier(t *testing.T) {
	proved := provedFixture(t)
	open := make([]Record, 0, len(proved))
	for _, record := range proved {
		switch record.(type) {
		case *EvidenceRun, *ExecutionWitness, *EvidenceAttestation:
			continue
		}
		open = append(open, record)
	}
	openGraph, err := Build(open)
	if err != nil {
		t.Fatalf("Build(open) = %v", err)
	}
	if state := openGraph.Verdicts["obligation:result"].State; state != VerdictOpen {
		t.Fatalf("open verdict = %s, want open", state)
	}
	openUnits, err := PlanWorkUnits(openGraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(openUnits) != 1 || openUnits[0].BehaviorID != "behavior:run" {
		t.Fatalf("open frontier = %#v, want one unit for behavior:run", openUnits)
	}
	if !slices.Contains(openUnits[0].WritePaths, "example.go") {
		t.Fatalf("open unit write paths = %v, want example.go", openUnits[0].WritePaths)
	}

	provedGraph, err := Build(proved)
	if err != nil {
		t.Fatalf("Build(proved) = %v", err)
	}
	if state := provedGraph.Verdicts["obligation:result"].State; state != VerdictProven {
		t.Fatalf("landed verdict = %s, want proven", state)
	}
	provedUnits, err := PlanWorkUnits(provedGraph)
	if err != nil {
		t.Fatal(err)
	}
	if len(provedUnits) != 0 {
		t.Fatalf("landing admissible evidence left %d work units on the frontier, want 0", len(provedUnits))
	}
}
