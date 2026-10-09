// Ports packages/coding-agent/src/core/agent-session.ts extendResourcesFromExtensions and buildExtensionResourcePaths.

package coding

import (
	"context"

	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extendResourcesFromExtensions asks the extensions' resources_discover handlers for resource paths after session_start and adds them to the Session's resource loader, then rebuilds the system prompt (agent-session.ts extendResourcesFromExtensions). A Session whose caller owns its resources (NoResources) has no loader to extend, and the caller runs resources_discover itself.
func (s *Session) extendResourcesFromExtensions(ctx context.Context, reason string) error {
	ref := s.resourceLoader.Load()
	runner := s.currentRunner()
	if ref == nil || !ref.promptFromLoader || runner == nil || !runner.HasHandlers(icodingagent.EventResourcesDiscover) {
		return nil
	}
	discovered, err := runner.EmitResourcesDiscover(ctx, s.services.CWD(), icodingagent.ResourcesDiscoverReason(reason))
	if err != nil {
		return err
	}
	if discovered == nil || len(discovered.SkillPaths) == 0 && len(discovered.PromptPaths) == 0 && len(discovered.ThemePaths) == 0 {
		return nil
	}
	err = ref.loader.ExtendResources(ResourceExtensionPaths{
		SkillPaths:  extensionResourcePaths(discovered.SkillPaths),
		PromptPaths: extensionResourcePaths(discovered.PromptPaths),
		ThemePaths:  extensionResourcePaths(discovered.ThemePaths),
	})
	if err != nil {
		return err
	}
	s.rebuildSystemPrompt(s.ActiveToolNames())
	return nil
}

func extensionResourcePaths(entries []extension.AttributedResourcePath) []ExtensionResourcePath {
	paths := make([]ExtensionResourcePath, len(entries))
	for i, entry := range entries {
		paths[i] = ExtensionResourcePath{Path: entry.Path, Metadata: icodingagent.ExtensionDiscoveredPathMetadata(entry.ExtensionPath)}
	}
	return paths
}
