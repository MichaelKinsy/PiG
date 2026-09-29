package ai

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	stdjson "encoding/json"
)

type credentialExpiryOracle struct {
	Pi   string `json:"pi"`
	Now  int64  `json:"now"`
	Rows []struct {
		Raw                   *string `json:"raw"`
		Absent                bool    `json:"absent"`
		Persisted             string  `json:"persisted"`
		GetAuthRefreshes      bool    `json:"getAuthRefreshes"`
		ModelRefreshRefreshes bool    `json:"modelRefreshRefreshes"`
		MinValidityError      bool    `json:"minValidityError"`
	} `json:"rows"`
}

func loadCredentialExpiryOracle(t *testing.T) credentialExpiryOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/credential-expiry.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle credentialExpiryOracle
	if err := stdjson.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Pi != "0.87.1" || len(oracle.Rows) == 0 {
		t.Fatalf("oracle = %+v", oracle)
	}
	return oracle
}

func withNowMillis(t *testing.T, now int64) {
	t.Helper()
	previous := nowMillis
	nowMillis = func() int64 { return now }
	t.Cleanup(func() { nowMillis = previous })
}

func expiryEntry(raw *string, tail string) string {
	if raw == nil {
		return `{"type":"oauth","refresh":"r","access":"a"` + tail + `}`
	}
	return `{"type":"oauth","refresh":"r","access":"a","expires":` + *raw + tail + `}`
}

// semanticJSON decodes JSON with number literals retained, so key order is ignored but JSON.stringify number text is compared.
func semanticJSON(t *testing.T, text string) any {
	t.Helper()
	decoder := stdjson.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return value
}

// TestCredentialExpiryMatchesPi compares persistence and every Pi expiry decision against ai/testdata/credential-expiry.json, which test/parity/probes/credential-expiry.mjs records from Pi 0.87.1 (auth-storage.ts JSON round-trip, resolve.ts:136-170, models.ts:462-478).
func TestCredentialExpiryMatchesPi(t *testing.T) {
	oracle := loadCredentialExpiryOracle(t)
	withNowMillis(t, oracle.Now)
	for _, row := range oracle.Rows {
		name := "absent"
		if row.Raw != nil {
			name = *row.Raw
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(`{"p":`+expiryEntry(row.Raw, `,"meta":{"k":[1,null,""]}`)+`}`), 0o600); err != nil {
				t.Fatal(err)
			}
			storage, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storage.Modify(ctx, "p", func(current *Credential) (*Credential, error) {
				next := cloneCredential(*current)
				return &next, nil
			}); err != nil {
				t.Fatalf("modify: %v", err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var file map[string]stdjson.RawMessage
			if err := stdjson.Unmarshal(written, &file); err != nil {
				t.Fatal(err)
			}
			if got, want := semanticJSON(t, string(file["p"])), semanticJSON(t, row.Persisted); !reflect.DeepEqual(got, want) {
				t.Errorf("persisted = %s, Pi = %s", file["p"], row.Persisted)
			}

			var stored Credential
			if err := stdjson.Unmarshal([]byte(expiryEntry(row.Raw, "")), &stored); err != nil {
				t.Fatalf("decode: %v", err)
			}
			refreshed := jsonInt(oracle.Now + 3_600_000)
			if got := expiryGetAuthRefreshes(t, stored, &refreshed, nil); got != row.GetAuthRefreshes {
				t.Errorf("getAuth refreshes = %v, Pi = %v", got, row.GetAuthRefreshes)
			}
			if got := expiryModelRefreshRefreshes(t, stored); got != row.ModelRefreshRefreshes {
				t.Errorf("model refresh refreshes = %v, Pi = %v", got, row.ModelRefreshRefreshes)
			}
			one := "1"
			var old Credential
			if err := stdjson.Unmarshal([]byte(expiryEntry(&one, "")), &old); err != nil {
				t.Fatal(err)
			}
			minimum := 60_000.0
			if got := expiryGetAuthError(t, old, row.Raw, &minimum); got != row.MinValidityError {
				t.Errorf("min validity error = %v, Pi = %v", got, row.MinValidityError)
			}
		})
	}
}

// jsonInt formats an int64 as a JSON number literal for the probe's refresh value.
func jsonInt(value int64) string { return strings.TrimSpace(stdjsonNumber(value)) }

func stdjsonNumber(value int64) string {
	data, _ := stdjson.Marshal(value)
	return string(data)
}

func expiryProvider(counter *int, refreshed *string) *ModelsProvider {
	return &ModelsProvider{
		ID:   "p",
		Name: "p",
		Auth: ProviderAuth{OAuth: &OAuthAuth{
			Name: "o",
			Refresh: func(_ context.Context, credential Credential) (Credential, error) {
				*counter++
				next := cloneCredential(credential)
				next.Access = "fresh"
				if refreshed == nil {
					next.ClearExpires()
					return next, nil
				}
				var decoded Credential
				if err := stdjson.Unmarshal([]byte(expiryEntry(refreshed, "")), &decoded); err != nil {
					return Credential{}, err
				}
				next.Expires, next.expiry = decoded.Expires, decoded.expiry
				return next, nil
			},
			ToAuth: func(credential Credential) (ModelAuth, error) { return ModelAuth{APIKey: credential.Access}, nil },
		}},
		GetModels:     func() ([]*Model, error) { return nil, nil },
		RefreshModels: func(RefreshModelsContext) error { return nil },
	}
}

func expiryModels(t *testing.T, stored Credential, provider *ModelsProvider) *Models {
	t.Helper()
	store := NewInMemoryCredentialStore()
	if _, err := store.Modify(context.Background(), "p", func(*Credential) (*Credential, error) { return &stored, nil }); err != nil {
		t.Fatal(err)
	}
	models := CreateModels(CreateModelsOptions{Credentials: store})
	models.SetProvider(provider)
	return models
}

func expiryGetAuthRefreshes(t *testing.T, stored Credential, refreshed *string, minimum *float64) bool {
	t.Helper()
	counter := 0
	models := expiryModels(t, stored, expiryProvider(&counter, refreshed))
	if _, err := models.GetAuth(context.Background(), "p", AuthResolutionOverrides{MinOAuthValidityMs: minimum}); err != nil {
		t.Fatalf("GetAuth: %v", err)
	}
	return counter > 0
}

func expiryGetAuthError(t *testing.T, stored Credential, refreshed *string, minimum *float64) bool {
	t.Helper()
	counter := 0
	models := expiryModels(t, stored, expiryProvider(&counter, refreshed))
	_, err := models.GetAuth(context.Background(), "p", AuthResolutionOverrides{MinOAuthValidityMs: minimum})
	return err != nil && strings.Contains(err.Error(), "expires too soon")
}

func expiryModelRefreshRefreshes(t *testing.T, stored Credential) bool {
	t.Helper()
	counter := 0
	refreshed := jsonInt(nowMillis() + 3_600_000)
	models := expiryModels(t, stored, expiryProvider(&counter, &refreshed))
	result := models.Refresh(context.Background())
	if len(result.Errors) != 0 {
		t.Fatalf("Refresh errors: %v", result.Errors)
	}
	return counter > 0
}
