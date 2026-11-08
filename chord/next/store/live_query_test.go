package store

import (
	"context"
	"testing"
)

// Ports packages/chord/test/store/live-query.test.ts, "a refresh applied again" (Pi main 8889e623d): a refresh applied
// again adopts only its last runs. The other cases of that file run on a host (chord/next/host/host_live_query_test.go).

// stubRunner stands for a live query: the refresh only calls its Rerun and Adopt, and hands the step through.
type stubRunner struct {
	runs    int
	adopted []QueryRun
}

func (stub *stubRunner) Rerun(*Step, context.Context) QueryRun {
	stub.runs++
	return QueryRun{Rows: &queryRows{value: []any{stub.runs}}}
}

func (stub *stubRunner) Adopt(run QueryRun) { stub.adopted = append(stub.adopted, run) }

func TestQueryRefreshAdoptsItsLastApplicationsRunsOnlyAGroupedCommitThatRolledBackAppliesAgainAlone(t *testing.T) {
	stub := &stubRunner{}
	refresh := NewQueryRefresh([]QueryRunner{stub}, context.Background())
	for range 2 {
		if _, err := refresh.Write.Apply(&Step{}); err != nil {
			t.Fatal(err)
		}
	}
	refresh.Adopt()
	if len(stub.adopted) != 1 || stub.adopted[0].Rows.value[0] != 2 {
		t.Fatalf("adopted = %+v, want the second run only", stub.adopted)
	}
}
