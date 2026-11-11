package hostdata

import (
	"context"
	"errors"
	"testing"
)

func panicsUnregistered(t *testing.T, name string, call func()) {
	t.Helper()
	defer func() {
		if got := recover(); !errors.Is(got.(error), ErrUnregistered) {
			t.Fatalf("%s before registration: recovered %v, want ErrUnregistered", name, got)
		}
	}()
	call()
}

func TestAnOperationBeforeTheStoreRegisteredFailsLoudly(t *testing.T) {
	registered = nil
	ctx := context.Background()
	panicsUnregistered(t, "TablesToMigrate", func() { TablesToMigrate(nil, nil) })
	panicsUnregistered(t, "Owners", func() { _, _ = Owners(nil, "k", nil, ctx) })
	panicsUnregistered(t, "Import", func() { _, _ = Import(nil, nil, Plan{}, Commit{}, ctx) })
	panicsUnregistered(t, "Delete", func() { _, _ = Delete(nil, "k", Commit{}, ctx) })
}

func TestRegisteredOperationsAreCalled(t *testing.T) {
	defer func() { registered = nil }()
	Register(Operations{
		Delete: func(store any, key string, commit Commit, ctx context.Context) (Counts, error) {
			return Counts{Documents: 1, Rows: len(key)}, nil
		},
	})
	counts, err := Delete(nil, "abc", Commit{}, context.Background())
	if err != nil || counts != (Counts{Documents: 1, Rows: 3}) {
		t.Fatalf("Delete = %+v, %v", counts, err)
	}
}
