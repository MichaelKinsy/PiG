package ai

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type azureURLOracleRow struct {
	Name         string `json:"name"`
	Resolve      bool   `json:"resolve"`
	ResourceName string `json:"resourceName,omitempty"`
	EnvBaseURL   string `json:"envBaseURL,omitempty"`
	ModelBaseURL string `json:"modelBaseURL,omitempty"`
	BaseURL      string `json:"baseURL,omitempty"`
	APIVersion   string `json:"apiVersion,omitempty"`
}

type azureURLOracleResult struct {
	ResolvedBaseURL string  `json:"resolvedBaseURL"`
	APIVersion      string  `json:"apiVersion"`
	URL             *string `json:"url"`
	Creds           bool    `json:"creds"`
	Error           *string `json:"error"`
}

func azureURLOraclePassingRows() []azureURLOracleRow {
	return []azureURLOracleRow{
		// (a) resource name only (AZURE_OPENAI_RESOURCE_NAME) -> https://<res>.openai.azure.com/openai/v1
		{Name: "a_resource_name_only", Resolve: true, ResourceName: "res", APIVersion: "v1"},
		// (b) AZURE_OPENAI_BASE_URL plain, without and with trailing slash (Azure host and non-Azure host)
		{Name: "b1_env_base_azure_plain_no_slash", Resolve: true, EnvBaseURL: "https://res.openai.azure.com", APIVersion: "v1"},
		{Name: "b2_env_base_azure_plain_slash", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/", APIVersion: "v1"},
		{Name: "b3_env_base_nonazure_plain_no_slash", Resolve: true, EnvBaseURL: "https://gw.example", APIVersion: "v1"},
		{Name: "b4_env_base_nonazure_plain_slash", Resolve: true, EnvBaseURL: "https://gw.example/", APIVersion: "v1"},
		// (c) model baseUrl from models.json plain
		{Name: "c1_model_base_azure_plain", Resolve: true, ModelBaseURL: "https://res.openai.azure.com", APIVersion: "v1"},
		{Name: "c2_model_base_nonazure_plain", Resolve: true, ModelBaseURL: "https://gw.example/openai/v1", APIVersion: "v1"},
		// (d) base ending in /openai/v1 (Azure host and non-Azure host)
		{Name: "d1_azure_openai_v1", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/v1", APIVersion: "v1"},
		{Name: "d2_azure_openai_v1_slash", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/v1/", APIVersion: "v1"},
		{Name: "d3_nonazure_openai_v1", Resolve: true, EnvBaseURL: "https://gw.example/openai/v1", APIVersion: "v1"},
		// (e) deployment-style base .../openai/deployments/<d>
		{Name: "e1_azure_deployments", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/deployments/d", APIVersion: "v1"},
		{Name: "e2_nonazure_deployments", Resolve: true, EnvBaseURL: "https://gw.example/openai/deployments/d", APIVersion: "v1"},
		// (f-b) Azure root with query: normalizeAzureBaseUrl strips query on Azure root, so Go and SDK 7.19.0 agree
		{Name: "f_b1_azure_root_q_apiver", Resolve: true, EnvBaseURL: "https://res.openai.azure.com?api-version=2024-10-21", APIVersion: "v1"},
		{Name: "f_b2_azure_root_q_foo", Resolve: true, EnvBaseURL: "https://res.openai.azure.com?foo=1", APIVersion: "v1"},
		// (g) base ending in /responses without preserved query
		{Name: "g1_azure_openai_v1_responses", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/v1/responses", APIVersion: "v1"},
		{Name: "g2_azure_openai_v1_responses_q_apiver", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/v1/responses?api-version=2024-10-21", APIVersion: "v1"},
		{Name: "g3_azure_openai_v1_responses_q_foo", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/openai/v1/responses?foo=1", APIVersion: "v1"},
		{Name: "g4_azure_other_responses", Resolve: true, EnvBaseURL: "https://res.openai.azure.com/custom/responses", APIVersion: "v1"},
		{Name: "g6_nonazure_openai_v1_responses", Resolve: true, EnvBaseURL: "https://gw.example/openai/v1/responses", APIVersion: "v1"},
	}
}

func runAzureSDKURLOracle(t *testing.T, rows []azureURLOracleRow) []azureURLOracleResult {
	t.Helper()
	payload, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", filepath.Join(root, "ai", "testdata", "azure_sdk_url_oracle.mjs"))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Azure SDK URL oracle (npm ci --ignore-scripts in ai/testdata/sdk first): %v\n%.2000s", err, &stderr)
	}
	var results []azureURLOracleResult
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(rows) {
		t.Fatalf("oracle returned %d results for %d rows", len(results), len(rows))
	}
	return results
}

func azureGoRequestURL(t *testing.T, row azureURLOracleRow) (string, string) {
	t.Helper()
	// Clear every Azure endpoint env var explicitly so the developer's shell cannot leak a real endpoint.
	t.Setenv("AZURE_OPENAI_BASE_URL", row.EnvBaseURL)
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", row.ResourceName)
	t.Setenv("AZURE_OPENAI_API_VERSION", row.APIVersion)
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "")

	user := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}})
	var seen string
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{
		APIKey:     "k",
		Model:      "m",
		ProviderID: "azure-openai-responses",
		BaseURL:    row.ModelBaseURL,
	})
	stream, err := provider.Stream(t.Context(), user, StreamOptions{Fetch: recordingFetch(&seen)})
	msg := streamErrorText(stream, err)
	return seen, msg
}

func assertAzureURLOracleRowsMatchSDK(t *testing.T, rows []azureURLOracleRow) {
	t.Helper()
	results := runAzureSDKURLOracle(t, rows)
	ran, skipped, differing := 0, 0, 0
	for i, row := range rows {
		ran++
		gotURL, gotErr := azureGoRequestURL(t, row)
		want := results[i]
		switch {
		case want.Error != nil:
			if gotURL != "" || gotErr != *want.Error {
				differing++
				t.Errorf("%s (resolved=%q): want error %q, got URL %q error %q", row.Name, want.ResolvedBaseURL, *want.Error, gotURL, gotErr)
			}
		case want.URL == nil:
			differing++
			t.Errorf("%s (resolved=%q): oracle returned neither url nor error", row.Name, want.ResolvedBaseURL)
		default:
			if gotURL != *want.URL {
				differing++
				t.Errorf("%s (resolved=%q):\n  Go      %s\n  SDK719  %s", row.Name, want.ResolvedBaseURL, gotURL, *want.URL)
			} else {
				t.Logf("%s | resolved=%s | go=%s | sdk719=%s | EQUAL", row.Name, want.ResolvedBaseURL, gotURL, *want.URL)
			}
		}
	}
	t.Logf("azure-openai-responses oracle: %d ran, %d skipped, %d differing", ran, skipped, differing)
	if differing > 0 {
		t.Fatalf("%d Azure URL oracle rows differ from openai 7.19.0", differing)
	}
}

// TestAzureResponsesRequestURLMatchesSDK compares the request URL Go's azure-openai-responses provider sends with the URL pinned openai 7.19.0 AzureOpenAI builds for the same Pi-resolved base URL and apiVersion.
func TestAzureResponsesRequestURLMatchesSDK(t *testing.T) {
	assertAzureURLOracleRowsMatchSDK(t, azureURLOraclePassingRows())
}
