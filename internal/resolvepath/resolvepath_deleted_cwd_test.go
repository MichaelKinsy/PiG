package resolvepath

import (
	"errors"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi's resolvePath is Node's path.resolve, which throws ENOENT from
// process.cwd() when it needs a removed working directory and never reads the
// working directory when an argument is absolute.
func TestResolveReportsAnUnreadableWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if got, err := Resolve("session.jsonl", ""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve(relative, process cwd) = %q, %v; want ENOENT", got, err)
	}
	if got, err := Resolve("session.jsonl", "rel"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Resolve(relative, relative base) = %q, %v; want ENOENT", got, err)
	}
	if got, err := Resolve("session.jsonl", "/base"); err != nil || got != "/base/session.jsonl" {
		t.Fatalf("Resolve(relative, absolute base) = %q, %v", got, err)
	}
	if got, err := Resolve("/abs/../x", ""); err != nil || got != "/x" {
		t.Fatalf("Resolve(absolute) = %q, %v", got, err)
	}
}
