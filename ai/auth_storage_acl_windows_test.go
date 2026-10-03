//go:build windows

package ai

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func authFileSDDL(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor.String()
}

// Pi auth-storage.ts:24-25 applies its 0600 mode only when writeFileSync creates auth.json and rewrites an existing file in place, so administrator-managed ACLs remain intact. On Windows Node maps that mode to no DACL (libuv sets only the read-only attribute), while PiG creates the file through ownerfile.CreateNew with a protected current-user DACL (D68). Every write path must leave the DACL as it found it, both PiG's creation-time DACL and an administrator's replacement.
func TestAuthStoragePreservesExistingACL(t *testing.T) {
	cases := []struct {
		name      string
		prepare   func(t *testing.T, path string)
		ownerOnly bool
	}{
		{"owner-only DACL of a store-created file", func(*testing.T, string) {}, true},
		{"administrator-widened DACL", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			testenv.GrantOthersRead(t, path)
		}, false},
	}
	writes := map[string]func(t *testing.T, store *AuthStorage){
		"Set": func(t *testing.T, store *AuthStorage) {
			if err := store.Set("probe", Credential{Type: CredentialAPIKey, Key: "rewritten"}); err != nil {
				t.Fatal(err)
			}
		},
		"Modify": func(t *testing.T, store *AuthStorage) {
			_, err := store.Modify(t.Context(), "probe", func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialAPIKey, Key: "modified"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		},
		"Delete": func(t *testing.T, store *AuthStorage) {
			if err := store.Delete(t.Context(), "probe"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for _, tc := range cases {
		for writeName, write := range writes {
			t.Run(tc.name+"/"+writeName, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "auth.json")
				tc.prepare(t, path)
				// An administrator's file is snapshotted before the store touches it; a store-created file is snapshotted as NewAuthStorage materializes it. Comparing after the first write as well keeps a first write that replaces the file from hiding behind a later comparison of two replacement DACLs.
				var before string
				if _, err := os.Stat(path); err == nil {
					before = authFileSDDL(t, path)
				}
				store, err := NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
				if before == "" {
					before = authFileSDDL(t, path)
				}
				if err := store.Set("probe", Credential{Type: CredentialAPIKey, Key: "first"}); err != nil {
					t.Fatal(err)
				}
				if after := authFileSDDL(t, path); after != before {
					t.Fatalf("security descriptor changed by first write:\n before %s\n after  %s", before, after)
				}
				write(t, store)
				if after := authFileSDDL(t, path); after != before {
					t.Fatalf("security descriptor changed by rewrite:\n before %s\n after  %s", before, after)
				}
				ownerOnly, err := ownerfile.OwnerOnly(path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if ownerOnly != tc.ownerOnly {
					t.Fatalf("owner-only after rewrite = %v, want %v", ownerOnly, tc.ownerOnly)
				}
			})
		}
	}
}
