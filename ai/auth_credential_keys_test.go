package ai

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestCredentialStorageUTF16ProviderIDsAcrossReadersAndWrites(t *testing.T) {
	ids := []string{jsstring.FromUTF16([]uint16{0xd800}), "\ufffd", jsstring.FromUTF16([]uint16{0xdc00}), "\U00010000"}
	values := []string{"high", "replacement", "low", "pair"}
	path := filepath.Join(t.TempDir(), "auth.json")
	raw := `{"\ud800":{"type":"api_key","key":"high"},"\ufffd":{"type":"api_key","key":"replacement"},"\udc00":{"type":"api_key","key":"low"},"\ud800\udc00":{"type":"api_key","key":"pair"}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, storage CredentialStore) {
		t.Helper()
		infos, err := storage.List(t.Context())
		want := make([]CredentialInfo, len(ids))
		for i, id := range ids {
			want[i] = CredentialInfo{ProviderID: id, Type: CredentialAPIKey}
			credential, readErr := storage.Read(t.Context(), id)
			if readErr != nil || credential == nil || credential.Key != values[i] {
				t.Fatalf("provider units=%x: credential=%#v error=%v", jsstring.ToUTF16(id), credential, readErr)
			}
		}
		if err != nil || !reflect.DeepEqual(infos, want) {
			t.Fatalf("provider order=%#v error=%v want=%#v", infos, err, want)
		}
	}
	t.Run("file", func(t *testing.T) { check(t, store) })
	t.Run("read-only", func(t *testing.T) { check(t, NewReadOnlyAuthStorage(path)) })
	if err := store.Delete(t.Context(), "absent"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		t.Fatalf("persisted credentials are not UTF-8 JSON: %q, %v", data, err)
	}
	t.Run("read-only after write", func(t *testing.T) { check(t, NewReadOnlyAuthStorage(path)) })
	t.Run("memory", func(t *testing.T) {
		memory := NewInMemoryAuthStorage(nil)
		for i, id := range ids {
			if _, err := memory.Modify(t.Context(), id, func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialAPIKey, Key: values[i]}, nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := memory.Delete(t.Context(), "absent"); err != nil {
			t.Fatal(err)
		}
		check(t, memory)
	})
}
