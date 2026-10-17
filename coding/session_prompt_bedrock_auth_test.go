package coding

import (
	"strings"
	"testing"
)

// Amazon Bedrock is an ordinary built-in provider in Pi: its auth (amazon-bedrock.ts bedrockAuth.resolve) needs a stored key or an AWS
// env signal, so AgentSession.prompt (agent-session.ts:2037-2050) rejects a prompt without one, and setModel's checkAuth
// (agent-session.ts:2477-2480) rejects the switch.
func TestBedrockWithoutAnAWSSignalNeedsAuthLikePi(t *testing.T) {
	for _, name := range []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	models := services.ModelRuntime().GetModels("amazon-bedrock")
	if len(models) == 0 {
		t.Fatal("no amazon-bedrock model in the catalog")
	}
	model := models[0]
	session, err := NewSession(services, SessionOptions{Model: model, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
		<-done
	})
	if err := session.Prompt(t.Context(), "hi"); err == nil || !strings.Contains(err.Error(), "No API key found for amazon-bedrock.") {
		t.Fatalf("prompt error = %v, want Pi's missing-auth error", err)
	}
	if err := session.CheckModelAuth(model); err == nil {
		t.Fatal("setModel's auth check accepted amazon-bedrock without credentials")
	}
	t.Setenv("AWS_BEARER_TOKEN_BEDROCK", "token")
	if err := session.ValidatePromptModelAuth(t.Context()); err != nil {
		t.Fatalf("an AWS env signal is Bedrock auth: %v", err)
	}
	if err := session.CheckModelAuth(model); err != nil {
		t.Fatalf("setModel's auth check with an AWS env signal: %v", err)
	}
}
