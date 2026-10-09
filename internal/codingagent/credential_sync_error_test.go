package codingagent

import (
	"errors"
	"testing"
)

// model-runtime.ts:135-150 CredentialSynchronizationError: the message names the operation and provider, `name` is set by the constructor, and
// `cause` comes from the options (Unwrap, as D103 maps it).
func TestCredentialSynchronizationErrorCarriesNameAndCause(t *testing.T) {
	cause := errors.New("disk full")
	e := NewCredentialSynchronizationError("anthropic", CredentialSynchronizationLogin, nil, cause)
	want := "Credential login committed for anthropic, but local synchronization failed"
	if e.Error() != want || e.Name() != "CredentialSynchronizationError" || !errors.Is(e, cause) {
		t.Fatalf("error=%q name=%q unwrap=%v", e.Error(), e.Name(), errors.Unwrap(e))
	}
}
