package host

import (
	"reflect"
	"testing"
)

// GO-ONLY (Pi has no case for it): a new target forgets the last value's rows (list.ts:144 `rows.reset()`): another target decodes its own way, so a row of the
// same key and the same stored values is decoded again and is not the object the previous target gave. Within one target it
// is reused (the control: a touch of another row leaves the object). The store's plugin is reloaded with another order, the
// target of the list is another, and its first page holds a row (id 3) of the first page of the previous one. The mutant that
// does not reset keeps the previous object.
func TestListANewTargetDecodesItsRowsAgain(t *testing.T) {
	w := lsHosted(t, 3, 5, nil, nil)
	rows, _ := mustListValue(t, w.screen)
	before := reflect.ValueOf(rows[2]).Pointer() // id 3, the last of [5 4 3]
	lsExpect(t, "ids", rowIDs(rows), []int{5, 4, 3})
	w.touch(5, 500)
	rows, _ = mustListValue(t, w.screen)
	if got := reflect.ValueOf(rows[2]).Pointer(); got != before {
		t.Fatalf("a touch of another row decoded row 3 again (%x, was %x): within one target its object is reused", got, before)
	}
	_, err := w.host.Reload([]HostFacet{lsStorePlugin("asc", nil), w.plugins["screen"]}, background, ReloadOptions{})
	must(t, err)
	lsSettled(w.host)
	rows, _ = mustListValue(t, w.screen)
	lsExpect(t, "ids", rowIDs(rows), []int{1, 2, 3})
	if got := reflect.ValueOf(rows[2]).Pointer(); got == before {
		t.Fatalf("row 3 after the other target is the object of the previous one (%x): nothing of it may be reused", got)
	}
	w.close()
}
