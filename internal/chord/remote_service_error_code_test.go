package chord

import "testing"

// Pi services/errors.ts:1-16: isRemoteServiceErrorCode(value: unknown) is true only for a string that is one of REMOTE_SERVICE_ERROR_CODES.
func TestIsRemoteServiceErrorCodeIsATypeGuard(t *testing.T) {
	wantOrder := []string{"service_not_allowed", "service_not_found", "service_mode_mismatch", "service_member_not_found", "service_member_mismatch", "service_instance_not_found", "service_stale_instance", "service_invalid_value"}
	if len(RemoteServiceErrorCodes) != len(wantOrder) {
		t.Fatalf("codes=%v", RemoteServiceErrorCodes)
	}
	for i, code := range wantOrder {
		if string(RemoteServiceErrorCodes[i]) != code || !IsRemoteServiceErrorCode(code) || !IsRemoteServiceErrorCode(RemoteServiceErrorCodes[i]) {
			t.Errorf("code %d %q not accepted in upstream order", i, code)
		}
	}
	for _, value := range []any{"", "Service_not_found", "service_unknown", 1, nil, []string{"service_not_found"}, remoteError(ErrServiceNotFound, "x")} {
		if IsRemoteServiceErrorCode(value) {
			t.Errorf("IsRemoteServiceErrorCode(%#v) = true, want false", value)
		}
	}
}
