package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// startSessionWithLoader starts a Session in cwd with loader as its resource loader and extensions loaded, and emits session_start for startup the way print, JSON and RPC mode bind it: the Session runs resources_discover and extends loader with the result (agent-session.ts bindExtensions). It returns the error session_start's discovery returned.
func startSessionWithLoader(t *testing.T, cwd, agentDir string, loader *cliResourceLoader, extensions []extension.Extension) error {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	runtime, err := coding.NewRuntime(coding.RuntimeOptions{Services: services, NewExtensions: extensions})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	provider := ai.NewFauxProvider(ai.FauxConfig{})
	session, err := runtime.New(coding.SessionStartOptions{
		Model:            &ai.Model{ID: "faux-1", Provider: provider, ProviderMeta: ai.ProviderMetadata{ProviderID: provider.ID()}},
		SkipBuiltinTools: true,
		SessionDir:       t.TempDir(),
		ResourceLoader:   loader,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session.EmitSessionStartTransition("startup", "")
}
