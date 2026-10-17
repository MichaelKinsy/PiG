//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
)

// Pi resolves the Bedrock proxy once, from model.baseUrl, and gives it only to the runtime client's requestHandler (bedrock-converse-stream.ts:220-231).
// IMDS and the container provider call node:http directly and SSO takes only the parent's logger, so neither uses a proxy; STS inherits the parent's
// requestHandler only when that handler is not HTTP/2, which is when Pi found a proxy.
func TestBedrockProxyIsTheBaseURLsAndOnlyTheRuntimeAndSTSUseIt(t *testing.T) {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", "AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK", "AWS_BEDROCK_SKIP_AUTH", "AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_STS"} {
		t.Setenv(name, "")
	}
	east, west := "https://bedrock-runtime.us-east-1.amazonaws.com", "https://bedrock-runtime.us-west-2.amazonaws.com/model/x/converse-stream"
	proxyFor := func(t *testing.T, client *awshttp.BuildableClient, target string) string {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, target, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		proxy := client.GetTransport().Proxy
		if proxy == nil {
			return ""
		}
		got, err := proxy(request)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			return ""
		}
		return got.String()
	}

	t.Run("the runtime proxy follows baseUrl, not the request host", func(t *testing.T) {
		for _, tc := range []struct {
			name, noProxy, want string
		}{
			{"NO_PROXY names the request host", "bedrock-runtime.us-west-2.amazonaws.com", "http://proxy.test:3128/"},
			{"NO_PROXY names the baseUrl host", "bedrock-runtime.us-east-1.amazonaws.com", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				env := ProviderEnv{"HTTPS_PROXY": "http://proxy.test:3128", "NO_PROXY": tc.noProxy, "AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-west-2"}
				_, runtime, err := loadBedrockConfig(t.Context(), east, "us.anthropic.claude-opus-4-8", StreamOptions{Env: env})
				if err != nil {
					t.Fatal(err)
				}
				if got := proxyFor(t, runtime, west); got != tc.want {
					t.Fatalf("runtime proxy for %s = %q, want %q", west, got, tc.want)
				}
			})
		}
	})

	t.Run("an empty baseUrl has no proxy", func(t *testing.T) {
		env := ProviderEnv{"HTTPS_PROXY": "http://proxy.test:3128", "AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-west-2"}
		_, runtime, err := loadBedrockConfig(t.Context(), "", "us.anthropic.claude-opus-4-8", StreamOptions{Env: env})
		if err != nil {
			t.Fatal(err)
		}
		if got := proxyFor(t, runtime, west); got != "" {
			t.Fatalf("runtime proxy = %q, want none", got)
		}
	})

	t.Run("the credential clients use no proxy", func(t *testing.T) {
		env := ProviderEnv{"HTTP_PROXY": "http://proxy.test:3128", "HTTPS_PROXY": "http://proxy.test:3128", "AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-west-2"}
		cfg, _, err := loadBedrockConfig(t.Context(), east, "us.anthropic.claude-opus-4-8", StreamOptions{Env: env})
		if err != nil {
			t.Fatal(err)
		}
		client, ok := cfg.HTTPClient.(*awshttp.BuildableClient)
		if !ok {
			t.Fatalf("cfg.HTTPClient = %T", cfg.HTTPClient)
		}
		if got := proxyFor(t, client, "http://169.254.169.254/latest/api/token"); got != "" {
			t.Fatalf("IMDS proxy = %q, want none", got)
		}
	})

	// The STS endpoint does not resolve, so an assumed role's credentials arrive only through the proxy.
	for _, tc := range []struct {
		name, noProxy string
		viaProxy      bool
	}{
		{"STS uses the runtime proxy when baseUrl has one", "", true},
		{"STS goes direct when baseUrl has no proxy", "bedrock.test.invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var hosts []string
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				hosts = append(hosts, r.Host)
				mu.Unlock()
				w.Header().Set("Content-Type", "text/xml")
				_, _ = w.Write([]byte(`<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult><Credentials><AccessKeyId>ASSUMED</AccessKeyId><SecretAccessKey>secret</SecretAccessKey><SessionToken>token</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials><AssumedRoleUser><Arn>arn:aws:sts::123456789012:assumed-role/r/s</Arn><AssumedRoleId>ID:s</AssumedRoleId></AssumedRoleUser></AssumeRoleResult></AssumeRoleResponse>`))
			}))
			defer proxy.Close()
			dir := t.TempDir()
			config := filepath.Join(dir, "config")
			if err := os.WriteFile(config, []byte("[profile role]\nrole_arn = arn:aws:iam::123456789012:role/r\nsource_profile = source\nregion = us-east-1\n\n[profile source]\naws_access_key_id = SOURCE\naws_secret_access_key = source-secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AWS_CONFIG_FILE", config)
			t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
			t.Setenv("AWS_ENDPOINT_URL_STS", "http://sts.test.invalid")
			env := ProviderEnv{"HTTP_PROXY": proxy.URL, "NO_PROXY": tc.noProxy}
			cfg, _, err := loadBedrockConfig(t.Context(), "http://bedrock.test.invalid", "us.anthropic.claude-opus-4-8", StreamOptions{Env: env, Profile: "role"})
			if err != nil {
				t.Fatal(err)
			}
			credentials, err := cfg.Credentials.Retrieve(t.Context())
			mu.Lock()
			defer mu.Unlock()
			if tc.viaProxy {
				if err != nil || credentials.AccessKeyID != "ASSUMED" || len(hosts) != 1 || hosts[0] != "sts.test.invalid" {
					t.Fatalf("credentials %q, err %v, proxied hosts %q; want the assumed role through the proxy", credentials.AccessKeyID, err, hosts)
				}
				return
			}
			if err == nil || len(hosts) != 0 {
				t.Fatalf("credentials %q, err %v, proxied hosts %q; want a direct STS call that fails to resolve", credentials.AccessKeyID, err, hosts)
			}
		})
	}
}
