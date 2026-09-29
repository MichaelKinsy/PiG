package compaction

import (
	"reflect"
	"testing"
)

// Both Pi compaction utils sort file names by UTF-16 code units, not UTF-8 bytes.
func TestCompactionFileListsUseJavaScriptSort(t *testing.T) {
	ops := NewFileOps()
	ops.Read["\ue000"] = struct{}{}
	ops.Read["\U00010000"] = struct{}{}
	read, modified := ComputeFileLists(ops)
	if !reflect.DeepEqual(read, []string{"\U00010000", "\ue000"}) || len(modified) != 0 {
		t.Fatalf("read=%q modified=%q", read, modified)
	}
}
