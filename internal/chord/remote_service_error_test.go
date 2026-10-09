package chord

import (
	"errors"
	"testing"
)

// upstream: packages/chord/src/services/types.ts RemoteServiceError carries a code and the message that Error.message reports.
func TestRemoteServiceErrorMessageIsTheErrorText(t *testing.T) {
	err := remoteError(ErrServiceNotFound, "service %s not found", "counter")
	var remote *RemoteServiceError
	if !errors.As(err, &remote) {
		t.Fatalf("%T is not a RemoteServiceError", err)
	}
	if remote.Message != "service counter not found" || remote.Error() != remote.Message || remote.Code != ErrServiceNotFound {
		t.Fatalf("RemoteServiceError = %+v, Error() = %q", remote, remote.Error())
	}
	if !hasRemoteServiceErrorCode(err, ErrServiceNotFound) || hasRemoteServiceErrorCode(err, ErrServiceNotAllowed) {
		t.Fatal("code matching must hold for the code only")
	}
}
