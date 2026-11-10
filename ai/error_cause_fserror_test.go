package ai

import (
	"errors"
	"fmt"
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
