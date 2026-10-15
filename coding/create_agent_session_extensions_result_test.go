package coding

import (
	"context"
	"path/filepath"
	"testing"
)

// sdk.ts:480-486: createAgentSession returns `extensionsResult = resourceLoader.getExtensions()`, the load of the services'
// resource loader, beside the Session and the model fallback message.
func TestCreateAgentSessionFromServicesReturnsTheLoadedExtensions(t *testing.T) {
	cwd, agentDir := t.TempDir(), filepath.Join(t.TempDir(), "agent")
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: cwd, AgentDir: agentDir,
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{
			NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
			LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
				return LoadExtensionsResult{Errors: []ExtensionLoadError{{Path: "ext.ts", Error: "boom"}}}, nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	result, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if got := result.ExtensionsResult.Errors; len(got) != 1 || got[0].Path != "ext.ts" || got[0].Error != "boom" {
		t.Fatalf("CreateAgentSessionResult.ExtensionsResult.Errors = %+v, want the resource loader's [{ext.ts boom}]", got)
	}
}
