package node

import (
	"errors"
	"testing"
)

type failingConnection struct {
	checkpointErr error
	closeErr      error
	closed        bool
}

func (connection *failingConnection) Exec(string) error { return connection.checkpointErr }

func (connection *failingConnection) Prepare(string) (*StatementSync, error) {
	return nil, errors.New("unexpected prepare")
}

func (connection *failingConnection) Close() error {
	connection.closed = true
	return connection.closeErr
}

// NodeSqliteDatabase.close runs the checkpoint in try and DatabaseSync.close in finally
// (packages/durable/src/storage/sqlite/node.ts:161-171): the connection always closes, and a close failure replaces
// the checkpoint failure.
func TestNodeSqliteDatabaseCloseReportsTheFinallyFailure(t *testing.T) {
	checkpoint := errors.New("checkpoint failed")
	closing := errors.New("close failed")
	for _, test := range []struct {
		name                string
		checkpoint, closing error
		want                error
	}{
		{"checkpoint only", checkpoint, nil, checkpoint},
		{"close only", nil, closing, closing},
		{"both", checkpoint, closing, closing},
		{"neither", nil, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := &failingConnection{checkpointErr: test.checkpoint, closeErr: test.closing}
			adapter := newNodeSqliteDatabase(connection)
			if err := adapter.Close(); !errors.Is(err, test.want) || (test.want == nil && err != nil) {
				t.Fatalf("Close() = %v, want %v", err, test.want)
			}
			if !connection.closed {
				t.Fatal("Close() did not close the connection")
			}
			if err := adapter.Close(); err != nil {
				t.Fatalf("second Close() = %v, want nil", err)
			}
		})
	}
}
