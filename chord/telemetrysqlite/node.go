package telemetrysqlite

// Ports packages/chord/src/telemetry-sqlite/node.ts (Pi main c23b9ec1). The SQLite telemetry store read-only, on a file:
// a store file that recorders of other processes write, opened for its reads (`TelemetryStore`) only.

import (
	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	sqlitenode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// SqliteTelemetryStore is a telemetry store opened for reads: the store's reads, and the close of its file.
type SqliteTelemetryStore struct {
	db chordsqlite.SqliteDatabase
}

var _ telemetry.TelemetryStore = (*SqliteTelemetryStore)(nil)

// OpenTelemetryStore opens the telemetry store at path read-only (SQLite's read-only mode: it never writes the file,
// never migrates it, never creates it), while recorders of other processes write it or after. It fails for a file that
// holds no store or a store of another version (a recorder, CreateSqliteTelemetry, migrates an older one).
func OpenTelemetryStore(path string) (*SqliteTelemetryStore, error) {
	db, err := sqlitenode.OpenNodeSqliteDatabase(path, sqlitenode.NodeSqliteDatabaseOptions{Mode: sqlitenode.ReadOnly})
	if err != nil {
		return nil, err
	}
	if err := assertTelemetryStore(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SqliteTelemetryStore{db: db}, nil
}

// GetTrace reads the committed spans of one trace.
func (store *SqliteTelemetryStore) GetTrace(traceID string) (*telemetry.StoredTrace, error) {
	return getTrace(store.db, traceID)
}

// FindTraces reads committed traces.
func (store *SqliteTelemetryStore) FindTraces(query telemetry.TraceQuery) (telemetry.FindTracesResult, error) {
	return findTraces(store.db, query)
}

// FindMetrics reads committed metric rows.
func (store *SqliteTelemetryStore) FindMetrics(query telemetry.MetricQuery) ([]telemetry.StoredMetric, error) {
	return findMetrics(store.db, query)
}

// Close closes the file; reads after it fail.
func (store *SqliteTelemetryStore) Close() error { return store.db.Close() }
