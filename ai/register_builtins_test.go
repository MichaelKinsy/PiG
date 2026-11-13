package ai

import "testing"

func TestLookupBuiltInProvider(t *testing.T) {
	// Verify every registered built-in API can construct a provider.
	for _, api := range []API{
		APIOpenAICompletions,
		APIMistralConversations,
		APIOpenAIResponses,
		APIAnthropicMessages,
		APIGoogleGenerativeAI,
		// providers:
		APIAzureOpenAIResponses,
		APIOpenAICodexResponses,
		APIGoogleVertex,
		// Bedrock stub (returns error on Stream but constructs successfully):
		APIBedrockConverseStream,
	} {
		f, ok := LookupBuiltInProvider(api)
		if !BuiltAPI(api) {
			// A build with this API's pig_strip tag registers no factory for it.
			if ok {
				t.Errorf("LookupBuiltInProvider(%q) found a factory for an API this build strips", api)
			}
			continue
		}
		if !ok {
			t.Errorf("LookupBuiltInProvider(%q) not found", api)
			continue
		}
		p := f("key", "model", "")
		if p == nil {
			t.Errorf("factory for %q returned nil", api)
		}
		_ = p.Close()
	}
}

func TestLookupBuiltInProvider_NotRegistered(t *testing.T) {
	_, ok := LookupBuiltInProvider("nonexistent")
	if ok {
		t.Error("expected unknown API to not be found")
	}
}

// TestBedrockProviderConstructs verifies that the Bedrock provider can
// be constructed via the built-in registry. We do NOT call Stream here
// because that requires real AWS credentials and reaches a network
// endpoint; live behaviour is covered by AWS SDK integration tests in
// the user's environment.
func TestBedrockProviderConstructs(t *testing.T) {
	f, ok := LookupBuiltInProvider(APIBedrockConverseStream)
	if !BuiltAPI(APIBedrockConverseStream) {
		// A build with pig_strip_bedrock_converse_stream registers no Bedrock factory.
		if ok {
			t.Fatal("Bedrock provider registered in a build that strips bedrock-converse-stream")
		}
		return
	}
	if !ok {
		t.Fatal("Bedrock provider not registered")
	}
	p := f("", "anthropic.claude-3-5-haiku-20241022-v1:0", "")
	if p == nil {
		t.Fatal("Bedrock factory returned nil")
	}
	if p.ID() != "amazon-bedrock" {
		t.Errorf("ID() = %q, want amazon-bedrock", p.ID())
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}

func TestRegisteredAPIs(t *testing.T) {
	apis := RegisteredAPIs()
	// 8 built-in providers currently registered (including Bedrock stub), less the APIs this build strips.
	if want := 8 - len(BuiltStrippedAPIs()); len(apis) < want {
		t.Errorf("expected at least %d registered APIs, got %d", want, len(apis))
	}
	for _, api := range apis {
		if api == API("google-gemini-cli") {
			t.Fatalf("RegisteredAPIs unexpectedly includes removed built-in API %q", api)
		}
	}
}
