package app

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Pi's `startServer` gives the app's `telemetry` to `createHttp`, so each routed request is a `chord.http` span of the server in the
// store. The kit once left the HTTP component without it: the store held the host's spans and no request's.
func TestAnAppWithATelemetryStoreRecordsEachRoutedRequestAsAChordHTTPSpan(t *testing.T) {
	db, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := telemetrysqlite.CreateSqliteTelemetry(db, telemetrysqlite.SqliteTelemetryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	s := startTestApp(t, nil, func(o *AppOptions) { o.Telemetry = store })
	// A public route of the kit (the pairing's): the request is routed whatever the answer.
	sendTo(t, s.port, "POST", PairPath, nil, `{"label":"laptop"}`)
	name := "chord.http"
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := store.Flush(); err != nil {
			t.Fatal(err)
		}
		found, err := store.FindTraces(telemetry.TraceQuery{Name: &name})
		if err != nil {
			t.Fatal(err)
		}
		for _, trace := range found.Traces {
			if route, _ := trace.Spans[0].Attributes.Get("route"); route == "POST "+PairPath {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no chord.http span for POST %s in the store (%d traces named chord.http)", PairPath, len(found.Traces))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
