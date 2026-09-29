package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// Pi runtime-credentials.ts:25,33,48 uses throwIfAborted, which throws the original reason rather than replacing it with a generic cancellation.
func TestRuntimeCredentialsPreservesCancellationCause(t *testing.T) {
	for _, operation := range []string{"read", "list", "delete"} {
		t.Run(operation, func(t *testing.T) {
			store := &runtimeCredentialStoreProbe{}
			credentials := NewRuntimeCredentials(store)
			credentials.SetRuntimeAPIKey("provider", "runtime-key")
			ctx, cancel := context.WithCancelCause(t.Context())
			reason := errors.New("caller cancelled " + operation)
			cancel(reason)
			var err error
			switch operation {
			case "read":
				_, err = credentials.Read(ctx, "provider")
			case "list":
				_, err = credentials.List(ctx)
			case "delete":
				err = credentials.Delete(ctx, "provider")
			}
			if err != reason { //nolint:errorlint // Pi throws the identical caller-provided reason; wrapping changes this local identity contract.
				t.Fatalf("%s returned %v, want original cause %v", operation, err, reason)
			}
			wantCalls := 0
			if operation == "list" {
				wantCalls = 1 // Pi awaits the base list before its own abort check.
			}
			if len(store.received) != wantCalls || !credentials.HasRuntimeAPIKey("provider") {
				t.Fatalf("base calls=%d, override retained=%v", len(store.received), credentials.HasRuntimeAPIKey("provider"))
			}
			data, marshalErr := json.Marshal([]any{operation, err.Error(), len(store.received)})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			fmt.Println("RUNTIME_CREDENTIAL_CANCEL " + string(data))
		})
	}
}
