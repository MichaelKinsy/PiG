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

// compaction/utils.ts computeFileLists drops read paths that were also edited or written, sorts both lists, and formatFileOperations renders them. The expected values are Pi 1.0.0's output for the same Sets under Node 24.
func TestComputeAndFormatFileListsMatchPi(t *testing.T) {
	ops := NewFileOps()
	for _, path := range []string{"b.ts", "x.ts", "a.ts", "y.ts", "Z.ts", "é.ts", "😀.ts", "\uffff.ts"} {
		ops.Read[path] = struct{}{}
	}
	ops.Edited["x.ts"] = struct{}{}
	ops.Written["y.ts"] = struct{}{}
	ops.Written["c.ts"] = struct{}{}
	read, modified := ComputeFileLists(ops)
	if want := []string{"Z.ts", "a.ts", "b.ts", "é.ts", "😀.ts", "\uffff.ts"}; !reflect.DeepEqual(read, want) {
		t.Fatalf("readFiles = %q, want %q", read, want)
	}
	if want := []string{"c.ts", "x.ts", "y.ts"}; !reflect.DeepEqual(modified, want) {
		t.Fatalf("modifiedFiles = %q, want %q", modified, want)
	}
	const want = "\n\n<read-files>\nZ.ts\na.ts\nb.ts\né.ts\n😀.ts\n\uffff.ts\n</read-files>\n\n<modified-files>\nc.ts\nx.ts\ny.ts\n</modified-files>"
	if got := FormatFileOperations(read, modified); got != want {
		t.Fatalf("formatFileOperations =\n%q\nwant\n%q", got, want)
	}
}
