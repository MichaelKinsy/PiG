package chord

import (
	"errors"
	"testing"
)

// services/errors.ts:18-26: RemoteServiceError(code, message) keeps both; Error() is the message; the code is one of the eight codes.
func TestNewRemoteServiceErrorKeepsCodeAndMessage(t *testing.T) {
	for _, code := range RemoteServiceErrorCodes {
		err := NewRemoteServiceError(code, "boom "+string(code))
		if err.Code != code || err.Error() != "boom "+string(code) || !IsRemoteServiceErrorCode(string(err.Code)) {
			t.Errorf("NewRemoteServiceError(%q) = %+v", code, err)
		}
	}
	var target *RemoteServiceError
	wrapped := remoteError(ErrServiceNotFound, "no %s", "service")
	if !errors.As(wrapped, &target) || target.Code != ErrServiceNotFound || target.Message != "no service" {
		t.Fatalf("remoteError = %+v", wrapped)
	}
}
