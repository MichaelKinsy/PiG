package ai

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/configvalue"
)

func TestReadOnlyAuthStorageDoesNotCreateFilesOrRunCommands(t *testing.T) {
	executed := false
	restore := configvalue.SetExecutorForTest(func(context.Context, string) (string, bool) {
		executed = true
		return "command-output", true
	})
	t.Cleanup(restore)
	configvalue.ClearCache()
	t.Setenv("PIG_TEST_READONLY_KEY", "env-key")

	dir := t.TempDir()
	missing := filepath.Join(dir, "agent", "auth.json")
	store := NewReadOnlyAuthStorage(missing)
	credential, err := store.Read(context.Background(), "openai")
	if err != nil || credential != nil {
		t.Fatalf("Read(missing) = %v, %v; want nil, nil", credential, err)
	}
	if infos, err := store.List(context.Background()); err != nil || len(infos) != 0 {
		t.Fatalf("List(missing) = %v, %v", infos, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only store created its directory: %v", err)
	}

	path := filepath.Join(dir, "auth.json")
	writeAuthFixture(t, path, `{"cmd":{"type":"api_key","key":"!op read secret"},"env":{"type":"api_key","key":"$PIG_TEST_READONLY_KEY"},"codex":{"type":"oauth","access":"a","refresh":"r","expires":1}}`)
	store = NewReadOnlyAuthStorage(path)
	command, err := store.Read(context.Background(), "cmd")
	if err != nil || command.Key != "!op read secret" {
		t.Fatalf("Read(cmd) = %+v, %v; want unresolved command key", command, err)
	}
	if executed {
		t.Fatal("read-only store executed a configured key command")
	}
	resolved, err := store.Read(context.Background(), "env")
	if err != nil || resolved.Key != "env-key" {
		t.Fatalf("Read(env) = %+v, %v; want resolved env reference", resolved, err)
	}
	infos, err := store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Pi's Object.entries retains the file's cmd, env, codex order.
	want := []CredentialInfo{{"cmd", CredentialAPIKey}, {"env", CredentialAPIKey}, {"codex", CredentialOAuth}}
	if len(infos) != len(want) {
		t.Fatalf("List = %+v", infos)
	}
	for index := range want {
		if infos[index] != want[index] {
			t.Fatalf("List = %+v, want %+v", infos, want)
		}
	}
	if _, err := store.Modify(context.Background(), "cmd", func(*Credential) (*Credential, error) { return nil, nil }); err == nil ||
		err.Error() != "Read-only credential storage cannot modify auth.json" {
		t.Fatalf("Modify error = %v", err)
	}
}

func TestReadOnlyAuthStorageRejectsInvalidState(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"malformed", `{invalid-json`, "Failed to read auth.json: "},
		{"array", `[]`, "Invalid auth.json: expected an object"},
		{"non-string key", `{"openai":{"type":"api_key","key":1}}`, `Invalid auth.json credential for provider "openai"`},
		{"non-string env", `{"openai":{"type":"api_key","env":{"A":1}}}`, `Invalid auth.json credential for provider "openai"`},
		{"oauth without expires", `{"codex":{"type":"oauth","access":"a","refresh":"r"}}`, `Invalid auth.json credential for provider "codex"`},
		{"unknown type", `{"openai":{"type":"api","key":"k"}}`, `Invalid auth.json credential for provider "openai"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "auth.json")
			writeAuthFixture(t, path, tc.content)
			_, err := NewReadOnlyAuthStorage(path).Read(context.Background(), "openai")
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("Read error = %v, want prefix %q", err, tc.want)
			}
		})
	}
}

func TestAuthStorageModifyIsTheCredentialWritePath(t *testing.T) {
	t.Setenv("PIG_TEST_STORE_KEY", "resolved-key")
	path := filepath.Join(t.TempDir(), "auth.json")
	store, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai", Credential{Type: CredentialAPIKey, Key: "$PIG_TEST_STORE_KEY"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	read, err := store.Read(ctx, "openai")
	if err != nil || read.Key != "resolved-key" {
		t.Fatalf("Read = %+v, %v; want resolved key", read, err)
	}
	unchanged, err := store.Modify(ctx, "openai", func(current *Credential) (*Credential, error) {
		if current == nil || current.Key != "$PIG_TEST_STORE_KEY" {
			t.Fatalf("Modify saw %+v; want the raw stored credential", current)
		}
		return nil, nil
	})
	if err != nil || unchanged.Key != "$PIG_TEST_STORE_KEY" {
		t.Fatalf("Modify(nil) = %+v, %v; want the current credential", unchanged, err)
	}
	written, err := store.Modify(ctx, "codex", func(current *Credential) (*Credential, error) {
		if current != nil {
			t.Fatalf("Modify saw %+v for a missing provider", current)
		}
		return &Credential{Type: CredentialOAuth, Access: "fresh", Refresh: "r", Expires: 42}, nil
	})
	if err != nil || written.Access != "fresh" {
		t.Fatalf("Modify(write) = %+v, %v", written, err)
	}
	raw, ok, err := store.GetRaw("codex")
	if err != nil || !ok || raw.Access != "fresh" || raw.Expires != 42 {
		t.Fatalf("persisted = %+v, %v, %v", raw, ok, err)
	}
	failure := errors.New("refresh failed")
	if _, err := store.Modify(ctx, "codex", func(*Credential) (*Credential, error) { return nil, failure }); !errors.Is(err, failure) {
		t.Fatalf("Modify error = %v; want the callback error", err)
	}
	infos, err := store.List(ctx)
	if err != nil || len(infos) != 2 || infos[0].ProviderID != "openai" || infos[1].ProviderID != "codex" {
		t.Fatalf("List = %+v, %v", infos, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.Read(canceled, "openai"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read(canceled) = %v", err)
	}
}

func TestInMemoryAuthStorageModifyAndRead(t *testing.T) {
	t.Setenv("PIG_TEST_MEMORY_KEY", "memory-key")
	store := NewInMemoryAuthStorage(map[string]Credential{"openai": {Type: CredentialAPIKey, Key: "$PIG_TEST_MEMORY_KEY"}})
	ctx := context.Background()
	read, err := store.Read(ctx, "openai")
	if err != nil || read.Key != "memory-key" {
		t.Fatalf("Read = %+v, %v", read, err)
	}
	if _, err := store.Modify(ctx, "openai", func(current *Credential) (*Credential, error) {
		if current.Key != "$PIG_TEST_MEMORY_KEY" {
			t.Fatalf("Modify saw %+v; want raw", current)
		}
		return &Credential{Type: CredentialAPIKey, Key: "literal"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	read, err = store.Read(ctx, "openai")
	if err != nil || read.Key != "literal" {
		t.Fatalf("Read after Modify = %+v, %v", read, err)
	}
	if missing, err := store.Read(ctx, "absent"); err != nil || missing != nil {
		t.Fatalf("Read(absent) = %+v, %v", missing, err)
	}
}

// Upstream packages/coding-agent/src/core/auth-storage.ts:356-366,449-469 serializes seeds and committed writes and parses each mutation's input. Every mutable credential field must cross that ownership boundary.
func TestInMemoryAuthStorageCredentialIsolation(t *testing.T) {
	mutations := []struct {
		name string
		run  func(*Credential)
	}{
		{"env", func(c *Credential) { c.Env["REGION"] = "changed" }},
		{"availableModelIds", func(c *Credential) { c.AvailableModelIDs[2] = 'X' }},
		{"gatewayConfig", func(c *Credential) { c.GatewayConfig[11] = '9' }},
		{"extra value", func(c *Credential) { c.Extra["custom"][9] = '9' }},
		{"extra map", func(c *Credential) { c.Extra["new"] = json.RawMessage(`true`) }},
	}
	for _, mutation := range mutations {
		for _, boundary := range []string{"seed", "read", "modify error", "modify nil", "modify cancelled", "write input", "write result"} {
			t.Run(mutation.name+"/"+boundary, func(t *testing.T) {
				seed, want := isolationCredential(), isolationCredential()
				store := NewInMemoryAuthStorage(map[string]Credential{"oauth": seed})
				switch boundary {
				case "seed":
					mutation.run(&seed)
				case "read":
					got, err := store.Read(t.Context(), "oauth")
					if err != nil || got == nil {
						t.Fatalf("Read = %v, %v", got, err)
					}
					mutation.run(got)
				case "modify error", "modify nil", "modify cancelled":
					ctx, cancel := context.WithCancelCause(t.Context())
					defer cancel(nil)
					failure := errors.New("refresh rejected")
					got, err := store.Modify(ctx, "oauth", func(current *Credential) (*Credential, error) {
						mutation.run(current)
						switch boundary {
						case "modify error":
							return current, failure
						case "modify cancelled":
							cancel(failure)
							return current, nil
						default:
							return nil, nil
						}
					})
					if boundary == "modify nil" {
						// Upstream returns the mutated parsed value but leaves its serialized backing value unchanged.
						changed := isolationCredential()
						mutation.run(&changed)
						if err != nil || !reflect.DeepEqual(got, &changed) {
							t.Fatalf("Modify(nil) = %#v, %v; want %#v", got, err, changed)
						}
					} else if got != nil || !errors.Is(err, failure) {
						t.Fatalf("Modify = %#v, %v; want nil, %v", got, err, failure)
					}
				case "write input", "write result":
					next := isolationCredential()
					next.Access = "committed"
					want.Access = "committed"
					got, err := store.Modify(t.Context(), "oauth", func(*Credential) (*Credential, error) { return &next, nil })
					if err != nil || !reflect.DeepEqual(got, &want) {
						t.Fatalf("Modify = %#v, %v; want %#v", got, err, want)
					}
					if boundary == "write input" {
						mutation.run(&next)
					} else {
						mutation.run(got)
					}
				}
				// Read also waits for a cancelled callback to settle before checking the backing credential.
				memoryAuthRead(t, store, "oauth", &want)
			})
		}
	}
}

func isolationCredential() Credential {
	return Credential{
		Type: CredentialOAuth, Access: "expired", Refresh: "refresh-token",
		Env:               map[string]string{"REGION": "eu"},
		AvailableModelIDs: json.RawMessage(`["model"]`),
		GatewayConfig:     json.RawMessage(`{"version":1,"models":[]}`),
		Extra:             map[string]json.RawMessage{"custom": json.RawMessage(`{"value":1}`)},
	}
}

func TestCloneCredentialPreservesEmptyFields(t *testing.T) {
	for _, want := range []Credential{{}, {
		Env: map[string]string{}, Extra: map[string]json.RawMessage{"empty": {}},
		AvailableModelIDs: json.RawMessage{}, GatewayConfig: json.RawMessage{},
	}} {
		if got := cloneCredential(want); !reflect.DeepEqual(got, want) {
			t.Fatalf("clone = %#v, want %#v", got, want)
		}
	}
}

func writeAuthFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
