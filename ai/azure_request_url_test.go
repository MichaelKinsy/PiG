package ai

import "testing"

// TestAzureResponsesRequestURL pins azureResponsesRequestURL (the pure builder) and the Azure Responses
// provider caller path to the exact URLs pinned openai 7.59.0 AzureOpenAI builds, without requiring Node.
func TestAzureResponsesRequestURL(t *testing.T) {
	// Expected URLs are copied directly from the openai 7.59.0 AzureOpenAI probe output (azure-openai-responses.ts:169).
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
			want:         "https://gw.example/responses?api-version=v1",
		},
		{
			name:         "nonazure_root_q_foo",
			resolvedBase: "https://gw.example/?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/responses?foo=1&api-version=v1",
		},
		{
			name:         "azure_openai_v1_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/v1?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1/responses?api-version=v1",
		},
		{
			name:         "azure_openai_v1_q_foo",
			resolvedBase: "https://res.openai.azure.com/openai/v1?foo=1",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1/responses?foo=1&api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_q_apiver",
			resolvedBase: "https://gw.example/openai/v1?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_q_foo",
			resolvedBase: "https://gw.example/openai/v1?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?foo=1&api-version=v1",
		},
		{
			name:         "azure_deployments_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/deployments/d?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/deployments/d/responses?api-version=v1",
		},
		{
			name:         "azure_deployments_q_foo",
			resolvedBase: "https://res.openai.azure.com/openai/deployments/d?foo=1",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/deployments/d/responses?foo=1&api-version=v1",
		},
		{
			name:         "nonazure_deployments_q_apiver",
			resolvedBase: "https://gw.example/openai/deployments/d?api-version=2024-10-21",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/deployments/d/responses?api-version=v1",
		},
		{
			name:         "nonazure_deployments_q_foo",
			resolvedBase: "https://gw.example/openai/deployments/d?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/deployments/d/responses?foo=1&api-version=v1",
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
			want:         "https://res.openai.azure.com/custom/responses/responses?api-version=v1",
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
			want:         "https://gw.example/openai/v1/responses/responses?api-version=v1",
		},
		{
			name:         "nonazure_openai_v1_responses_q_foo",
			resolvedBase: "https://gw.example/openai/v1/responses?foo=1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses/responses?foo=1&api-version=v1",
		},
		{
			name:         "classic_azure_openai_responses_q_apiver",
			resolvedBase: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/responses/responses?api-version=v1",
		},
		{
			name:         "apiver_with_space",
			resolvedBase: "https://res.openai.azure.com/openai/v1",
			apiVersion:   "2025 04",
			want:         "https://res.openai.azure.com/openai/v1/responses?api-version=2025%2004",
		},
		{
			name:         "apiver_with_plus",
			resolvedBase: "https://res.openai.azure.com/openai/v1",
			apiVersion:   "2025+04",
			want:         "https://res.openai.azure.com/openai/v1/responses?api-version=2025%2B04",
		},
		{
			name:         "double_trailing_slash_raw_base",
			resolvedBase: "https://gw.example/openai/v1//",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1//responses?api-version=v1",
		},
		{
			name:         "uppercase_host_raw_base",
			resolvedBase: "https://GW.EXAMPLE/openai/v1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			name:         "default_port_443_raw_base",
			resolvedBase: "https://gw.example:443/openai/v1",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			name:         "fragment_azure",
			resolvedBase: "https://res.openai.azure.com/openai/v1#x",
			apiVersion:   "v1",
			want:         "https://res.openai.azure.com/openai/v1?api-version=v1#x/responses",
		},
		{
			name:         "fragment_nonazure",
			resolvedBase: "https://gw.example/openai/v1#x",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1?api-version=v1#x/responses",
		},
		{
			name:         "query_and_fragment_nonazure",
			resolvedBase: "https://gw.example/openai/v1?a=1#x",
			apiVersion:   "v1",
			want:         "https://gw.example/openai/v1/responses?a=1&api-version=v1",
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
			want: "https://res.openai.azure.com/openai/responses/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "classic_model", ModelBaseURL: "https://res.openai.azure.com/openai/responses?api-version=2025-04-01-preview", APIVersion: "v1"},
			want: "https://res.openai.azure.com/openai/responses/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "nonazure_responses_q_foo", EnvBaseURL: "https://gw.example/openai/v1/responses?foo=1", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses/responses?foo=1&api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "deployments_q_apiver", EnvBaseURL: "https://res.openai.azure.com/openai/deployments/d?api-version=2024-10-21", APIVersion: "v1"},
			want: "https://res.openai.azure.com/openai/deployments/d/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "apiver_with_space", EnvBaseURL: "https://res.openai.azure.com/openai/v1", APIVersion: "2025 04"},
			want: "https://res.openai.azure.com/openai/v1/responses?api-version=2025%2004",
		},
		{
			row:  azureURLOracleRow{Name: "apiver_with_plus", EnvBaseURL: "https://res.openai.azure.com/openai/v1", APIVersion: "2025+04"},
			want: "https://res.openai.azure.com/openai/v1/responses?api-version=2025%2B04",
		},
		{
			row:  azureURLOracleRow{Name: "double_trailing_slash_nonazure", EnvBaseURL: "https://gw.example/openai/v1//", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "uppercase_host_nonazure", EnvBaseURL: "https://GW.EXAMPLE/openai/v1", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "default_port_443_nonazure", EnvBaseURL: "https://gw.example:443/openai/v1", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses?api-version=v1",
		},
		{
			row:  azureURLOracleRow{Name: "fragment_nonazure", EnvBaseURL: "https://gw.example/openai/v1#x", APIVersion: "v1"},
			want: "https://gw.example/openai/v1?api-version=v1#x/responses",
		},
		{
			row:  azureURLOracleRow{Name: "query_and_fragment_nonazure", EnvBaseURL: "https://gw.example/openai/v1?a=1#x", APIVersion: "v1"},
			want: "https://gw.example/openai/v1/responses?a=1&api-version=v1",
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

// TestOpenAIBuildURLRepeatedBaseParamsAndAnthropicIsolation verifies openai 7.59.0 repeated baseURL
// query preservation (client.mjs:502-554) and confirms Anthropic keeps @anthropic-ai/sdk 0.129.0 buildURL
// (client.mjs:386-400) where path is appended directly before query parsing.
func TestOpenAIBuildURLRepeatedBaseParamsAndAnthropicIsolation(t *testing.T) {
	cases := []struct {
		name         string
		baseURL      string
		path         string
		defaultQuery []sdkQueryParam
		want         string
	}{
		{
			name:         "repeated_base_param_preserved_and_moved_to_end",
			baseURL:      "https://gw.example/openai/v1?foo=1&bar=x&foo=2",
			path:         "/responses",
			defaultQuery: []sdkQueryParam{{name: "api-version", value: "v1"}},
			want:         "https://gw.example/openai/v1/responses?bar=x&api-version=v1&foo=1&foo=2",
		},
		{
			name:         "repeated_base_param_overridden_by_defaultQuery",
			baseURL:      "https://gw.example/openai/v1?a=1&a=2",
			path:         "/responses",
			defaultQuery: []sdkQueryParam{{name: "a", value: "override"}, {name: "api-version", value: "v1"}},
			want:         "https://gw.example/openai/v1/responses?a=override&api-version=v1",
		},
		{
			name:    "repeated_base_param_overridden_by_pathQuery",
			baseURL: "https://gw.example/v1?foo=1&foo=2",
			path:    "/responses?foo=3",
			want:    "https://gw.example/v1/responses?foo=3",
		},
		{
			name:    "repeated_base_param_form_encodes_spaces_and_tilde",
			baseURL: "https://gw.example/openai/v1?a=b+c&a=d&x=~!",
			path:    "/responses",
			want:    "https://gw.example/openai/v1/responses?x=%7E%21&a=b+c&a=d",
		},
		{
			name:    "single_base_param_keeps_qs_encoding",
			baseURL: "https://gw.example/openai/v1?a=b+c&x=~!",
			path:    "/responses",
			want:    "https://gw.example/openai/v1/responses?a=b%20c&x=~%21",
		},
		{
			name:    "hash_only_without_query_keeps_concatenation",
			baseURL: "https://gw.example/openai/v1#frag",
			path:    "/chat/completions",
			want:    "https://gw.example/openai/v1#frag/chat/completions",
		},
		{
			name:    "hash_containing_question_mark_drops_hash",
			baseURL: "https://gw.example/openai/v1#f?q=1",
			path:    "/chat/completions",
			want:    "https://gw.example/openai/v1/chat/completions",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := openaiBuildURLWithDefaultQuery(tc.baseURL, tc.path, tc.defaultQuery...)
			if err != nil {
				t.Fatalf("openaiBuildURLWithDefaultQuery(%q, %q): %v", tc.baseURL, tc.path, err)
			}
			if got != tc.want {
				t.Fatalf("openaiBuildURLWithDefaultQuery(%q, %q) = %q, want %q", tc.baseURL, tc.path, got, tc.want)
			}
		})
	}

	// Anthropic (@anthropic-ai/sdk 0.129.0 client.mjs:386-400) does not strip baseURL query/hash before appending path.
	gotAnthropic, err := sdkBuildURL("https://proxy.example/base?foo=1", "/v1/messages?beta=true")
	if err != nil {
		t.Fatalf("sdkBuildURL: %v", err)
	}
	wantAnthropic := "https://proxy.example/base?foo=1%2Fv1%2Fmessages%3Fbeta%3Dtrue"
	if gotAnthropic != wantAnthropic {
		t.Fatalf("sdkBuildURL(anthropic) = %q, want %q", gotAnthropic, wantAnthropic)
	}
}
