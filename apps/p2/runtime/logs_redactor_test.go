package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A secret added after a redaction is redacted by the next one (the ordered secrets are rebuilt when the set changes), the
// longest first as before.
func TestRedactorRedactsASecretAddedAfterARedactionLongestFirst(t *testing.T) {
	redactor := NewLogRedactor()
	if got := redactor.Redact("value hunter2 value"); got != "value hunter2 value" {
		t.Fatalf("before = %q", got)
	}
	redactor.AddSecret("hunter2")
	if got := redactor.Redact("value hunter2 value"); got != "value [redacted] value" {
		t.Fatalf("after = %q", got)
	}
	redactor.AddSecret("hunter2-and-more")
	if got := redactor.Redact("hunter2-and-more"); got != "[redacted]" {
		t.Fatalf("longest first = %q", got)
	}
	redactor.AddSecret("a b")
	if got := redactor.Redact("a%20b"); got != "[redacted]" {
		t.Fatalf("encoded = %q", got)
	}
}

// The credential files of the directories registered are read at each redaction until the directory is removed: a process that
// started servers one after another (a test binary) stopped paying for the ones that are gone.
func TestRedactorStopsReadingTheDirectoriesRemovedUnderARoot(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "data")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	write := func(dir, secret string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"`+secret+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	redactor := NewLogRedactor()
	redactor.AddDirectory(directory)
	redactor.AddDirectory(other)
	write(directory, "first-secret")
	if got := redactor.Redact("first-secret"); got != "[redacted]" {
		t.Fatalf("a registered directory's secret = %q", got)
	}
	redactor.RemoveDirectoriesUnder(root)
	write(directory, "second-secret")
	if got := redactor.Redact("second-secret"); !strings.Contains(got, "second-secret") {
		t.Fatalf("a removed directory was read: %q", got)
	}
	// What it had read stays, and a directory outside the root is still read.
	if got := redactor.Redact("first-secret"); got != "[redacted]" {
		t.Fatalf("a secret already read = %q", got)
	}
	write(other, "third-secret")
	if got := redactor.Redact("third-secret"); got != "[redacted]" {
		t.Fatalf("a directory outside the root = %q", got)
	}
}
