package ai

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// During a takeover another process writes the provider whose credential the interrupted modifier computed. Pi's withLockAsync writes the `next` it computed from its own stale read over the whole file (auth-storage.ts:177-189), so the interrupted holder's credential wins for that provider, and a login or logout of it that the other process made is undone. Modify writes the same provider the same way: the last writer wins, and a refreshed credential (which may hold the only copy of a rotated refresh token) is never dropped for a competing entry. It differs from Pi only for the OTHER providers: it keeps what the other process wrote, where Pi loses it. The test runs Pi's own withLockAsync through the same interleaving and compares the provider entries.
func TestAuthStorageTakeoverSameProviderLastWriterWinsLikePiUpstream(t *testing.T) {
	apiKey := func(key string) Credential { return Credential{Type: CredentialAPIKey, Key: key} }
	oauth := func(tag string) Credential {
		return Credential{Type: CredentialOAuth, Access: "access-" + tag, Refresh: "refresh-" + tag, Expires: 4102444800000}
	}
	cases := []struct {
		name    string
		seed    Credential // provider-a before the takeover
		held    Credential // what the interrupted modifier returns for provider-a
		other   *Credential
		otherOp string // "login": the other process stores other for provider-a; "logout": it removes provider-a
	}{
		{"oauth refreshed, api key login by the other process", oauth("old"), oauth("rotated"), new(apiKey("later-login")), "login"},
		{"oauth refreshed, oauth login by the other process", oauth("old"), oauth("rotated"), new(oauth("later-login")), "login"},
		{"oauth refreshed, logout by the other process", oauth("old"), oauth("rotated"), nil, "logout"},
		{"api key replaced, api key login by the other process", apiKey("old"), apiKey("rotated"), new(apiKey("later-login")), "login"},
		{"api key replaced, logout by the other process", apiKey("old"), apiKey("rotated"), nil, "logout"},
	}
	const provider, untouched, added = "provider-a", "provider-b", "provider-c"
	marshal := func(entries map[string]Credential) string {
		data, err := json.Marshal(entries)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	readEntries := func(t *testing.T, path string) map[string]any {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var entries map[string]any
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		return entries
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed := map[string]Credential{provider: tc.seed, untouched: apiKey("stable")}
			// What the other process leaves in the file: its own login or logout of provider-a, and a provider only it knows.
			otherFile := map[string]Credential{untouched: apiKey("stable"), added: apiKey("other-process")}
			if tc.otherOp == "login" {
				otherFile[provider] = *tc.other
			}
			holderFile := map[string]Credential{provider: tc.held, untouched: apiKey("stable")}

			piFile := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(piFile, []byte(marshal(seed)), 0o600); err != nil {
				t.Fatal(err)
			}
			const script = `(async () => {
  const fs = require('node:fs');
  const [root, file, holder, other] = process.argv.slice(1);
  const { FileAuthStorageBackend } = await import(require('node:url').pathToFileURL(require('node:path').join(root, 'dist/core/auth-storage.js')).href);
  await new FileAuthStorageBackend(file).withLockAsync(async () => {
    fs.rmdirSync(file + '.lock');
    new FileAuthStorageBackend(file).withLock(() => ({ result: undefined, next: other }));
    return { result: undefined, next: holder };
  });
})()`
			if out, err := exec.CommandContext(t.Context(), "node", "-e", script, piCodingAgentPackage(t), piFile, marshal(holderFile), marshal(otherFile)).CombinedOutput(); err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			piEntries := readEntries(t, piFile)

			store, path := authPortFile(t, marshal(seed))
			got, err := store.Modify(t.Context(), provider, func(*Credential) (*Credential, error) {
				if err := os.Remove(path + ".lock"); err != nil {
					return nil, err
				}
				other, err := NewAuthStorage(path)
				if err != nil {
					return nil, err
				}
				if tc.otherOp == "login" {
					err = other.Set(provider, *tc.other)
				} else {
					err = other.Delete(context.Background(), provider)
				}
				if err != nil {
					return nil, err
				}
				if err := other.Set(added, apiKey("other-process")); err != nil {
					return nil, err
				}
				held := tc.held
				return &held, nil
			})
			if err != nil || got == nil {
				t.Fatalf("Modify = %+v, %v", got, err)
			}
			entries := readEntries(t, path)
			var heldEntries map[string]any
			if err := json.Unmarshal([]byte(marshal(holderFile)), &heldEntries); err != nil {
				t.Fatal(err)
			}
			heldEntry := heldEntries[provider]
			if !reflect.DeepEqual(entries[provider], piEntries[provider]) {
				t.Errorf("%s: Modify left %v, Pi leaves %v", provider, entries[provider], piEntries[provider])
			}
			if !reflect.DeepEqual(entries[provider], heldEntry) {
				t.Errorf("%s = %v, want the interrupted holder's credential", provider, entries[provider])
			}
			if !reflect.DeepEqual(entries[untouched], piEntries[untouched]) {
				t.Errorf("%s: Modify left %v, Pi leaves %v", untouched, entries[untouched], piEntries[untouched])
			}
			// The one difference from Pi: the other process's entry for a provider the holder never touched survives.
			if _, lost := piEntries[added]; lost {
				t.Errorf("Pi kept %s: the oracle no longer loses the other process's providers", added)
			}
			if !reflect.DeepEqual(entries[added], map[string]any{"type": "api_key", "key": "other-process"}) {
				t.Errorf("%s = %v, want the other process's entry kept", added, entries[added])
			}
		})
	}
}
