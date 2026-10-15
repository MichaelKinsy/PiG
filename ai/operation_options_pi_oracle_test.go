package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's Models auth operations and InMemoryModelsStore (models.ts checkAuth/getAvailable/getAvailableOfType/getAllAvailable/
// logout, models-store.ts read/write/delete, Node, types stripped) and PiG's AuthOperationOptions and ModelsStoreOperationOptions
// methods over one matrix: no signal, a signal already cancelled (default cause, or a cause), and a signal cancelled while the
// provider's auth check blocks. Pi and PiG must return the same value without a signal, reject with the signal's reason when it is
// cancelled, leave the credential and the stored catalog untouched when the operation is rejected up front, and treat an empty
// provider id as every provider.
type operationOptionsCase struct {
	Op         string `json:"op"`
	ProviderID string `json:"providerId"`
	Signal     string `json:"signal"`
	Blocked    bool   `json:"blocked,omitempty"`
}

type operationOptionsResult struct {
	Outcome         string          `json:"outcome"`
	Value           json.RawMessage `json:"value"`
	Name            string          `json:"name"`
	Message         string          `json:"message"`
	CredentialAfter bool            `json:"credentialAfter"`
	StoreAfter      json.RawMessage `json:"storeAfter"`
}

// operationOptionsCaseTimeout bounds one case: an operation that ignores its signal blocks on the provider's auth check forever.
const operationOptionsCaseTimeout = 10 * time.Second

func operationOptionsMatrix() []operationOptionsCase {
	var cases []operationOptionsCase
	for _, c := range []operationOptionsCase{
		{Op: "checkAuth", ProviderID: "p1"}, {Op: "checkAuth", ProviderID: "missing"},
		{Op: "getAvailable", ProviderID: ""}, {Op: "getAvailable", ProviderID: "p1"}, {Op: "getAvailable", ProviderID: "missing"},
		{Op: "getAvailableOfChat", ProviderID: ""}, {Op: "getAllAvailable", ProviderID: "p1"},
	} {
		for _, signal := range []string{"none", "aborted", "abortedWithReason"} {
			cases = append(cases, operationOptionsCase{Op: c.Op, ProviderID: c.ProviderID, Signal: signal})
		}
		if c.ProviderID != "missing" {
			for _, signal := range []string{"abortsWhileBlocked", "abortsWhileBlockedWithReason"} {
				cases = append(cases, operationOptionsCase{Op: c.Op, ProviderID: c.ProviderID, Signal: signal, Blocked: true})
			}
		}
	}
	for _, op := range []string{"logout", "storeRead", "storeWrite", "storeDelete"} {
		for _, signal := range []string{"none", "aborted", "abortedWithReason"} {
			cases = append(cases, operationOptionsCase{Op: op, ProviderID: "p1", Signal: signal})
		}
	}
	return cases
}

func TestOperationOptionsMatchPiOnAMatrix(t *testing.T) {
	cases := operationOptionsMatrix()
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/operation_options_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var pi []operationOptionsResult
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(cases) {
		t.Fatalf("oracle output: %v", err)
	}
	for i, c := range cases {
		got := runOperationOptionsCase(t, c)
		want := pi[i]
		name := c.Op + "/" + c.ProviderID + "/" + c.Signal
		if got.Outcome != want.Outcome {
			t.Errorf("%s: PiG %s (%s), Pi %s (%s: %s)", name, got.Outcome, got.Message, want.Outcome, want.Name, want.Message)
			continue
		}
		if got.Outcome == "rejected" && (got.Name != want.Name || got.Message != want.Message && want.Name != "AbortError") {
			t.Errorf("%s: rejected with %s %q, Pi %s %q", name, got.Name, got.Message, want.Name, want.Message)
		}
		if got.Outcome == "value" && !jsonEqual(t, got.Value, want.Value) {
			t.Errorf("%s: value %s, Pi %s", name, got.Value, want.Value)
		}
		if got.CredentialAfter != want.CredentialAfter || !jsonEqual(t, got.StoreAfter, want.StoreAfter) {
			t.Errorf("%s: credential kept %v store %s, Pi %v %s", name, got.CredentialAfter, got.StoreAfter, want.CredentialAfter, want.StoreAfter)
		}
	}
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// runOperationOptionsCase is the Go side of one oracle case; its result uses Pi's names: an AbortError for a signal cancelled with
// no cause (context.Canceled), the cause's message otherwise.
func runOperationOptionsCase(t *testing.T, c operationOptionsCase) operationOptionsResult {
	t.Helper()
	ctx := t.Context()
	gate, started := make(chan struct{}), make(chan struct{}, 1)
	credentials := NewInMemoryCredentialStore()
	if _, err := credentials.Modify(ctx, "p1", func(*Credential) (*Credential, error) { return &Credential{Type: CredentialAPIKey, Key: "stored"}, nil }); err != nil {
		t.Fatal(err)
	}
	store := NewInMemoryModelsStore()
	if err := store.Write(ctx, "p1", ModelsStoreEntry{Models: []AnyModel{}}); err != nil {
		t.Fatal(err)
	}
	models := CreateModels(CreateModelsOptions{Credentials: credentials, ModelsStore: store})
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", models: []*Model{modelsRuntimeModel("p1", "a"), modelsRuntimeModel("p1", "b")}, auth: &ProviderAuth{APIKey: &APIKeyAuth{
		Name: "Test",
		Check: func(context.Context, APIKeyAuthInput) (*AuthCheck, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			if c.Blocked {
				<-gate
			}
			return &AuthCheck{Type: CredentialAPIKey, Source: "test"}, nil
		},
		Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil },
	}}}))
	signal, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	options := AuthOperationOptions{Signal: signal}
	storeOptions := ModelsStoreOperationOptions{Signal: signal}
	switch c.Signal {
	case "none":
		options, storeOptions = AuthOperationOptions{}, ModelsStoreOperationOptions{}
	case "aborted":
		cancel(nil)
	case "abortedWithReason":
		cancel(errors.New("why"))
	}
	done := make(chan operationOptionsResult, 1)
	go func() {
		var value any
		var err error
		switch c.Op {
		case "checkAuth":
			var check *AuthCheck
			check, err = models.CheckAuthWith(ctx, c.ProviderID, options)
			if check != nil {
				value = map[string]any{"source": check.Source, "type": string(check.Type)}
			}
		case "getAvailable":
			var list []*Model
			list, err = models.GetAvailableWith(ctx, c.ProviderID, options)
			value = modelIDs(list)
		case "getAvailableOfChat":
			var list []AnyModel
			list, err = models.GetAvailableOfTypeWith(ctx, ModelTypeChat, c.ProviderID, options)
			value = modelIDs(list)
		case "getAllAvailable":
			var list []AnyModel
			list, err = models.GetAllAvailableWith(ctx, c.ProviderID, options)
			value = modelIDs(list)
		case "logout":
			err = models.LogoutWith(ctx, "p1", options)
		case "storeRead":
			var entry *ModelsStoreEntry
			entry, err = store.ReadWith(ctx, "p1", storeOptions)
			if entry != nil {
				value = map[string]any{"models": []any{}}
			}
		case "storeWrite":
			err = store.WriteWith(ctx, "p1", ModelsStoreEntry{Models: []AnyModel{}, ETag: "new"}, storeOptions)
		case "storeDelete":
			err = store.DeleteWith(ctx, "p1", storeOptions)
		}
		result := operationOptionsResult{Outcome: "value"}
		if err != nil {
			result = operationOptionsResult{Outcome: "rejected", Name: "Error", Message: err.Error()}
			if errors.Is(err, context.Canceled) {
				result.Name, result.Message = "AbortError", "This operation was aborted"
			}
		} else {
			result.Value, _ = json.Marshal(value)
		}
		done <- result
	}()
	switch c.Signal {
	case "abortsWhileBlocked":
		<-started
		cancel(nil)
	case "abortsWhileBlockedWithReason":
		<-started
		cancel(errors.New("why"))
	}
	var result operationOptionsResult
	select {
	case result = <-done:
	case <-time.After(operationOptionsCaseTimeout):
		close(gate)
		t.Fatalf("%s/%s/%s: the operation did not settle within %v (Pi rejects as soon as the signal aborts)", c.Op, c.ProviderID, c.Signal, operationOptionsCaseTimeout)
	}
	close(gate)
	credential, readErr := credentials.Read(ctx, "p1")
	result.CredentialAfter = readErr == nil && credential != nil
	entry, _ := store.Read(ctx, "p1")
	if entry == nil {
		result.StoreAfter = json.RawMessage("null")
	} else {
		after := map[string]any{"models": []any{}}
		if entry.ETag != "" {
			after["etag"] = entry.ETag
		}
		result.StoreAfter, _ = json.Marshal(after)
	}
	return result
}
