//go:build !pig_strip_node_extensions

package subprocess

import (
	"testing"
)

// Each member's host socket needs IO that continues while a synchronous host call blocks the main thread, but one IO thread serves every socket of the process. A Worker is a V8 isolate and a Node environment, about 10 MiB of RSS before it reads a frame.
func TestNodeCellMembersShareOneIOWorker(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		isolation string
		members   int
		workers   int
	}{
		{isolation: "", members: 5, workers: 1},
		{isolation: "isolated", members: 1, workers: 1},
	} {
		cost := nodeCellCostOf(t, tc.members, tc.isolation, false)
		if cost.Members != tc.members || !cost.Shared {
			t.Fatalf("isolation %q: %d members in the process sharing library identities = %v, want %d sharing them", tc.isolation, cost.Members, cost.Shared, tc.members)
		}
		if cost.Workers != tc.workers {
			t.Errorf("isolation %q: %d members run %d Worker threads, want %d", tc.isolation, tc.members, cost.Workers, tc.workers)
		}
	}
}

// Members two to five import the Pi SDK as the first does and evaluate no second copy of it: they add only their own module and runtime state. The Worker a member would add for its own IO is counted by TestNodeCellMembersShareOneIOWorker, not here.
//
// The probe measures what the process retains after a forced full collection: V8's heapUsed plus the external memory it accounts. A second copy of the SDK's modules lives in that heap. The process's RSS is not that measure and cannot hold a budget of a few MiB: it also holds whatever of the startup transients was still resident when it was read. In 19 runs (8 unloaded, 11 under a load average of 160 to 235) one member read 131.8 to 152.2 MiB of RSS, 6.4 to 27.5 MiB below its peak, and five members 149.7 to 160.8 MiB, so RSS gave 0.8 to 7.2 MiB per added member and failed the 6 MiB budget once. The retained heap and external memory gave 0.20 to 0.25 MiB per added member in every one of those runs and in 46 more (8 unloaded, 38 loaded); a member that retains 8,000 small objects reads 0.79 MiB and one that retains 60,000 reads 4.35 MiB.
func TestNodeCellAddedMembersStayWithinTheirOwnState(t *testing.T) {
	t.Parallel()
	one := nodeCellCostOf(t, 1, "", false)
	five := nodeCellCostOf(t, 5, "", false)
	if !five.Shared {
		t.Fatal("cell members evaluated separate copies of the Pi library")
	}
	const budget = 2 << 20
	added := ((five.Heap + five.External) - (one.Heap + one.External)) / 4
	t.Logf("each added Node cell member retains %.2f MiB of heap and external memory (1 member %.1f MiB, 5 members %.1f MiB; RSS %.1f and %.1f MiB, not asserted)", float64(added)/(1<<20), float64(one.Heap+one.External)/(1<<20), float64(five.Heap+five.External)/(1<<20), float64(one.RSS)/(1<<20), float64(five.RSS)/(1<<20))
	if added > budget {
		t.Errorf("each added Node cell member retains %.2f MiB of heap and external memory, want at most %d MiB", float64(added)/(1<<20), budget>>20)
	}
}

// Pi's extensions read one ModelRegistry (model-registry.ts). The host sends each catalog publication, about 1.2 MB of JSON, to every member's socket; an added member must not pay for its own IO Worker and its own eagerly decoded and normalized copies of three publications, while each keeps its own model objects. The base peaks at about 24 MiB more per member. TestNodeProviderSocketsShareARepeatedLargeFrame proves that the cell reads and decodes a repeated publication once; this test bounds what a member adds to the peak.
//
// The peak depends on when V8 collects the transient publication bytes and envelopes, which a loaded host delays: under a load average of about 250 this branch measured 2.1 to 8.9 MiB per added member, 3.4 to 11.2 MiB with frame sharing disabled, and the base 22.0 to 28.5 MiB. The budget sits between those peaks and the base's.
func TestNodeCellMembersShareRegistryPublications(t *testing.T) {
	t.Parallel()
	one := nodeCellCostOf(t, 1, "", true)
	five := nodeCellCostOf(t, 5, "", true)
	const budget = 16 << 20
	added := (five.Peak - one.Peak) / 4
	t.Logf("each added Node cell member raises the peak RSS by %.1f MiB (1 member %.1f MiB, 5 members %.1f MiB)", float64(added)/(1<<20), float64(one.Peak)/(1<<20), float64(five.Peak)/(1<<20))
	t.Logf("each added Node cell member retains %.2f MiB of heap (1 member %.2f MiB, 5 members %.2f MiB)", float64(five.Heap-one.Heap)/4/(1<<20), float64(one.Heap)/(1<<20), float64(five.Heap)/(1<<20))
	if added > budget {
		t.Errorf("each added Node cell member raises the peak RSS by %.1f MiB, want at most %d MiB", float64(added)/(1<<20), budget>>20)
	}
}
