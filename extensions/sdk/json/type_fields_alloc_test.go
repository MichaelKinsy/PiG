package json

import (
	"reflect"
	"testing"
)

// typeFields runs once for each struct type, in the first use of the type, and the alloc-baseline gate reads what it allocates in the first iteration of the benchmarks that encode the type. Its two sorts allocate nothing: with sort.Slice they made six objects of the 28 a call for fieldOrderDeclared, not 22.
func TestTypeFieldsAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	typ := reflect.TypeFor[fieldOrderDeclared]()
	typeFields(typ) // the encoders of its fields are cached after this call
	if got := testing.AllocsPerRun(50, func() { typeFields(typ) }); got > 22 {
		t.Errorf("typeFields allocated %v objects a call, want at most 22", got)
	}
}
