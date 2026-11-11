// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPrepareWritesAddedLinesAndRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	d := &data{Files: []prFile{
		{Filename: "a/b.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n-old\n+new one\n+new two\n context"},
		{Filename: "gone.go", Status: "removed", Patch: "@@ -1 +0,0 @@\n-x"},
	}}
	ev := evaluation{Scans: []string{"gitleaks"}}
	if err := prepare(context.Background(), config{}, d, ev, dir, io.Discard); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "added", "a", "b.go"))
	if err != nil || string(got) != "new one\nnew two\n" {
		t.Fatalf("added lines %q, err %v", got, err)
	}
	list, _ := os.ReadFile(filepath.Join(dir, "files.txt"))
	if string(list) != "a/b.go\n" {
		t.Fatalf("files.txt %q", list)
	}
	d.Files = []prFile{{Filename: "../escape", Status: "added", Patch: "+x"}}
	if err := prepare(context.Background(), config{}, d, ev, dir, io.Discard); err == nil || !strings.Contains(err.Error(), "leaves the scan directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordVerdict(t *testing.T) {
	ev := evaluation{Checks: map[string]verdict{"gitleaks": {Result: resultScan}, "trufflehog": {Result: resultScan}, "signed": {Result: resultPass}}}
	ev.finish()
	if ev.Result != resultScan {
		t.Fatalf("result %s", ev.Result)
	}
	if err := recordVerdict(&ev, "gitleaks", 0, ""); err != nil || ev.Checks["gitleaks"].Result != resultPass || ev.Result != resultScan {
		t.Fatalf("after a clean scan: %+v %v", ev, err)
	}
	if err := recordVerdict(&ev, "trufflehog", 183, "\nfound: aws key in x\n"); err != nil || ev.Result != resultFail || !strings.Contains(ev.Checks["trufflehog"].Reason, "aws key in x") {
		t.Fatalf("after a failing scan: %+v %v", ev, err)
	}
	if err := recordVerdict(&ev, "signed", 0, ""); err == nil {
		t.Fatal("a check not waiting for a scan accepted a verdict")
	}
	if err := recordVerdict(&ev, "nope", 0, ""); err == nil {
		t.Fatal("an unknown check accepted a verdict")
	}
}

func TestAddedLineNumbers(t *testing.T) {
	patch := "@@ -1,3 +1,4 @@\n keep\n-gone\n+new 2\n+new 3\n keep\n@@ -10,2 +11,2 @@\n keep 11\n-x\n+new 12\n\\ No newline at end of file"
	got := addedLineNumbers(patch)
	if want := []int{2, 3, 12}; !slices.Equal(got, want) {
		t.Fatalf("added lines %v, want %v", got, want)
	}
}
