package ai

import "testing"

// compat.ts azureOpenAIResponsesApi().stream(model, context, options) is the azure-openai-responses API: it POSTs to the Azure endpoint
// <azureBaseUrl>/responses with the api-version query and the deployment name as the request model (azure-openai-responses.ts,
// azure-openai-config.ts), not to the plain OpenAI /v1/responses route the openai-responses API uses.
func TestAzureOpenAIResponsesAPIStreamsThroughTheAzureEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "")
	t.Setenv("AZURE_OPENAI_API_VERSION", "")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "")
	server := newAzureWireServer(t, azureResponsesDoneSSE)
	model := sharedModel("gpt-4o-mini", "azure-openai-responses", APIAzureOpenAIResponses, false, []string{"text"}, nil)
	stream, err := AzureOpenAIResponsesAPI().Stream(t.Context(), model, newTranscriptContext([]Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}),
		StreamOptions{APIKey: "azure-key", AzureBaseURL: server.URL + "/openai/v1", AzureAPIVersion: "2025-03-01", AzureDeploymentName: "my-deployment"})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v", result)
	}
	got := server.last(t)
	if got.path != "/openai/v1/responses" || got.query != "api-version=2025-03-01" || got.model != "my-deployment" {
		t.Fatalf("request = %+v, want POST /openai/v1/responses?api-version=2025-03-01 for deployment my-deployment", got)
	}
}
