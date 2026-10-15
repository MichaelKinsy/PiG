package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func registryProviderWithKey(key string) map[string]any {
	return map[string]any{"baseUrl": "https://example.com/v1", "apiKey": key, "api": "anthropic-messages", "models": []any{map[string]any{"id": "test-model", "name": "Test Model", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 100000, "maxTokens": 8000}}}
}
func registryTestServices(t *testing.T, dir string, providers map[string]any) *AgentSessionServices {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	data, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

func TestModelRegistryAPIKeysUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		env       map[string]string
		want      *string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1535
		{name: "apiKey with ! prefix executes command and uses stdout", key: "!echo test-api-key-from-command", want: new("test-api-key-from-command")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1546
		{name: "apiKey with ! prefix trims whitespace from command output", key: "!echo '  spaced-key  '", want: new("spaced-key")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1557
		{name: "apiKey with ! prefix handles multiline output (uses trimmed result)", key: "!printf 'line1\\nline2'", want: new("line1\nline2")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1568
		{name: "apiKey with ! prefix returns undefined on command failure", key: "!exit 1"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1579
		{name: "apiKey with ! prefix returns undefined on nonexistent command", key: "!nonexistent-command-12345"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1590
		{name: "apiKey with ! prefix returns undefined on empty output", key: "!printf ''"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1601
		{name: "apiKey with $ prefix resolves to env value", key: "$TEST_API_KEY_12345", env: map[string]string{"TEST_API_KEY_12345": "env-api-key-value"}, want: new("env-api-key-value")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1623
		{name: "apiKey with braced env syntax resolves to env value", key: "${TEST_BRACED_API_KEY_12345}", env: map[string]string{"TEST_BRACED_API_KEY_12345": "braced-env-api-key-value"}, want: new("braced-env-api-key-value")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1646
		{name: "apiKey interpolates braced env references inside literals", key: "${TEST_INTERPOLATED_PART_A_12345}_${TEST_INTERPOLATED_PART_B_12345}", env: map[string]string{"TEST_INTERPOLATED_PART_A_12345": "left", "TEST_INTERPOLATED_PART_B_12345": "right"}, want: new("left_right")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1678
		{name: "apiKey with $$ prefix escapes a leading dollar", key: "$$TEST_API_KEY_12345", want: new("$TEST_API_KEY_12345")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1689
		{name: "apiKey with $! escapes a literal bang and still interpolates later env refs", key: "$!literal-$TEST_API_KEY_12345", env: map[string]string{"TEST_API_KEY_12345": "env-api-key-value"}, want: new("!literal-env-api-key-value")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1711
		{name: "plain apiKey is used directly even when it matches an env var", key: "TEST_API_KEY_12345", env: map[string]string{"TEST_API_KEY_12345": "env-api-key-value"}, want: new("TEST_API_KEY_12345")}, // gitleaks:allow (test fixture, not a credential)
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1733
		{name: "apiKey as literal value is used directly when not an env var", key: "literal_api_key_value", want: new("literal_api_key_value")},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1747
		{name: "apiKey command can use shell features like pipes", key: "!echo 'hello world' | tr ' ' '-'", want: new("hello-world")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			registry := registryTestServices(t, "", map[string]any{"custom-provider": registryProviderWithKey(tc.key)}).Registry()
			if actual := registry.GetAPIKeyForProvider(t.Context(), "custom-provider"); !reflect.DeepEqual(actual, tc.want) {
				t.Fatalf("key=%v, want %v", actual, tc.want)
			}
		})
	}
}

func TestModelRegistryCommandLookupCountsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name              string
		lookups           int
		instances, failed bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1759
		{name: "command is executed on every provider lookup", lookups: 3},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1778
		{name: "commands are re-executed across registry instances", lookups: 2, instances: true},
		// .upstream/v0.87.1/packages/coding-agent/test/model-registry.test.ts:1813
		{name: "failed commands are retried", lookups: 2, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			counter := filepath.Join(dir, "counter")
			if err := os.WriteFile(counter, []byte("0"), 0o600); err != nil {
				t.Fatal(err)
			}
			end := `echo "key-value"`
			if tc.failed {
				end = "exit 1"
			}
			key := `!sh -c 'count=$(cat "` + counter + `"); echo $((count + 1)) > "` + counter + `"; ` + end + `'`
			providers := map[string]any{"custom-provider": registryProviderWithKey(key)}
			registry := registryTestServices(t, dir, providers).Registry()
			for i := range tc.lookups {
				if tc.instances && i > 0 {
					registry = registryTestServices(t, dir, providers).Registry()
				}
				value := registry.GetAPIKeyForProvider(t.Context(), "custom-provider")
				if tc.failed && value != nil {
					t.Errorf("failed command returned %q", *value)
				}
			}
			data, err := os.ReadFile(counter)
			if err != nil {
				t.Fatal(err)
			}
			want := map[int]string{2: "2", 3: "3"}[tc.lookups]
			if strings.TrimSpace(string(data)) != want {
				t.Fatalf("command executions=%q, want %q", data, want)
			}
		})
	}
}
