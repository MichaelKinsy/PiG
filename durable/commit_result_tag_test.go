package durable

import (
	"errors"
	"reflect"
	"testing"
)

// Pi 32c71a6d4 tags CommitResult with kind: "stored" | "refused".
func TestCommitResultIsTagged(t *testing.T) {
	typ := reflect.TypeFor[CommitResult]()
	field, ok := typ.FieldByName("Kind")
	if !ok {
		t.Fatalf("CommitResult has no Kind field (fields: %d); want tagged union with Kind stored | refused", typ.NumField())
	}
	if field.Type.Kind() != reflect.String {
		t.Fatalf("CommitResult.Kind has underlying kind %v, want string", field.Type.Kind())
	}
	if stored := StoredCommit(3); stored.Kind != CommitStored || string(stored.Kind) != "stored" || stored.Stored != 3 || stored.Refused != nil {
		t.Fatalf("StoredCommit(3) = %+v", stored)
	}
	boom := errors.New("boom")
	if refused := RefusedCommit(1, boom); refused.Kind != CommitRefused || string(refused.Kind) != "refused" || refused.Stored != 1 || !errors.Is(refused.Refused, boom) {
		t.Fatalf("RefusedCommit(1, boom) = %+v", refused)
	}
}
