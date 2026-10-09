package ai

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// providerModule is what a pinned packages/ai/src/providers/<id>.ts createProvider() call declares.
type providerModule struct {
	file     string
	id       string
	name     string
	baseURL  string
	label    string
	envNames []string
	oauth    bool
	apis     []string
}

var (
	providerIDRe         = regexp.MustCompile(`(?m)^\t\tid: "([^"]+)",`)
	providerNameRe       = regexp.MustCompile(`(?m)^\t\tid: "[^"]+",\n\t\tname: "([^"]+)",`)
	providerBaseURLRe    = regexp.MustCompile(`(?m)^\t\tbaseUrl: "([^"]+)",`)
	providerAuthRe       = regexp.MustCompile(`envApiKeyAuth\(\s*"([^"]+)",\s*\[([^\]]*)\]`)
	providerGenericRe    = regexp.MustCompile(`export function \w+Provider\(\): Provider<([^>]+)>`)
	providerAPIKeyRe     = regexp.MustCompile(`"([a-z0-9-]+)":\s*\w+(?:Streams|Api)?\(`)
	quotedRe             = regexp.MustCompile(`"([^"]+)"`)
	createProviderCallRe = regexp.MustCompile(`createProvider(?:<[^>]*>)?\(`)
)

// readProviderModules parses each pinned provider module that calls createProvider with a literal id.
func readProviderModules(t *testing.T) []providerModule {
	t.Helper()
	files, err := filepath.Glob("../.upstream/current/packages/ai/src/providers/*.ts")
	if err != nil || len(files) == 0 {
		t.Fatalf("pinned provider modules: %v (%d files)", err, len(files))
	}
	var modules []providerModule
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		if !createProviderCallRe.MatchString(source) {
			continue
		}
		module := providerModule{file: filepath.Base(file)}
		if match := providerIDRe.FindStringSubmatch(source); match != nil {
			module.id = match[1]
		}
		if module.id == "" {
			continue
		}
		if match := providerNameRe.FindStringSubmatch(source); match != nil {
			module.name = match[1]
		}
		if match := providerBaseURLRe.FindStringSubmatch(source); match != nil {
			module.baseURL = match[1]
		}
		if match := providerAuthRe.FindStringSubmatch(source); match != nil {
			module.label = match[1]
			for _, env := range quotedRe.FindAllStringSubmatch(match[2], -1) {
				module.envNames = append(module.envNames, env[1])
			}
		}
		module.oauth = strings.Contains(source, "oauth: lazyOAuth(")
		if match := providerGenericRe.FindStringSubmatch(source); match != nil {
			for _, api := range quotedRe.FindAllStringSubmatch(match[1], -1) {
				module.apis = append(module.apis, api[1])
			}
		}
		slices.Sort(module.apis)
		modules = append(modules, module)
	}
	return modules
}

// Every built-in provider declares the id, display name, base URL, API-key auth (label and environment variables), OAuth presence and API set of its pinned providers/<id>.ts module.
func TestBuiltinProvidersMatchPinnedProviderModules(t *testing.T) {
	modules := readProviderModules(t)
	if len(modules) < 30 {
		t.Fatalf("parsed only %d provider modules", len(modules))
	}
	for _, module := range modules {
		t.Run(module.id, func(t *testing.T) {
			provider := builtinProvider(module.id)
			if provider.ID != module.id || (module.name != "" && provider.Name != module.name) {
				t.Errorf("id/name = %q/%q, upstream %q/%q (%s)", provider.ID, provider.Name, module.id, module.name, module.file)
			}
			if provider.BaseURL != module.baseURL {
				t.Errorf("baseUrl = %q, upstream %q", provider.BaseURL, module.baseURL)
			}
			if module.label != "" {
				if provider.Auth.APIKey == nil || provider.Auth.APIKey.Name != module.label {
					t.Errorf("apiKey auth = %+v, upstream envApiKeyAuth(%q)", provider.Auth.APIKey, module.label)
				} else {
					for _, env := range module.envNames {
						if !apiKeyAuthConfigured(t, provider.Auth.APIKey, func(name string) (string, bool) { return "secret", name == env }) {
							t.Errorf("API-key auth with only %s set is not configured", env)
						}
					}
					if apiKeyAuthConfigured(t, provider.Auth.APIKey, func(string) (string, bool) { return "", false }) {
						t.Errorf("API-key auth with no environment is configured")
					}
				}
			}
			if hasOAuth := provider.Auth.OAuth != nil; hasOAuth != module.oauth {
				t.Errorf("oauth present = %v, upstream %v", hasOAuth, module.oauth)
			}
			if len(module.apis) > 0 {
				models, err := provider.GetModels()
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, model := range models {
					if api := string(model.ProviderMeta.API); !slices.Contains(got, api) {
						got = append(got, api)
					}
				}
				slices.Sort(got)
				if !slices.Equal(got, module.apis) {
					t.Errorf("chat model APIs = %v, upstream Provider<%v>", got, module.apis)
				}
			}
		})
	}
}

// apiKeyAuthConfigured reports whether auth finds a credential in env: Check when the provider has one, else Resolve, as availability is checked by resolving.
func apiKeyAuthConfigured(t *testing.T, auth *APIKeyAuth, env func(string) (string, bool)) bool {
	t.Helper()
	input := APIKeyAuthInput{Ctx: AuthContext{Env: env, FileExists: func(string) bool { return false }}}
	if auth.Check != nil {
		check, err := auth.Check(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		return check != nil
	}
	result, err := auth.Resolve(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result != nil
}
