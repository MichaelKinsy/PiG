package host

import (
	"strings"
	"testing"
)

// packages/chord/src/cell.ts:177-183 (Cell.initialize): a cell declared with init has no value until initialize sets its first one, validated
// against the schema as `initial value of state <key>`; a second initialize throws `state <key> is already initialized`, and the value stays.
func TestCellInitializeSetsTheFirstValueOfACellAwaitingInitValidatedAndOnlyOnce(t *testing.T) {
	cell, err := NewCellAwaitingInit[any]("n", kw("type", "number"), allowWrites, SourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if thrown := chordPanic(func() { _ = cell.Value() }); thrown == nil || thrown.Code != CodeNotReady || !strings.Contains(thrown.Message, "state n (its init has not run)") {
		t.Fatalf("Value before Initialize panicked with %v, want not_ready naming the state", thrown)
	}
	if err := cell.Initialize("no"); err == nil || !strings.Contains(err.Error(), "initial value of state n") {
		t.Fatalf("Initialize with a string = %v, want the schema refusal of the initial value", err)
	}
	if thrown := chordPanic(func() { _ = cell.Value() }); thrown == nil {
		t.Fatal("a refused Initialize left the cell ready")
	}
	if err := cell.Initialize(7.0); err != nil {
		t.Fatalf("Initialize(7) = %v", err)
	}
	var got any
	if thrown := chordPanic(func() { got = cell.Value() }); thrown != nil || got != 7.0 {
		t.Fatalf("Value after Initialize = %v (panicked with %v), want 7", got, thrown)
	}
	if err := cell.Initialize(8.0); err == nil || err.Error() != "state n is already initialized" {
		t.Fatalf("second Initialize = %v, want `state n is already initialized`", err)
	}
	if thrown := chordPanic(func() { got = cell.Value() }); thrown != nil || got != 7.0 {
		t.Fatalf("Value after a refused second Initialize = %v (panicked with %v), want 7", got, thrown)
	}
}
