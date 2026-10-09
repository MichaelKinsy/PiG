package chord

// Pins packages/chord/src/services/errors.ts by value: REMOTE_SERVICE_ERROR_CODES (errors.ts:1-10) travel on the
// session-worker wire (session-worker.ts:61 validates them against the same list), and RemoteServiceError
// (errors.ts:18-26) keeps the code beside a message that is the error text alone. The services tests compare errors with
// the exported constants, so a changed literal passes them while every peer reads a different code.

import (
	"errors"
	"fmt"
	"testing"
)

// Pi: packages/chord/src/services/errors.ts:19 (code)
// Pi: packages/chord/src/services/errors.ts:21 (message)
// packages/chord/src/services/errors.ts:18-25: RemoteServiceError carries a code of the RemoteServiceErrorCode union.
func TestRemoteServiceErrorCodesAreTheStringsErrorsTSDeclares(t *testing.T) {
	for _, tc := range []struct {
		code RemoteServiceErrorCode
		want string
	}{
		{ErrServiceNotAllowed, "service_not_allowed"},
		{ErrServiceNotFound, "service_not_found"},
		{ErrServiceModeMismatch, "service_mode_mismatch"},
		{ErrServiceMemberNotFound, "service_member_not_found"},
		{ErrServiceMemberMismatch, "service_member_mismatch"},
		{ErrServiceInstanceNotFound, "service_instance_not_found"},
		{ErrServiceStaleInstance, "service_stale_instance"},
		{ErrServiceInvalidValue, "service_invalid_value"},
	} {
		if string(tc.code) != tc.want {
			t.Errorf("code = %q, want %q", tc.code, tc.want)
		}
	}

	err := fmt.Errorf("call failed: %w", remoteError(ErrServiceStaleInstance, "Service %s instance is stale", "demo"))
	var remote *RemoteServiceError
	if !errors.As(err, &remote) || remote.Code != ErrServiceStaleInstance || remote.Error() != "Service demo instance is stale" || remote.Message != "Service demo instance is stale" {
		t.Fatalf("RemoteServiceError = %#v, want the code and the message alone", remote)
	}
	if !hasRemoteServiceErrorCode(err, ErrServiceStaleInstance) || hasRemoteServiceErrorCode(err, ErrServiceNotFound) || hasRemoteServiceErrorCode(errors.New("plain"), ErrServiceStaleInstance) {
		t.Fatal("IsRemoteServiceErrorCode does not match exactly the wrapped code")
	}
}
