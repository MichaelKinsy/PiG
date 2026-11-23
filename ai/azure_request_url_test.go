package ai

import "testing"

// TestAzureResponsesRequestURL pins azureResponsesRequestURL (the pure builder) and the Azure Responses
// provider caller path to the exact URLs pinned openai 7.19.0 AzureOpenAI builds, without requiring Node.
func TestAzureResponsesRequestURL(t *testing.T) {
	// Expected URLs are copied directly from the openai 7.19.0 AzureOpenAI probe output.
	cases := []struct {
		name         string
		resolvedBase string
		apiVersion   string
		want         string
	}{
		{
			name:         "azure_openai_v1",
			resolvedBase: "https://res.openai.azure.com/openai/v1",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1/responses?api-version=v1",
		},
		{
			name:         "nonazure_plain_root",
			resolvedBase: "https://gw.example",
			apiVersion:   "v1",
			want:         "https://gw.example/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1",
			resolvedBase: "https://gw.example/openai/v1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			name:         "azure_deployments",
			resolvedBase: "https://res.openai.azure.com/openai/deployments/d",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/deployments/d/responses?api-version=v1",
		},
		{
			name:         "nonazure_deployments",
			resolvedBase: "https://gw.example/openai/deployments/d",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/deployments/d/responses?api-version=v1",
		},
		{
			name:         "nonazure_root_q_apiver",
			resolvedBase: "https://gw.example/?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/?api-version=v1",
		},
		{
			name:         "nonazure_root_q_foo",
			resolvedBase: "https://gw.example/?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "azure_openai_v1_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/v1?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1?api-version=v1",
		},
		{
			name:         "azure_openai_v1_q_foo",
			resolvedBase: "https://res.openai.azure.com/openai/v1?foo=1",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_q_apiver",
			resolvedBase: "https://gw.example/openai/v1?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_q_foo",
			resolvedBase: "https://gw.example/openai/v1?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "azure_deployments_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/deployments/d?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/deployments/d?api-version=v1",
		},
		{
			name:         "azure_deployments_q_foo",
			resolvedBase: "https://res.openai.azure.com/openai/deployments/d?foo=1",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/deployments/d?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "nonazure_deployments_q_apiver",
			resolvedBase: "https://gw.example/openai/deployments/d?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/deployments/d?api-version=v1",
		},
		{
			name:         "nonazure_deployments_q_foo",
			resolvedBase: "https://gw.example/openai/deployments/d?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/deployments/d?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "azure_other_responses",
			resolvedBase: "https://res.openai.azure.com/custom/responses",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/custom/responses/responses?api-version=v1",
		},
		{
			name:         "azure_other_responses_q_apiver",
			resolvedBase: "https://res.openai.azure.com/custom/responses?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/custom/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_responses",
			resolvedBase: "https://gw.example/openai/v1/responses",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_responses_q_apiver",
			resolvedBase: "https://gw.example/openai/v1/responses?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_responses_q_foo",
			resolvedBase: "https://gw.example/openai/v1/responses?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?foo=1%2Fresponses&api-version=v1",
		},
		{
			name:         "classic_azure_openai_responses_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/responses?api-version=v1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := azureResponsesRequestURL(AzureConfig{BaseURL: tc.resolvedBase, APIVersion: tc.apiVersion})
			if err != nil {
				t.Fatalf("azureResponsesRequestURL(%q, %q): %v", tc.resolvedBase, tc.apiVersion, err)
			}
			if got != tc.want {
				t.Fatalf("azureResponsesRequestURL(%q, %q) = %q, want %q", tc.resolvedBase, tc.apiVersion, got, tc.want)
			}
		})
	}

	// Caller path through NewAzureOpenAIResponsesProvider.Stream (no Node required).
	callerCases := []struct {
		row  azureURLOracleRow
		want string
	}{
		{
			row:  azureURLOracleRow{Name: "classic_env", EnvBaseURL: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview", APIVersion: "v1"},
			want: "https://res.openai.azure.com/openai/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "classic_model", ModelBaseURL: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview", APIVersion: "v1"},
			want: "https://res.openai.azure.com/openai/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "nonazure_responses_q_foo", EnvBaseURL: "https://gw.example/openai/v1/responses?foo=1", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses?foo=1%2Fresponses&api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "deployments_q_apiver", EnvBaseURL: "https://res.openai.azure.com/openai/deployments/d?api-version=2024-10-21", APIVersion: "v1"},
			want: "https://res.openai.azure.com/openai/deployments/d?api-version=v1",
		},
	}
	for _, tc := range callerCases {
		t.Run("caller/"+tc.row.Name, func(t *testing.T) {
			got, _ := azureGoRequestURL(t, tc.row)
			if got != tc.want {
				t.Fatalf("caller %s got %q, want %q", tc.row.Name, got, tc.want)
			}
		})
	}
}
