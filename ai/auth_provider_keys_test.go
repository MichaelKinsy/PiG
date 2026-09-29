package ai

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Pi auth-storage.ts:363,464,477-488 retains JSON.parse key identity through Object.entries and JSON.stringify, including lone UTF-16 units.
func TestPairReviewCredentialKeysRetainDistinctUTF16Values(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"\ud800":{"type":"api_key","key":"surrogate"},"\ufffd":{"type":"api_key","key":"replacement"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{jsstring.FromUTF16([]uint16{0xd800}): "surrogate", "\ufffd": "replacement"}
	if len(infos) != len(want) {
		t.Errorf("distinct UTF-16 credential keys collapsed: list=%+v; want %d providers", infos, len(want))
	}
	if err := store.Delete(t.Context(), "absent"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"surrogate"`) || !strings.Contains(string(data), `"replacement"`) {
		t.Errorf("no-op deletion lost a credential: %s", data)
	}
	assertCredentialProviderKeys(t, NewReadOnlyAuthStorage(path), want)
}

func TestAuthStorageProviderKeyUTF16Identity(t *testing.T) {
	high := jsstring.FromUTF16([]uint16{0xd800})
	low := jsstring.FromUTF16([]uint16{0xdc00})
	raw := `{"\uD800":{"type":"api_key","key":"high"},"\ufffd":{"type":"api_key","key":"replacement"},"\uDC00":{"type":"api_key","key":"low"},"\ud800\udc00":{"type":"api_key","key":"paired"},"\\ud800":{"type":"api_key","key":"literal"},"a\ud800b":{"type":"api_key","key":"embedded"},"ordinary":{"type":"api_key","key":"plain"}}`
	want := map[string]string{
		high: "high", "\ufffd": "replacement", low: "low", "\U00010000": "paired",
		`\ud800`: "literal", "a" + high + "b": "embedded", "ordinary": "plain",
	}
	for _, operation := range []string{"read-only", "absent-delete", "modify", "set", "update", "delete-one"} {
		t.Run(operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if operation == "read-only" {
				assertCredentialProviderKeys(t, NewReadOnlyAuthStorage(path), want)
				return
			}
			store, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialProviderKeys(t, store, want)
			expected := make(map[string]string, len(want))
			maps.Copy(expected, want)
			switch operation {
			case "absent-delete":
				err = store.Delete(t.Context(), "absent")
			case "modify":
				_, err = store.Modify(t.Context(), high, func(current *Credential) (*Credential, error) {
					if current == nil || current.Key != "high" {
						t.Fatalf("modifier received %#v, want high-surrogate credential", current)
					}
					return &Credential{Type: CredentialAPIKey, Key: "modified"}, nil
				})
				expected[high] = "modified"
			case "set":
				err = store.Set(high, Credential{Type: CredentialAPIKey, Key: "set"})
				expected[high] = "set"
			case "update":
				err = store.Update(low, func(current Credential, exists bool) (Credential, error) {
					if !exists || current.Key != "low" {
						t.Fatalf("updater received %#v, exists=%v", current, exists)
					}
					return Credential{Type: CredentialAPIKey, Key: "updated"}, nil
				})
				expected[low] = "updated"
			case "delete-one":
				err = store.Delete(t.Context(), high)
				delete(expected, high)
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !utf8.Valid(data) || !json.Valid(data) {
				t.Fatalf("disk is not valid UTF-8 JSON: %q, %v", data, err)
			}
			assertCredentialProviderKeys(t, NewReadOnlyAuthStorage(path), expected)
			reopened, err := NewAuthStorage(path)
			if err != nil {
				t.Fatal(err)
			}
			assertCredentialProviderKeys(t, reopened, expected)
		})
	}
}

func TestAuthStorageAddsSurrogateProviderWithoutCorruptingReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	high := jsstring.FromUTF16([]uint16{0xd800})
	want := map[string]string{high: "new-high", "\ufffd": "replacement"}
	for _, id := range []string{"\ufffd", high} {
		if _, err := store.Modify(t.Context(), id, func(*Credential) (*Credential, error) {
			return &Credential{Type: CredentialAPIKey, Key: want[id]}, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertCredentialProviderKeys(t, NewReadOnlyAuthStorage(path), want)
}

func BenchmarkCredentialProviderKeyRewrite(b *testing.B) {
	path := filepath.Join(b.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"\ud800":{"type":"api_key","key":"surrogate"},"\ufffd":{"type":"api_key","key":"replacement"}}`), 0o600); err != nil {
		b.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := store.Delete(b.Context(), "absent"); err != nil {
			b.Fatal(err)
		}
		if _, err := NewReadOnlyAuthStorage(path).List(b.Context()); err != nil {
			b.Fatal(err)
		}
	}
}

func assertCredentialProviderKeys(t *testing.T, store CredentialStore, want map[string]string) {
	t.Helper()
	infos, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != len(want) {
		t.Errorf("provider count=%d want=%d: %#v", len(infos), len(want), infos)
	}
	seen := make(map[string]bool)
	for _, info := range infos {
		if _, ok := want[info.ProviderID]; !ok || seen[info.ProviderID] || info.Type != CredentialAPIKey {
			t.Errorf("unexpected credential metadata: id units=%x type=%q", jsstring.ToUTF16(info.ProviderID), info.Type)
		}
		seen[info.ProviderID] = true
	}
	for id, key := range want {
		credential, err := store.Read(t.Context(), id)
		if err != nil || credential == nil || credential.Key != key {
			t.Errorf("provider units=%x credential=%#v error=%v, want key %q", jsstring.ToUTF16(id), credential, err, key)
		}
	}
}
