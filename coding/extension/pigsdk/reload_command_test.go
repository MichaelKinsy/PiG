package pigsdk

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// `pig reload` is the pre-session counterpart to the interactive /reload: both
// make what is loaded match what is on disk. It replaced `pig sdk`, whose five
// verbs split one action across sync and prune and duplicated `pig diagnose`
// for reporting.

func TestReloadStagesAndPrunesInOneCommand(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)

	var out, errOut bytes.Buffer
	if code := RunCommand([]string{"reload"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	got := out.String()
	// A restage is what makes a build stale, so pruning has to happen in the
	// same command; otherwise the SDK is current while the builds selected
	// against it are not.
	for _, want := range []string{"staged go", "staged python", "staged rust", "unreachable build"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// The accessors exist for build scripts. They must ensure the SDK is current
// first, or a script wires an extension to a directory pig is about to restage.
func TestReloadSDKPathStagesBeforePrinting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)

	var out, errOut bytes.Buffer
	if code := RunCommand([]string{"reload", "--sdk-path"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	dir := strings.TrimSpace(out.String())
	if dir == "" {
		t.Fatal("no path printed")
	}
	// The failure this guards is printing a path that is not staged yet: a
	// setup script would symlink an extension to an empty directory. So assert
	// the directory exists and holds SDK source, not merely that the string
	// looks right.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("printed %q but it is not a directory: %v", dir, err)
	}
	var hasGoSource bool
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			hasGoSource = true
		}
	}
	if !hasGoSource {
		t.Errorf("printed %q but nothing was staged there (%d entries); a build "+
			"script would wire an extension to an empty directory", dir, len(entries))
	}
}

func TestReloadSDKPathAcceptsALanguage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)

	for _, tc := range []struct {
		args       []string
		wantSuffix string
	}{
		{[]string{"reload", "--sdk-path", "rust"}, "sdk-rs"},
		{[]string{"reload", "--sdk-path=python"}, "sdk-py"},
		{[]string{"reload", "--sdk-path"}, "sdk"}, // defaults to go
	} {
		var out, errOut bytes.Buffer
		if code := RunCommand(tc.args, &out, &errOut); code != 0 {
			t.Fatalf("%v: exit %d, stderr=%s", tc.args, code, errOut.String())
		}
		if got := strings.TrimSpace(out.String()); !strings.HasSuffix(got, tc.wantSuffix) {
			t.Errorf("%v printed %q, want a path ending %q", tc.args, got, tc.wantSuffix)
		}
	}
}

// --dry-run must not stage: its whole purpose is to report without changing
// anything a subsequent build would select.
func TestReloadDryRunStagesNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)

	var out, errOut bytes.Buffer
	if code := RunCommand([]string{"reload", "--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	if got := out.String(); strings.Contains(got, "staged go") {
		t.Errorf("--dry-run staged the SDKs:\n%s", got)
	}
	if got := out.String(); !strings.Contains(got, "would drop") {
		t.Errorf("--dry-run did not report what it would drop:\n%s", got)
	}
}

// The dispatcher tries each bundled command in turn, so a non-match must return
// -1 rather than consuming the args.
func TestReloadDeclinesOtherCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{{}, {"docs"}, {"sdk"}, {"sdk", "sync"}} {
		if code := RunCommand(args, &out, &errOut); code != -1 {
			t.Errorf("RunCommand(%v) = %d, want -1 so the dispatcher can try the next command", args, code)
		}
	}
}

// A dry run on a root that has never been staged must report what it would do.
// Erroring there told the user to run the very command they were previewing.
func TestReloadDryRunOnAFreshRootIsNotCircular(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)

	var out, errOut bytes.Buffer
	if code := RunCommand([]string{"reload", "--dry-run"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr=%s", code, errOut.String())
	}
	if got := errOut.String(); strings.Contains(got, "run `pig reload`") {
		t.Errorf("the preview told the user to run the command they are previewing:\n%s", got)
	}
	if got := out.String(); !strings.Contains(got, "would stage go") {
		t.Errorf("a fresh root did not report the staging it would do:\n%s", got)
	}
}
