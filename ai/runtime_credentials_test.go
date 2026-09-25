package ai

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRuntimeCredentialsOverlay mirrors upstream RuntimeCredentials: a
// runtime key reads ahead of the stored credential without touching
// auth.json, lists as an api_key entry, leaves Modify to the base store,
// reveals the base on removal, and Delete clears both.
func TestRuntimeCredentialsOverlay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "auth.json")
	auth, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("openai", Credential{Type: CredentialAPIKey, Key: "stored-key"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	credentials := NewRuntimeCredentials(auth)
	credentials.SetRuntimeAPIKey("openai", "runtime-key")
	credentials.SetRuntimeAPIKey("anthropic", "runtime-only")

	read, err := credentials.Read(ctx, "openai")
	if err != nil || read == nil || read.Type != CredentialAPIKey || read.Key != "runtime-key" {
		t.Fatalf("Read(openai) = %+v, %v; want the runtime key", read, err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatalf("auth.json changed:\n%s", after)
	}

	infos, err := credentials.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []CredentialInfo{{ProviderID: "openai", Type: CredentialAPIKey}, {ProviderID: "anthropic", Type: CredentialAPIKey}}
	if !reflect.DeepEqual(infos, want) {
		t.Fatalf("List = %+v, want %+v", infos, want)
	}

	if _, err := credentials.Modify(ctx, "openai", func(*Credential) (*Credential, error) {
		return &Credential{Type: CredentialAPIKey, Key: "modified-key"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if read, _ := credentials.Read(ctx, "openai"); read == nil || read.Key != "runtime-key" {
		t.Fatalf("Read after Modify = %+v; the runtime key must remain", read)
	}

	credentials.RemoveRuntimeAPIKey("openai")
	if credentials.HasRuntimeAPIKey("openai") {
		t.Fatal("runtime key survived removal")
	}
	if read, _ := credentials.Read(ctx, "openai"); read == nil || read.Key != "modified-key" {
		t.Fatalf("Read after removal = %+v; want the base credential", read)
	}

	if err := auth.Set("anthropic", Credential{Type: CredentialAPIKey, Key: "stored-anthropic"}); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Delete(ctx, "anthropic"); err != nil {
		t.Fatal(err)
	}
	if credentials.HasRuntimeAPIKey("anthropic") {
		t.Fatal("Delete kept the runtime key")
	}
	if read, _ := credentials.Read(ctx, "anthropic"); read != nil {
		t.Fatalf("Read after Delete = %+v; want nothing", read)
	}
}

// TestRuntimeCredentialsListKeepsInsertionOrder mirrors upstream's Map
// iteration: runtime-only providers list in insertion order, replacing a key
// keeps its position, and removing then re-adding moves it to the end. Base
// entries keep their order and position.
func TestRuntimeCredentialsListKeepsInsertionOrder(t *testing.T) {
	ctx := context.Background()
	base := NewInMemoryAuthStorage(map[string]Credential{"m-base": {Type: CredentialOAuth}})
	credentials := NewRuntimeCredentials(base)
	credentials.SetRuntimeAPIKey("z-provider", "z1")
	credentials.SetRuntimeAPIKey("a-provider", "a1")
	credentials.SetRuntimeAPIKey("m-base", "m1")
	credentials.SetRuntimeAPIKey("z-provider", "z2")
	ids := func() []string {
		infos, err := credentials.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, info := range infos {
			out = append(out, info.ProviderID+":"+string(info.Type))
		}
		return out
	}
	if got, want := ids(), []string{"m-base:api_key", "z-provider:api_key", "a-provider:api_key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
	credentials.RemoveRuntimeAPIKey("z-provider")
	credentials.SetRuntimeAPIKey("z-provider", "z3")
	credentials.RemoveRuntimeAPIKey("m-base")
	if got, want := ids(), []string{"m-base:oauth", "a-provider:api_key", "z-provider:api_key"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("List after remove/re-add = %v, want %v", got, want)
	}
}
