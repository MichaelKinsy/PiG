//go:build !windows

package plugins

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Not a port of a case of upstream's tests, a case of its behavior: `#watchAlone` passes every event of a recorded file to
// `mark`, so an attribute change is compared like a write (libuv reports `IN_ATTRIB` as `change`). A file made unreadable
// differs (it cannot be read: `readRecorded` fails), and clear again once it is readable with the bytes it had. A change of
// times only compares the same bytes. Unix only: on Windows `os.Chmod` only toggles the read-only attribute. Pi, run on the
// same steps (the review of this slice, `chmod 000 then 644`): [true, false]; touch: [false].
func TestTheBaseMarkComparesARecordedFileWhoseAttributesChangedAnUnreadableOneDiffersAReadableOneClears(t *testing.T) {
	_, f := based(t, nil)
	dir := realOf(t, tempRoot(t))
	file := filepath.Join(dir, "entry.ts")
	write(t, file, "as built")
	f.recordBase(model.BaseInputs{file: hashOf("as built")})
	f.settled()
	if f.marks().Base {
		t.Fatal("base set at the record")
	}
	if err := os.Chtimes(file, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	f.settled()
	if f.marks().Base {
		t.Fatal("base set by a change of times")
	}
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o644) })
	if _, err := os.ReadFile(file); err == nil {
		t.Fatal("this user reads a file with no permission: the case needs a user that does not")
	}
	f.baseIs(true)
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	f.baseIs(false)
}
