package ai

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func oauthAuthCatalogProvider(t *testing.T, id string) *ModelsProvider {
	t.Helper()
	auth, err := BuiltinProviderAuth(id)
	if err != nil {
		t.Fatal(err)
	}
	var models []*Model
	for _, m := range ListModels(id) {
		models = append(models, m.ToModel())
	}
	return CreateProvider(CreateProviderOptions{ID: id, Models: models, Auth: auth})
}

// Ports packages/ai/test/oauth-auth.test.ts:21,30,37,42,47,53,58,64,83,101,130,148.
func TestOAuthAuthAdaptersUpstream(t *testing.T) {
	t.Run("keeps the extension OAuth barrel free of built-in flow implementations", func(t *testing.T) {
		// The native SDK package is the Go extension barrel. Inspect all its public declarations, not the linked host's implementation package.
		files, err := filepath.Glob("../extensions/sdk/*.go")
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatal("SDK sources missing")
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Recv == nil && (d.Name.Name == "LoginAnthropic" || d.Name.Name == "AnthropicOAuth") {
						t.Errorf("built-in flow exported in %s", path)
					}
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						if v, ok := spec.(*ast.ValueSpec); ok {
							for _, name := range v.Names {
								if name.Name == "AnthropicOAuth" {
									t.Errorf("built-in adapter exported in %s", path)
								}
							}
						}
					}
				}
			}
		}
	})
	t.Run("identifies only subscription-backed OAuth flows as subscriptions", func(t *testing.T) {
		for _, id := range []string{"anthropic", "openai-codex", "github-copilot", "kimi-coding", "xai", "openrouter"} {
			method, ok := OAuthProviderAuth(id)
			if !ok || method.IsSubscription != (id != "openrouter") {
				t.Errorf("%s method=%+v present=%v", id, method, ok)
			}
		}
	})
	for _, id := range []string{"anthropic", "openai-codex", "xai", "openrouter"} {
		t.Run(id+" toAuth derives the api key from the access token", func(t *testing.T) {
			method, ok := OAuthProviderAuth(id)
			if !ok {
				t.Fatal("missing OAuth method")
			}
			cred := Credential{Type: CredentialOAuth, Access: "token", Refresh: "r"}
			if id == "openrouter" {
				cred.Refresh = ""
				cred.Expires = 9007199254740991
			}
			auth, err := method.ToAuth(cred)
			if err != nil || !reflect.DeepEqual(auth, ModelAuth{APIKey: "token"}) {
				t.Fatalf("auth=%#v %v", auth, err)
			}
			if id == "openrouter" {
				fresh, err := method.Refresh(t.Context(), cred)
				if err != nil || !reflect.DeepEqual(fresh, cred) {
					t.Fatalf("refresh=%#v %v", fresh, err)
				}
			}
		})
	}
	t.Run("github-copilot toAuth derives baseUrl from the token proxy endpoint", func(t *testing.T) {
		access := "tid=abc;exp=123;proxy-ep=proxy.enterprise.example;rest"
		method, _ := OAuthProviderAuth("github-copilot")
		auth, err := method.ToAuth(Credential{Type: CredentialOAuth, Access: access, Refresh: "r"})
		if err != nil || !reflect.DeepEqual(auth, ModelAuth{APIKey: access, BaseURL: "https://api.enterprise.example"}) {
			t.Fatalf("auth=%#v %v", auth, err)
		}
	})
	t.Run("github-copilot toAuth falls back to the enterprise domain, then the individual endpoint", func(t *testing.T) {
		method, _ := OAuthProviderAuth("github-copilot")
		for _, tc := range []struct{ domain, want string }{{"https://company.ghe.com", "https://copilot-api.company.ghe.com"}, {"", "https://api.individual.githubcopilot.com"}} {
			auth, err := method.ToAuth(Credential{Type: CredentialOAuth, Access: "no-proxy-ep", Refresh: "r", EnterpriseDomain: tc.domain})
			if err != nil || auth.BaseURL != tc.want {
				t.Fatalf("auth=%#v %v", auth, err)
			}
		}
	})
	t.Run("anthropic refresh exchanges the refresh token and returns a typed credential", func(t *testing.T) {
		withMockCopilotClient(t, func(*http.Request) (*http.Response, error) {
			return metaJSONResponse(200, map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600}), nil
		})
		method, _ := OAuthProviderAuth("anthropic")
		cred, err := method.Refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old", Refresh: "old-r"})
		if err != nil || cred.Type != CredentialOAuth || cred.Access != "new-access" || cred.Refresh != "new-refresh" || cred.Expires <= time.Now().UnixMilli() {
			t.Fatalf("refresh=%#v %v", cred, err)
		}
	})
	t.Run("github-copilot refresh preserves the enterprise domain", func(t *testing.T) {
		var urls []string
		withMockCopilotClient(t, func(r *http.Request) (*http.Response, error) {
			urls = append(urls, r.URL.String())
			if strings.HasSuffix(r.URL.Path, "/models") {
				return metaJSONResponse(200, map[string]any{"data": []any{}}), nil
			}
			return metaJSONResponse(200, map[string]any{"token": "new-token", "expires_at": 9999999999}), nil
		})
		method, _ := OAuthProviderAuth("github-copilot")
		cred, err := method.Refresh(t.Context(), Credential{Type: CredentialOAuth, Access: "old", Refresh: "gh-token", EnterpriseDomain: "company.ghe.com"})
		if err != nil || cred.Access != "new-token" || cred.EnterpriseDomain != "company.ghe.com" || len(urls) == 0 || !strings.Contains(urls[0], "api.company.ghe.com") {
			t.Fatalf("refresh=%#v %v urls=%v", cred, err, urls)
		}
	})
	for _, tc := range []struct{ id, access, url string }{{"anthropic", "oauth-access-token", ""}, {"github-copilot", "tid=abc;exp=123;proxy-ep=proxy.business.githubcopilot.com;rest", "https://api.business.githubcopilot.com"}} {
		t.Run("Models.getAuth "+tc.id, func(t *testing.T) {
			store := NewInMemoryCredentialStore()
			defer store.operations.Wait()
			_, err := store.Modify(t.Context(), tc.id, func(*Credential) (*Credential, error) {
				return &Credential{Type: CredentialOAuth, Access: tc.access, Refresh: "r", Expires: time.Now().Add(10 * time.Minute).UnixMilli()}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			models := CreateModels(CreateModelsOptions{Credentials: store})
			models.SetProvider(oauthAuthCatalogProvider(t, tc.id))
			defer models.Close()
			result, err := models.GetAuth(t.Context(), tc.id)
			if err != nil || result == nil || result.Auth.APIKey != tc.access || result.Auth.BaseURL != tc.url || (tc.id == "anthropic" && result.Source != "OAuth") {
				t.Fatalf("auth=%#v %v", result, err)
			}
		})
	}
}
