package coding

import (
	"context"
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// applyLoadedExtensions finishes the extension load of a Services container the way createAgentSessionServices does after resourceLoader.reload
// (agent-session-services.ts:157-193 and applyExtensionFlagValues, :96-124): the provider, native provider and virtual model registrations the extensions
// queued on the shared runtime apply to the model registry (a failed one becomes the error diagnostic `Extension "<path>" error: <message>`), a local
// refresh follows, and the command-line flag values land in the runtime's FlagValues. A load with no shared runtime has nothing to drain or write.
func (s *AgentSessionServices) applyLoadedExtensions(ctx context.Context) {
	loaded := s.resourceLoader.GetExtensions()
	runtime := loaded.Runtime
	if runtime == nil {
		return
	}
	registry := s.registry
	release := registry.HoldRegistrationRefresh()
	runtime.BindProviderActions(extension.ProviderActions{
		RegisterProvider:              registry.RegisterExtensionProvider,
		UnregisterProvider:            registry.UnregisterProvider,
		RegisterNativeProviderCarrier: registry.RegisterNativeProvider,
		RegisterVirtualModel:          registry.RegisterVirtualModel,
		UnregisterVirtualModel:        registry.UnregisterVirtualModel,
	}, func(err *extension.ExtensionError) {
		s.diagnostics = append(s.diagnostics, AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Extension "%s" error: %s`, err.ExtensionPath, err.Error)})
	})
	release()
	s.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	values, _ := s.ApplyExtensionFlagValues(loaded.Extensions)
	if runtime.FlagValues == nil && len(values) > 0 {
		runtime.FlagValues = map[string]any{}
	}
	maps.Copy(runtime.FlagValues, values)
}
