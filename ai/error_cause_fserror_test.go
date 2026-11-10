package ai

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// A Node fs error has no `cause`, so withCauseDetail appends nothing to a message that interpolates one
// (identity-token.mjs: `Failed to read identity token file at ${path}: ${err}`). The Go error behind an FSError is the platform's own
// description of the failure: on Windows ERROR_FILE_NOT_FOUND reads "The system cannot find the file specified.", which Node's message
// does not contain, so walking into it appended it to the message there while Linux hid it ("no such file or directory" is in Node's).
func TestWithCauseDetailEndsAtANodeFSError(t *testing.T) {
	t.Parallel()
	platform := errors.New("The system cannot find the file specified.")
	read := &nodeerrno.FSError{Syscall: "open", Path: "/run/absent.jwt", Code: "ENOENT", Err: platform}
	const node = "ENOENT: no such file or directory, open '/run/absent.jwt'"
	if read.Error() != node {
		t.Fatalf("FSError = %q, want %q", read.Error(), node)
	}
	for name, test := range map[string]struct {
		err  error
		want string
	}{
		"an fs error wrapped in a message": {
			err:  fmt.Errorf("Failed to read identity token file at /run/absent.jwt: Error: %w", read),
			want: "Failed to read identity token file at /run/absent.jwt: Error: " + node,
		},
		"an fs error": {err: read, want: node},
		"an fs error at the end of a cause chain": {
			err:  &causeChain{"Connection error.", fmt.Errorf("Error: %w", read)},
			want: "Connection error.: " + node,
		},
	} {
		if got := WithCauseDetail(test.err); got != test.want {
			t.Errorf("%s: WithCauseDetail = %q, want %q", name, got, test.want)
		}
	}
}

// windowsFileNotFound is what os.Open reports on Windows for ERROR_FILE_NOT_FOUND: the platform's own description, which fs.ErrNotExist
// matches. It stands in for the syscall.Errno Windows has, so the Windows path of the message runs on every platform.
type windowsFileNotFound struct{}

func (windowsFileNotFound) Error() string        { return "The system cannot find the file specified." }
func (windowsFileNotFound) Is(target error) bool { return target == fs.ErrNotExist }

// readAnthropicIdentityToken builds its error from the *fs.PathError os.ReadFile returns; on Windows that is not Node's text, and
// WithCauseDetail must leave Node's message alone (identity-token.mjs:14-16).
func TestWithCauseDetailOfAWindowsPathErrorReadsAsNode(t *testing.T) {
	t.Parallel()
	const path = `C:\run\absent.jwt`
	opened := &fs.PathError{Op: "open", Path: path, Err: windowsFileNotFound{}}
	if got, want := opened.Error(), "open "+path+": The system cannot find the file specified."; got != want {
		t.Fatalf("PathError = %q, want %q", got, want)
	}
	err := fmt.Errorf("Failed to read identity token file at %s: Error: %w", path, nodeerrno.FromPathError(opened))
	const want = "Failed to read identity token file at " + path + ": Error: ENOENT: no such file or directory, open '" + path + "'"
	if got := WithCauseDetail(err); got != want {
		t.Errorf("WithCauseDetail = %q, want %q", got, want)
	}
}
