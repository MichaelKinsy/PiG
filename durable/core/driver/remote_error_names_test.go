package driver_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
)

// Pi 1.2.0 durable/src/errors.ts:17-34, :49 and the Session: StorageRequestError, ConversationBusy and ReadAfterWrite are different classes (`this.name` is the class name) and the
// Session tells them apart by class, so a remote error maps to a typed Pi error only when its name is that class's name: a StorageRequestError target must not match an error named
// otherwise (a poisoning storage failure arrives as a plain Error), and a StorageRequestError-named error must not match the other two targets.
// mutation-checked: dropping the name check of the StorageRequestError case makes every foreign name a StorageRequestError and fails it.
func TestRemoteErrorMapsToATypedErrorOnlyByItsClassName(t *testing.T) {
	for _, name := range []string{"Error", "ConversationBusy", "ReadAfterWrite", "TypeError", ""} {
		if rejected, ok := errors.AsType[*durable.StorageRequestError](error(&driver.CoreError{Name: name, Message: "boom"})); ok {
			t.Errorf("an error named %q matched *StorageRequestError: %+v", name, rejected)
		}
	}
	rejectedError := error(&driver.CoreError{Name: "StorageRequestError", Message: "Conversation 7 is busy"})
	var busy *durable.ConversationBusy
	var afterWrite *durable.ReadAfterWrite
	if errors.As(rejectedError, &busy) || errors.As(rejectedError, &afterWrite) {
		t.Errorf("a StorageRequestError-named error matched another class: busy=%+v readAfterWrite=%+v", busy, afterWrite)
	}
	var rejected *durable.StorageRequestError
	if !errors.As(rejectedError, &rejected) || rejected.Message != "Conversation 7 is busy" {
		t.Errorf("a StorageRequestError-named error did not map: %+v", rejected)
	}
}
